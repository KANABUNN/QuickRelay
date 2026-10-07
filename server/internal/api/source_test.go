package api

import (
	"bytes"
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

func TestSourceDocumentAuthenticationAndEEWBoundary(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()
	code, _ := st.CreatePairing(ctx, now)
	token, err := st.Pair(ctx, code, "phone", strings.Repeat("s", 32), now)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Register(ctx, "phone", model.Registration{InstallationID: "phone", DeviceToken: "aabb", Environment: "development"}, now); err != nil {
		t.Fatal(err)
	}
	e := dmdata.Envelope{Type: "data", ID: "text", Classification: "telegram.earthquake", Format: "a/n", Encoding: "utf-8", Body: "SYNTHETIC TEST ONLY"}
	e.Head.Type = "WEPA60"
	e.Head.Time = now
	raw, _ := json.Marshal(e)
	report, err := dmdata.Normalize(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Ingest(ctx, report); err != nil {
		t.Fatal(err)
	}
	warning := model.Report{ID: "warning", EventID: "123", MessageID: "warning", Classification: "eew.warning", TelegramType: "VXSE43", ReportedAt: now, ReceivedAt: now, Raw: raw, Bulletin: report.Bulletin}
	if _, err = st.Ingest(ctx, warning); err != nil {
		t.Fatal(err)
	}
	app := New(st, strings.Repeat("s", 32))
	for _, tc := range []struct {
		id, token string
		status    int
	}{{report.ID, "", 401}, {report.ID, token, 200}, {"warning", token, 404}} {
		req := httptest.NewRequest("GET", "/api/v1/reports/"+tc.id+"/source", nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		out := httptest.NewRecorder()
		app.ServeHTTP(out, req)
		if out.Code != tc.status {
			t.Fatal(out.Code)
		}
		if tc.status == 200 && (!bytes.Equal(out.Body.Bytes(), []byte(e.Body)) || out.Header().Get("Cache-Control") != "no-store" || !strings.Contains(out.Header().Get("Content-Disposition"), ".txt")) {
			t.Fatal("source transport differs")
		}
	}
	var count int
	if err = st.DB.QueryRow("SELECT count(*) FROM deliveries WHERE report_sequence=(SELECT sequence FROM reports WHERE id=?)", report.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("raw document queued push", count, err)
	}
}
