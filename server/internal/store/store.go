package store

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/model"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ DB *sql.DB }

var ErrUnauthorized = errors.New("unauthorized")
var ErrInvalid = errors.New("invalid request")
var ErrNotFound = errors.New("not found")
var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func ValidID(s string) bool   { return identifier.MatchString(s) }
func Hash(s string) string    { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func Time(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	for _, q := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY)"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, entry := range entries {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE name=?", entry.Name()).Scan(&n); err != nil {
			db.Close()
			return nil, err
		}
		if n > 0 {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			db.Close()
			return nil, err
		}
		tx, err := db.Begin()
		if err != nil {
			db.Close()
			return nil, err
		}
		if _, err = tx.Exec(string(body)); err == nil {
			_, err = tx.Exec("INSERT INTO schema_migrations(name) VALUES(?)", entry.Name())
		}
		if err != nil {
			tx.Rollback()
			db.Close()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) CreatePairing(ctx context.Context, now time.Time) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%08d", n.Int64())
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM pairing WHERE expires <= ? OR installation_id = ''", now.Unix()); err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO pairing(code_hash,expires) VALUES(?,?)", Hash(code), now.Add(10*time.Minute).Unix())
	if err != nil {
		return "", err
	}
	return code, tx.Commit()
}
func (s *Store) Pair(ctx context.Context, code, id, secret string, now time.Time) (string, error) {
	if len(secret) < 32 {
		return "", errors.New("PAIRING_SECRET must be at least 32 characters")
	}
	if len(code) != 8 || strings.Trim(code, "0123456789") != "" || !ValidID(id) {
		return "", ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var expires int64
	var claimed string
	var failures int
	err = tx.QueryRowContext(ctx, "SELECT expires,installation_id,failures FROM pairing WHERE code_hash=?", Hash(code)).Scan(&expires, &claimed, &failures)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (expires <= now.Unix() || failures >= 5 || claimed != "" && claimed != id) {
		if _, e := tx.ExecContext(ctx, "UPDATE pairing SET failures=failures+1 WHERE installation_id='' AND expires>?", now.Unix()); e != nil {
			return "", e
		}
		if e := tx.Commit(); e != nil {
			return "", e
		}
		return "", ErrUnauthorized
	}
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("device:" + code + ":" + id))
	token := hex.EncodeToString(mac.Sum(nil))
	if claimed == "" {
		prefs, _ := json.Marshal(model.DefaultPreferences())
		_, err = tx.ExecContext(ctx, `INSERT INTO devices(installation_id,credential_hash,preferences,last_seen_at) VALUES(?,?,?,?)
   ON CONFLICT(installation_id) DO UPDATE SET credential_hash=excluded.credential_hash,revoked=0`, id, Hash(token), string(prefs), Time(now))
		if err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE pairing SET installation_id=? WHERE code_hash=?", id, Hash(code)); err != nil {
			return "", err
		}
	} else {
		// Replay is allowed only for the same installation and current credential.
		var hash string
		var revoked bool
		if err = tx.QueryRowContext(ctx, "SELECT credential_hash,revoked FROM devices WHERE installation_id=?", id).Scan(&hash, &revoked); err != nil {
			return "", err
		}
		if revoked || hash != Hash(token) {
			return "", ErrUnauthorized
		}
	}
	return token, tx.Commit()
}
func (s *Store) Authenticate(ctx context.Context, token string) (string, error) {
	if len(token) != 64 {
		return "", ErrUnauthorized
	}
	var id string
	err := s.DB.QueryRowContext(ctx, "SELECT installation_id FROM devices WHERE credential_hash=? AND revoked=0", Hash(token)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnauthorized
	}
	return id, err
}
func ValidatePreferences(p model.Preferences) error {
	if len(p.EventTypes) > 20 {
		return ErrInvalid
	}
	allowed := map[string]bool{"eew_forecast": true, "eew_warning": true, "eew_cancel": true, "earthquake_info": true, "earthquake_update": true, "system_test": true}
	for _, typ := range p.EventTypes {
		if !allowed[typ] {
			return ErrInvalid
		}
	}
	return nil
}
func (s *Store) Register(ctx context.Context, id string, r model.Registration, now time.Time) error {
	if r.Environment == "sandbox" {
		r.Environment = "development"
	} // Legacy iOS input.
	if id != r.InstallationID || !apns.ValidToken(r.DeviceToken) ||
		r.Environment != "development" && r.Environment != "production" ||
		len(r.DeviceName) > 256 || len(r.AppVersion) > 64 || len(r.OSVersion) > 64 {
		return ErrInvalid
	}
	if r.Preferences != nil {
		if err := ValidatePreferences(*r.Preferences); err != nil {
			return err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	token := strings.ToLower(r.DeviceToken)
	// A reinstallation must not leave duplicate recipients for the same APNs token.
	if _, err = tx.ExecContext(ctx, "UPDATE devices SET token='',push_active=0 WHERE token=? AND environment=? AND installation_id<>?", token, r.Environment, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE devices SET token=?,environment=?,push_active=1,token_updated_ms=?,
 device_name=?,app_version=?,os_version=?,last_seen_at=? WHERE installation_id=? AND revoked=0`,
		token, r.Environment, now.UnixMilli(), r.DeviceName, r.AppVersion, r.OSVersion, Time(now), id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrUnauthorized
	}
	if r.Preferences != nil {
		p, _ := json.Marshal(r.Preferences)
		if _, err = tx.ExecContext(ctx, "UPDATE devices SET preferences=? WHERE installation_id=?", string(p), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Device(ctx context.Context, id string) (model.Device, error) {
	var d model.Device
	var p string
	err := s.DB.QueryRowContext(ctx, `SELECT installation_id,device_name,environment,app_version,os_version,NOT revoked,
 preferences,last_seen_at,token,token_updated_ms,push_active FROM devices WHERE installation_id=?`, id).
		Scan(&d.InstallationID, &d.DeviceName, &d.Environment, &d.AppVersion, &d.OSVersion, &d.Active, &p, &d.LastSeenAt, &d.Token, &d.TokenUpdatedMS, &d.PushActive)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	err = json.Unmarshal([]byte(p), &d.Preferences)
	return d, err
}
func (s *Store) Preferences(ctx context.Context, id string, p model.Preferences) error {
	if err := ValidatePreferences(p); err != nil {
		return err
	}
	b, _ := json.Marshal(p)
	_, err := s.DB.ExecContext(ctx, "UPDATE devices SET preferences=? WHERE installation_id=?", string(b), id)
	return err
}
func (s *Store) Revoke(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE devices SET revoked=1,push_active=0,token='' WHERE installation_id=?", id)
	return err
}
func (s *Store) InvalidateToken(ctx context.Context, d model.Device, invalidatedMS int64) error {
	// Ignore old 410 responses after token rotation / re-registration.
	if invalidatedMS > 0 && invalidatedMS < d.TokenUpdatedMS {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE devices SET push_active=0 WHERE installation_id=? AND token=? AND environment=? AND token_updated_ms=?`,
		d.InstallationID, d.Token, d.Environment, d.TokenUpdatedMS)
	return err
}
