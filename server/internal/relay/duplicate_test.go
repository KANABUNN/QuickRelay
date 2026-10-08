package relay

import (
	"context"
	"quakerelay/server/internal/model"
	"testing"
)

// DMDATA still sends VXSE44 before VXSE45 for the same forecast.
// Both must stay in history, but only the replacement VXSE45 may alert.
func TestLegacyAndModernForecastProduceOneAlert(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		st, w, sender, modern := setup(t)
		ctx := context.Background()
		legacy := modern
		legacy.ID, legacy.MessageID, legacy.TelegramType = "legacy", "legacy-message", "VXSE44"
		pair := []model.Report{legacy, modern}
		if reverse {
			pair[0], pair[1] = pair[1], pair[0]
		}
		for _, report := range pair {
			if _, err := st.Ingest(ctx, report); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 4; i++ {
			if _, err := w.Step(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if len(sender.requests) != 1 {
			t.Fatalf("reverse=%v: got %d alerts, want 1", reverse, len(sender.requests))
		}
		event, history, err := st.Event(ctx, modern.EventID)
		if err != nil || len(history) != 2 || event.TelegramType != "VXSE45" {
			t.Fatalf("reverse=%v: latest=%s history=%d err=%v", reverse, event.TelegramType, len(history), err)
		}
		warning := modern
		warning.ID, warning.MessageID, warning.TelegramType = "warning", "warning-message", "VXSE43"
		warning.Classification, warning.EventType, warning.Warning = "eew.warning", "eew_warning", true
		if _, err := st.Ingest(ctx, warning); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Step(ctx); err != nil {
			t.Fatal(err)
		}
		if len(sender.requests) != 2 {
			t.Fatal("warning promotion was suppressed")
		}
		cancel := modern
		cancel.ID, cancel.MessageID = "cancel-modern", "cancel-modern-message"
		cancel.EventType, cancel.Cancelled, cancel.Final = "eew_cancel", true, true
		if _, err := st.Ingest(ctx, cancel); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Step(ctx); err != nil {
			t.Fatal(err)
		}
		if len(sender.requests) != 3 {
			t.Fatal("modern cancellation was suppressed")
		}
	}
}

func TestLegacyQueuedBeforeUpgradeIsSkipped(t *testing.T) {
	st, w, sender, r := setup(t)
	ctx := context.Background()
	// Simulate a pending VXSE44 delivery created by the old binary.
	r.ID, r.MessageID, r.TelegramType = "queued-legacy", "queued-legacy-msg", "VXSE44"
	got, err := st.Ingest(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(ctx, `INSERT OR IGNORE INTO deliveries(report_sequence,device_id,next_attempt_ms,expires_ms,updated_ms)
        VALUES(?,?,?,?,?)`, got.Sequence, "phone", r.ReceivedAt.UnixMilli(), r.ReportedAt.Add(r.TTL()).UnixMilli(), r.ReceivedAt.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.requests) != 0 {
		t.Fatal("legacy queued delivery was sent")
	}
}
