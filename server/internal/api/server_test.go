package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/store"
)

func TestAuthenticatedRegistrationAPI(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	app := New(st, strings.Repeat("s", 32))
	code, _ := st.CreatePairing(context.Background(), time.Now())
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "devices/me", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request("POST", "pair/complete", "", `{"pairing_code":"`+code+`","installation_id":"phone"}`)
	var p struct {
		Token string `json:"device_access_token"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != 200 || p.Token == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "devices/register", p.Token, `{"installation_id":"phone","device_token":"aabb","environment":"development","device_name":"test"}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "aabb") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("PUT", "devices/me", p.Token, `{"installation_id":"victim","device_token":"ccdd","environment":"production"}`)
	if w.Code != 400 {
		t.Fatal("ownership check", w.Code)
	}
	app.APNsEnvironmentAllowed = func(env string) bool { return env == "development" }
	w = request("POST", "devices/register", p.Token, `{"installation_id":"phone","device_token":"ccdd","environment":"production"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "apns_environment_unavailable") {
		t.Fatal("unconfigured environment accepted", w.Code)
	}
	d, err := st.Device(context.Background(), "phone")
	if err != nil || d.Token != "aabb" {
		t.Fatal("registration was changed on rejection")
	}
	w = request("PATCH", "devices/me/preferences", p.Token, `{"notifications_enabled":false,"event_types":[]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"notifications_enabled":false`) {
		t.Fatal(w.Body.String())
	}
	w = request("DELETE", "devices/me", p.Token, "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	if w = request("GET", "devices/me", p.Token, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
