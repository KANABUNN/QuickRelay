package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/model"
	"quakerelay/server/internal/store"
	"strings"
	"testing"
	"time"
)

type sourceFixture struct{ stats dmdata.Stats }

func (f sourceFixture) Snapshot() dmdata.Stats { return f.stats }
func TestReceiverStatusUsesFramesAndRequiresAuthentication(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()
	code, _ := st.CreatePairing(ctx, now)
	credential, err := st.Pair(ctx, code, "phone", strings.Repeat("s", 32), now)
	if err != nil {
		t.Fatal(err)
	}
	app := New(st, strings.Repeat("s", 32))
	call := func(auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/v1/status", nil)
		if auth {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		out := httptest.NewRecorder()
		app.ServeHTTP(out, req)
		return out
	}
	if w := call(false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	// A long period without earthquake data does not imply a disconnected feed.
	app.Source = sourceFixture{dmdata.Stats{Connected: true, LastFrameAt: now, LastDataAt: now.Add(-24 * time.Hour)}}
	var out struct {
		Configured bool `json:"source_configured"`
		Fresh      bool `json:"source_fresh"`
		DB         bool `json:"db"`
	}
	w := call(true)
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.Configured || !out.Fresh || !out.DB {
		t.Fatal(w.Code, err, w.Body.String())
	}
	app.Source = sourceFixture{dmdata.Stats{Connected: true, LastFrameAt: now.Add(-101 * time.Second)}}
	w = call(true)
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Fresh {
		t.Fatal("stale heartbeat declared healthy")
	}
	app.Source = nil
	w = call(true)
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Configured || out.Fresh {
		t.Fatal("offline confused with live")
	}
}
func TestPreferencePatchPreservesUnmentionedFiltersAndScopesActivityTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Close() }()
	ctx := context.Background()
	now := time.Now()
	code, _ := st.CreatePairing(ctx, now)
	credential, err := st.Pair(ctx, code, "phone", strings.Repeat("s", 32), now)
	if err != nil {
		t.Fatal(err)
	}
	app := New(st, strings.Repeat("s", 32))
	call := func(method, path, body, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1/"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		return w
	}
	patch := `{"earthquake_regions":["宮崎県"],"tsunami_regions":["宮崎県"],"minimum_intensity":"4","live_activities_enabled":true}`
	if w := call("PATCH", "devices/me/preferences", patch, credential); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("PATCH", "devices/me/preferences", `{"custom_sound_enabled":false}`, credential); w.Code != 200 {
		t.Fatal(w.Code)
	}
	d, _ := st.Device(ctx, "phone")
	if d.Preferences.MinimumIntensity != "4" || len(d.Preferences.EarthquakeRegions) != 1 || !d.Preferences.LiveActivitiesEnabled || d.Preferences.CustomSoundEnabled {
		t.Fatal("partial patch erased fields")
	}
	for _, bad := range []string{`{"minimum_intensity":"unknown"}`, `{"earthquake_regions":[" 宮崎県"]}`, `{"tsunami_regions":["宮崎県","宮崎県"]}`} {
		if w := call("PATCH", "devices/me/preferences", bad, credential); w.Code != 400 {
			t.Fatal("invalid filter accepted", w.Code)
		}
	}
	// Token writes never accept a recipient override or return the secret.
	tokenBody := `{"push_token":"ccdd"}`
	if w := call("PUT", "devices/me/live-activity/start-token", tokenBody, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("PUT", "devices/me/live-activity/start-token", `{"push_token":"ccdd","device_id":"other"}`, credential); w.Code != 400 {
		t.Fatal("override accepted")
	}
	if w := call("PUT", "devices/me/live-activity/start-token", tokenBody, credential); w.Code != 200 || strings.Contains(w.Body.String(), "ccdd") {
		t.Fatal(w.Code)
	}
	report := model.Report{ID: "r", MessageID: "m", EventID: "event1", Classification: "eew.forecast", TelegramType: "VXSE45", EventType: "eew_forecast", Raw: json.RawMessage(`{}`), ReportedAt: now, ReceivedAt: now}
	out, err := st.IngestHistorical(ctx, report)
	if err != nil {
		t.Fatal(err)
	}
	report.ServerSequence = out.Sequence
	if _, err = st.ReserveLiveStart(ctx, "phone", report, now); err != nil {
		t.Fatal(err)
	}
	if w := call("PUT", "devices/me/live-activity/token", `{"event_id":"event1","telegram_type":"VXSE43","activity_id":"a1","push_token":"eeff","start_sequence":1}`, credential); w.Code != 404 {
		t.Fatal("cross-stream token accepted", w.Code)
	}
	if w := call("PUT", "devices/me/live-activity/token", `{"event_id":"event1","telegram_type":"VXSE45","activity_id":"a1","push_token":"eeff","start_sequence":1}`, credential); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	st.Close()
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d, _ = st.Device(ctx, "phone")
	if d.Preferences.MinimumIntensity != "4" {
		t.Fatal("filters not durable")
	}
	live, err := st.LiveActivity(ctx, "phone", "event1", "VXSE45")
	if err != nil || live.Token != "eeff" {
		t.Fatal("activity registration not durable", err)
	}
}
