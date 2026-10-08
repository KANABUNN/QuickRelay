package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/model"
	"quakerelay/server/internal/store"
	"strings"
	"testing"
	"time"
)

type testNotificationSender struct{ requests []apns.Request }

func (f *testNotificationSender) Send(_ context.Context, r apns.Request) (apns.Result, error) {
	f.requests = append(f.requests, r)
	return apns.Result{Status: 200}, nil
}
func TestNotificationTestOwnershipIdempotencyAndNoSharedHistory(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	app := New(st, strings.Repeat("s", 32))
	fake := &testNotificationSender{}
	app.APNsConfigured = true
	app.TestSender = fake
	pair := func(id, token string) string {
		code, err := st.CreatePairing(context.Background(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		credential, err := st.Pair(context.Background(), code, id, strings.Repeat("s", 32), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err = st.Register(context.Background(), id, model.Registration{InstallationID: id, DeviceToken: token, Environment: "development"}, time.Now()); err != nil {
			t.Fatal(err)
		}
		return credential
	}
	first, second := pair("first", "aabb"), pair("second", "ccdd")
	request := func(method, path, credential, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	path := "devices/me/notification-tests"
	body := `{"request_id":"test-1","style":"warning"}`
	if w := request("POST", path, "", body); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request("POST", path, first, `{"request_id":"test-1","style":"warning","device_id":"second"}`); w.Code != 400 {
		t.Fatal("target override accepted")
	}
	for i := 0; i < 2; i++ {
		w := request("POST", path, first, body)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "apns_accepted") || strings.Contains(w.Body.String(), "aabb") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if len(fake.requests) != 1 || fake.requests[0].Token != "aabb" {
		t.Fatal("duplicate or wrong recipient")
	}
	var payload map[string]any
	_ = json.Unmarshal(fake.requests[0].Payload, &payload)
	if payload["category"] != "system_test" || !strings.Contains(string(fake.requests[0].Payload), "地震情報ではありません") {
		t.Fatal("test resembled earthquake")
	}
	if w := request("GET", path+"/test-1", second, ""); w.Code != 404 {
		t.Fatal("cross-device result disclosed")
	}
	if w := request("POST", path, first, `{"request_id":"test-2","style":"normal"}`); w.Code != 429 {
		t.Fatal("cooldown failed", w.Code)
	}
	if w := request("POST", path, second, `{"request_id":"test-3","style":"normal"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	page, err := st.Sync(context.Background(), 0, 10)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("test polluted history", err)
	}
	d, _ := st.Device(context.Background(), "first")
	d.Preferences.NotificationsEnabled = false
	if err = st.Preferences(context.Background(), "first", d.Preferences); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", path, first, `{"request_id":"test-4","style":"normal"}`); w.Code != 409 {
		t.Fatal("disabled notifications ignored")
	}
}
