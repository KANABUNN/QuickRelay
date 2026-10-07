package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func client(t *testing.T) *Client {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New("TEAM", "KEY", "jp.kb-dev.quickrelay", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestJWTSignatureAndCache(t *testing.T) {
	c := client(t)
	now := time.Now()
	c.now = func() time.Time { return now }
	token, err := c.token()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(&c.key.PublicKey, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("invalid ES256 JWS")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	_ = json.Unmarshal(raw, &claims)
	if claims["iss"] != "TEAM" || int64(claims["iat"].(float64)) != now.Unix() {
		t.Fatal(claims)
	}
	now = now.Add(49 * time.Minute)
	cached, _ := c.token()
	if cached != token {
		t.Fatal("JWT not cached")
	}
	now = now.Add(2 * time.Minute)
	renewed, _ := c.token()
	if renewed == token {
		t.Fatal("JWT not renewed")
	}
}
func TestSendHTTP2AndFailure(t *testing.T) {
	c := client(t)
	status := 200
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.URL.Path != "/3/device/aabb" || r.Header.Get("apns-topic") != "jp.kb-dev.quickrelay" ||
			r.Header.Get("apns-push-type") != "alert" || r.Header.Get("apns-priority") != "10" ||
			r.Header.Get("apns-expiration") != "0" || !strings.HasPrefix(r.Header.Get("Authorization"), "bearer ") {
			t.Errorf("bad request")
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"aps":{"alert":"test"}}` {
			t.Errorf("bad payload")
		}
		w.Header().Set("apns-id", "test-id")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(status)
		if status != 200 {
			_, _ = io.WriteString(w, `{"reason":"Unregistered","timestamp":1234}`)
		}
	}))
	s.EnableHTTP2 = true
	s.StartTLS()
	defer s.Close()
	c.http = s.Client()
	c.endpoint = func(string) string { return s.URL }
	req := Request{Token: "aabb", Environment: "development", Payload: []byte(`{"aps":{"alert":"test"}}`), Priority: 10}
	got, err := c.Send(context.Background(), req)
	if err != nil || !got.Accepted() || got.ID != "test-id" {
		t.Fatalf("%+v %v", got, err)
	}
	status = 410
	got, err = c.Send(context.Background(), req)
	if err != nil || !got.InvalidToken() || got.Timestamp != 1234 || got.RetryAfter != 7*time.Second {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestValidationAndClassification(t *testing.T) {
	c := client(t)
	for _, req := range []Request{
		{Token: "../secret", Environment: "development", Payload: []byte("{}"), Priority: 10},
		{Token: "aa", Environment: "typo", Payload: []byte("{}"), Priority: 10},
		{Token: "aa", Environment: "production", Payload: []byte(strings.Repeat("x", 4097)), Priority: 10},
	} {
		if _, err := c.Send(context.Background(), req); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
	if !(Result{Status: 429}).Retryable() || !(Result{Status: 503}).Retryable() || (Result{Status: 403}).Retryable() {
		t.Fatal("bad retry classification")
	}
	if (Result{Status: 403, Reason: "InvalidProviderToken"}).InvalidToken() {
		t.Fatal("provider failure must not disable a device")
	}
	if _, err := New("TEAM", "KEY", "topic", []byte("invalid")); err == nil {
		t.Fatal("invalid key accepted")
	}
}

func TestHTTP2ResponseMatrix(t *testing.T) {
	for _, tc := range []struct {
		status         int
		reason         string
		invalid, retry bool
	}{
		{200, "", false, false}, {400, "BadDeviceToken", true, false},
		{400, "PayloadEmpty", false, false}, {410, "Unregistered", true, false},
		{429, "TooManyRequests", false, true}, {500, "InternalServerError", false, true},
		{403, "BadEnvironmentKeyInToken", false, false},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			c := client(t)
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 2 {
					t.Error("HTTP/2 required")
				}
				w.Header().Set("Retry-After", "5")
				w.WriteHeader(tc.status)
				if tc.status != 200 {
					_ = json.NewEncoder(w).Encode(map[string]any{"reason": tc.reason, "timestamp": 1234})
				}
			}))
			s.EnableHTTP2 = true
			s.StartTLS()
			defer s.Close()
			c.http = s.Client()
			c.endpoint = func(string) string { return s.URL }
			got, err := c.Send(context.Background(), Request{Token: "aabb", Environment: "development", Payload: []byte(`{"aps":{"alert":"test"}}`), Priority: 10})
			if err != nil || got.Status != tc.status || got.InvalidToken() != tc.invalid || got.Retryable() != tc.retry {
				t.Fatal(got, err)
			}
		})
	}
}
