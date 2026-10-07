package apns

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentScopedProvider(t *testing.T) {
	keys := make(map[string]Credentials)
	for _, env := range []string{"development", "production"} {
		raw, err := x509.MarshalPKCS8PrivateKey(client(t).key)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "test.p8")
		if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), 0600); err != nil {
			t.Fatal(err)
		}
		keys[env] = Credentials{KeyID: env, KeyFile: file}
	}
	p, err := NewProvider("TEAM", "jp.kb-dev.quickrelay", keys)
	if err != nil {
		t.Fatal(err)
	}
	for env, c := range p.clients {
		s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "bearer "), ".")
			if len(parts) != 3 {
				t.Error("missing JWT")
				return
			}
			raw, _ := base64.RawURLEncoding.DecodeString(parts[0])
			var h map[string]string
			_ = json.Unmarshal(raw, &h)
			if h["kid"] != env || h["alg"] != "ES256" || r.Header.Get("apns-topic") != "jp.kb-dev.quickrelay" {
				t.Error("wrong environment key or topic")
			}
			w.WriteHeader(200)
		}))
		s.EnableHTTP2 = true
		s.StartTLS()
		defer s.Close()
		c.http = s.Client()
		c.endpoint = func(string) string { return s.URL }
		result, err := p.Send(context.Background(), Request{Token: "aabb", Environment: env, Payload: []byte(`{"aps":{"alert":"test"}}`), Priority: 10})
		if err != nil || !result.Accepted() {
			t.Fatal(result, err)
		}
	}
	delete(p.clients, "production")
	if p.Supports("production") || !p.Supports("sandbox") {
		t.Fatal("environment availability")
	}
	result, err := p.Send(context.Background(), Request{Environment: "production"})
	if err != nil || result.Retryable() || result.InvalidToken() || result.Status != 403 {
		t.Fatal(result, err)
	}
}
