package eewbridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"quakerelay/server/internal/eew"
)

// ErrUnauthorized covers unknown, malformed and revoked tokens alike.
var ErrUnauthorized = errors.New("unauthorized")

const tokenPrefix = "eqb_"

// Token is a per-device credential. The secret is shown once at issue.
type Token struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Environment string     `json:"environment"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

func tokenHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// IssueToken creates a production-scope credential and returns its secret.
func (s *Store) IssueToken(ctx context.Context, name string, now time.Time) (Token, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return Token{}, "", ErrInvalid
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, "", err
	}
	id := make([]byte, 6)
	if _, err := rand.Read(id); err != nil {
		return Token{}, "", err
	}
	t := Token{ID: "t_" + hex.EncodeToString(id), Name: name, Environment: eew.EnvironmentProduction, CreatedAt: now.UTC()}
	secret := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	_, err := s.DB.ExecContext(ctx, "INSERT INTO tokens(id,name,token_sha256,environment,created_ms) VALUES(?,?,?,?,?)",
		t.ID, t.Name, tokenHash(secret), t.Environment, now.UnixMilli())
	return t, secret, err
}

// Authenticate returns the active token for a secret.
func (s *Store) Authenticate(ctx context.Context, secret string) (Token, error) {
	var t Token
	if !strings.HasPrefix(secret, tokenPrefix) || len(secret) != len(tokenPrefix)+43 {
		return t, ErrUnauthorized
	}
	var created int64
	err := s.DB.QueryRowContext(ctx, "SELECT id,name,environment,created_ms FROM tokens WHERE token_sha256=? AND revoked_ms IS NULL",
		tokenHash(secret)).Scan(&t.ID, &t.Name, &t.Environment, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrUnauthorized
	}
	t.CreatedAt = time.UnixMilli(created).UTC()
	return t, err
}

func (s *Store) Tokens(ctx context.Context) ([]Token, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name,environment,created_ms,revoked_ms FROM tokens ORDER BY created_ms,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		var created int64
		var revoked sql.NullInt64
		if err = rows.Scan(&t.ID, &t.Name, &t.Environment, &created, &revoked); err != nil {
			return nil, err
		}
		t.CreatedAt = time.UnixMilli(created).UTC()
		if revoked.Valid {
			at := time.UnixMilli(revoked.Int64).UTC()
			t.RevokedAt = &at
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken disables a token; running sessions close within the revocation check period.
func (s *Store) RevokeToken(ctx context.Context, id string, now time.Time) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE tokens SET revoked_ms=? WHERE id=? AND revoked_ms IS NULL", now.UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return errors.New("no active token with that id")
	}
	return nil
}

// RevokedAmong returns the ids in the list that are no longer active.
func (s *Store) RevokedAmong(ctx context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, id := range ids {
		var revoked sql.NullInt64
		err := s.DB.QueryRowContext(ctx, "SELECT revoked_ms FROM tokens WHERE id=?", id).Scan(&revoked)
		if errors.Is(err, sql.ErrNoRows) || err == nil && revoked.Valid {
			out[id] = true
		} else if err != nil {
			return nil, err
		}
	}
	return out, nil
}
