package relay

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/model"
	"quakerelay/server/internal/store"
)

type fakeSender struct {
	result   apns.Result
	requests []apns.Request
	err      error
}

func (f *fakeSender) Send(_ context.Context, r apns.Request) (apns.Result, error) {
	f.requests = append(f.requests, r)
	return f.result, f.err
}
func setup(t *testing.T) (*store.Store, *Worker, *fakeSender, model.Report) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	now := time.Now()
	code, _ := st.CreatePairing(ctx, now)
	_, err = st.Pair(ctx, code, "phone", strings.Repeat("s", 32), now)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Register(ctx, "phone", model.Registration{InstallationID: "phone", DeviceToken: "aabb", Environment: "development"}, now); err != nil {
		t.Fatal(err)
	}
	n := 1
	r := model.Report{ID: "report1", MessageID: "msg1", EventID: "202610050001", TelegramType: "VXSE45", Classification: "eew.forecast", EventType: "eew_forecast", Serial: &n, Title: "予報", Body: "テスト", ReportedAt: now, ReceivedAt: now, Raw: json.RawMessage(`{}`)}
	fake := &fakeSender{result: apns.Result{Status: 200}}
	worker := &Worker{Store: st, Sender: fake, Now: func() time.Time { return now }}
	return st, worker, fake, r
}
func TestDurableOutboxAndAPNsPayload(t *testing.T) {
	st, w, f, r := setup(t)
	ctx := context.Background()
	if _, err := st.Ingest(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Ingest(ctx, r); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.Step(ctx); err != nil || !worked {
		t.Fatal(worked, err)
	}
	counts, _ := st.DeliveryCounts(ctx)
	if counts["accepted"] != 1 || len(f.requests) != 1 {
		t.Fatal(counts)
	}
	req := f.requests[0]
	if !req.Expiration.IsZero() || req.Priority != 10 || req.Environment != "development" || req.ID == "" {
		t.Fatal("APNs headers", req)
	}
	var payload map[string]any
	_ = json.Unmarshal(req.Payload, &payload)
	aps := payload["aps"].(map[string]any)
	if payload["server_sequence"] != float64(1) || aps["interruption-level"] != "time-sensitive" || payload["event_id"] != r.EventID || payload["report_id"] != r.ID {
		t.Fatal(payload)
	}
	if worked, err := w.Step(ctx); err != nil || worked {
		t.Fatal("duplicate delivery")
	}
}
func TestRetryExpirySupersessionAndInvalidToken(t *testing.T) {
	t.Run("429 recovery", func(t *testing.T) {
		st, w, f, r := setup(t)
		ctx := context.Background()
		_, _ = st.Ingest(ctx, r)
		f.result = apns.Result{Status: 429, Reason: "TooManyRequests", RetryAfter: 3 * time.Second}
		_, err := w.Step(ctx)
		if err != nil {
			t.Fatal(err)
		}
		counts, _ := st.DeliveryCounts(ctx)
		if counts["retry"] != 1 {
			t.Fatal(counts)
		}
		w.Now = func() time.Time { return r.ReportedAt.Add(5 * time.Second) }
		f.result = apns.Result{Status: 200}
		_, err = w.Step(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.requests) != 2 || f.requests[0].ID != f.requests[1].ID {
			t.Fatal("unstable apns-id")
		}
	})
	t.Run("stale source", func(t *testing.T) {
		st, w, f, r := setup(t)
		r.ReportedAt = r.ReportedAt.Add(-time.Hour)
		_, _ = st.Ingest(context.Background(), r)
		_, err := w.Step(context.Background())
		if err != nil || len(f.requests) != 0 {
			t.Fatal("expired alert sent", err)
		}
	})
	t.Run("lease recovery", func(t *testing.T) {
		st, w, f, r := setup(t)
		ctx := context.Background()
		_, _ = st.Ingest(ctx, r)
		claimed, err := st.Claim(ctx, r.ReportedAt)
		if err != nil || claimed == nil {
			t.Fatal(err)
		}
		if d, err := st.Claim(ctx, r.ReportedAt.Add(time.Second)); err != nil || d != nil {
			t.Fatal("duplicate claim")
		}
		w.Now = func() time.Time { return r.ReportedAt.Add(31 * time.Second) }
		_, err = w.Step(ctx)
		if err != nil || len(f.requests) != 1 {
			t.Fatal("lease not recovered", err)
		}
	})
	t.Run("invalid token", func(t *testing.T) {
		st, w, f, r := setup(t)
		ctx := context.Background()
		_, _ = st.Ingest(ctx, r)
		f.result = apns.Result{Status: 410, Reason: "Unregistered", Timestamp: r.ReportedAt.Add(time.Second).UnixMilli()}
		_, err := w.Step(ctx)
		d, _ := st.Device(ctx, "phone")
		if err != nil || d.PushActive {
			t.Fatal("invalid token not disabled", err)
		}
	})
	t.Run("cancel supersedes queued report", func(t *testing.T) {
		st, w, f, r := setup(t)
		ctx := context.Background()
		_, _ = st.Ingest(ctx, r)
		cancel := r
		cancel.ID = "cancel"
		cancel.MessageID = "cancel"
		cancel.Cancelled = true
		cancel.Final = true
		cancel.EventType = "eew_cancel"
		cancel.ReportedAt = cancel.ReportedAt.Add(time.Second)
		_, _ = st.Ingest(ctx, cancel)
		_, err := w.Step(ctx)
		if err != nil || len(f.requests) != 0 {
			t.Fatal("cancelled report sent", err)
		}
	})
}

func TestPermanentAndServerErrors(t *testing.T) {
	for _, tc := range []struct {
		status           int
		reason, delivery string
		active           bool
	}{
		{400, "BadDeviceToken", "invalid_token", false},
		{400, "PayloadEmpty", "failed", true},
		{403, "EnvironmentNotConfigured", "failed", true},
		{500, "InternalServerError", "expired", true},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			st, w, f, r := setup(t)
			ctx := context.Background()
			if _, err := st.Ingest(ctx, r); err != nil {
				t.Fatal(err)
			}
			f.result = apns.Result{Status: tc.status, Reason: tc.reason}
			if _, err := w.Step(ctx); err != nil {
				t.Fatal(err)
			}
			counts, err := st.DeliveryCounts(ctx)
			if err != nil || counts[tc.delivery] != 1 {
				t.Fatal(counts, err)
			}
			device, err := st.Device(ctx, "phone")
			if err != nil || device.PushActive != tc.active {
				t.Fatal("wrong token invalidation", err)
			}
		})
	}
}
