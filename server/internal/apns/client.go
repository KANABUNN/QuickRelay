// Package apns implements Apple's token-authenticated HTTP/2 provider protocol.
package apns

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	team, keyID, topic string
	key                *ecdsa.PrivateKey
	http               *http.Client
	mu                 sync.Mutex
	jwt                string
	issued             time.Time
	now                func() time.Time
	endpoint           func(string) string
}

type Request struct {
	Token, Environment string
	Payload            []byte
	Expiration         time.Time
	Priority           int
	// Stable per delivery, including retries. APNs does not promise deduplication.
	ID string
}

type Result struct {
	Status     int
	ID         string
	Reason     string
	Timestamp  int64 // APNs invalidation time, milliseconds since epoch.
	RetryAfter time.Duration
}

func (r Result) Accepted() bool  { return r.Status == http.StatusOK }
func (r Result) Retryable() bool { return r.Status == 0 || r.Status == 429 || r.Status >= 500 }
func (r Result) InvalidToken() bool {
	return r.Status == 410 && (r.Reason == "Unregistered" || r.Reason == "ExpiredToken") ||
		r.Status == 400 && (r.Reason == "BadDeviceToken" || r.Reason == "DeviceTokenNotForTopic")
}

func New(team, keyID, topic string, p8 []byte) (*Client, error) {
	if team == "" || keyID == "" || topic == "" {
		return nil, errors.New("APNS_TEAM_ID, APNS_KEY_ID and APNS_BUNDLE_ID are required")
	}
	block, rest := pem.Decode(p8)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("APNs key must be one PKCS#8 PEM private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid APNs PKCS#8 key")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("APNs key must use ECDSA P-256")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &Client{
		team: team, keyID: keyID, topic: topic, key: key, now: time.Now,
		http: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		endpoint: func(env string) string {
			if env == "development" {
				return "https://api.sandbox.push.apple.com"
			}
			return "https://api.push.apple.com"
		},
	}, nil
}

func (c *Client) token() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.jwt != "" && !now.Before(c.issued) && now.Sub(c.issued) < 50*time.Minute {
		return c.jwt, nil
	}
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": c.keyID})
	claims, _ := json.Marshal(map[string]any{"iss": c.team, "iat": now.Unix()})
	enc := base64.RawURLEncoding
	content := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(content))
	r, s, err := ecdsa.Sign(rand.Reader, c.key, digest[:])
	if err != nil {
		return "", err
	}
	// JWS requires 32-byte R followed by 32-byte S, not an ASN.1 DER signature.
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	c.jwt = content + "." + enc.EncodeToString(signature)
	c.issued = now
	return c.jwt, nil
}

func ValidToken(token string) bool {
	if len(token) < 2 || len(token) > 512 || len(token)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

func (c *Client) Send(ctx context.Context, n Request) (Result, error) {
	if !ValidToken(n.Token) {
		return Result{}, errors.New("invalid device token")
	}
	if n.Environment != "development" && n.Environment != "production" {
		return Result{}, errors.New("invalid APNs environment")
	}
	if len(n.Payload) > 4096 || !json.Valid(n.Payload) {
		return Result{}, errors.New("APNs payload must be valid JSON up to 4096 bytes")
	}
	if n.Priority != 5 && n.Priority != 10 {
		return Result{}, errors.New("invalid APNs priority")
	}
	jwt, err := c.token()
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(n.Environment)+"/3/device/"+strings.ToLower(n.Token), bytes.NewReader(n.Payload))
	if err != nil {
		return Result{}, errors.New("cannot build APNs request")
	}
	req.Header.Set("Authorization", "bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apns-topic", c.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", strconv.Itoa(n.Priority))
	expiry := int64(0)
	if !n.Expiration.IsZero() {
		expiry = n.Expiration.Unix()
	}
	req.Header.Set("apns-expiration", strconv.FormatInt(expiry, 10))
	if n.ID != "" {
		req.Header.Set("apns-id", n.ID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, errors.New("APNs transport failed")
	} // URLs contain device tokens.
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 {
		return Result{}, errors.New("APNs requires HTTP/2")
	}
	r := Result{Status: resp.StatusCode, ID: resp.Header.Get("apns-id")}
	if v, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && v > 0 {
		r.RetryAfter = time.Duration(v) * time.Second
	} else if at, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
		r.RetryAfter = at.Sub(c.now())
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if err != nil || len(body) > 8192 {
		return r, errors.New("invalid APNs response body")
	}
	if r.Status != 200 {
		var failure struct {
			Reason    string
			Timestamp int64
		}
		if err := json.Unmarshal(body, &failure); err != nil {
			return r, fmt.Errorf("APNs HTTP %d with invalid error response", r.Status)
		}
		r.Reason = failure.Reason
		r.Timestamp = failure.Timestamp
	}
	return r, nil
}
