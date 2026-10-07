package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"quakerelay/server/internal/model"
)

func report(id, typ string, serial int, at time.Time) model.Report {
	return model.Report{ID: id, MessageID: id, EventID: "20261005120000", TelegramType: typ, Classification: "eew.forecast", EventType: "eew_forecast", Serial: &serial, ReportedAt: at, ReceivedAt: at, Raw: json.RawMessage(`{}`)}
}
func TestOrderingCancellationStreamsAndSync(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	first := report("r4", "VXSE45", 4, now)
	res, err := s.Ingest(ctx, first)
	if err != nil || !res.Current {
		t.Fatal(res, err)
	}
	res, err = s.Ingest(ctx, first)
	if err != nil || res.Inserted {
		t.Fatal("duplicate", res, err)
	}
	semantic := first
	semantic.ID = "alias"
	semantic.MessageID = "alias"
	res, err = s.Ingest(ctx, semantic)
	if err != nil || res.Inserted {
		t.Fatal("semantic duplicate", res, err)
	}
	late := report("r3", "VXSE45", 3, now.Add(time.Second))
	res, err = s.Ingest(ctx, late)
	if err != nil || res.Current || !res.Inserted {
		t.Fatal("late revision", res, err)
	}
	cancel := report("cancel", "VXSE45", 4, now.Add(2*time.Second))
	cancel.Cancelled = true
	cancel.Final = true
	cancel.EventType = "eew_cancel"
	res, err = s.Ingest(ctx, cancel)
	if err != nil || !res.Current {
		t.Fatal("same serial cancel", res, err)
	}
	revive := report("revive", "VXSE45", 5, now.Add(3*time.Second))
	res, err = s.Ingest(ctx, revive)
	if err != nil || res.Current {
		t.Fatal("cancel revived", res, err)
	}
	warning := report("warning", "VXSE43", 1, now.Add(4*time.Second))
	warning.Classification = "eew.warning"
	warning.Warning = true
	warning.EventType = "eew_warning"
	res, err = s.Ingest(ctx, warning)
	if err != nil || !res.Current {
		t.Fatal("independent serial stream", res, err)
	}
	e, reports, err := s.Event(ctx, first.EventID)
	if err != nil || len(reports) != 5 || e.Cancelled || !e.Warning {
		t.Fatal("stream state", e, len(reports), err)
	}
	page, err := s.Sync(ctx, 0, 2)
	if err != nil || len(page.Items) != 2 || !page.HasMore {
		t.Fatal(page, err)
	}
	last := page.Next
	page, err = s.Sync(ctx, last, 10)
	if err != nil || len(page.Items) != 3 || page.HasMore || page.Next <= last {
		t.Fatal(page, err)
	}
	if _, err = s.Sync(ctx, page.Latest+10, 10); err != ErrInvalid {
		t.Fatal("future cursor accepted")
	}
}

func TestOutboxFailureRollsBackReport(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	code, err := s.CreatePairing(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pair(ctx, code, "phone", "01234567890123456789012345678901", now); err != nil {
		t.Fatal(err)
	}
	if err = s.Register(ctx, "phone", model.Registration{InstallationID: "phone", DeviceToken: "aabb", Environment: "development"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`CREATE TRIGGER fail_queue BEFORE INSERT ON deliveries BEGIN SELECT RAISE(ABORT, 'simulated queue failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Ingest(ctx, report("atomic", "VXSE45", 1, now)); err == nil {
		t.Fatal("expected enqueue failure")
	}
	for _, table := range []string{"reports", "events", "streams", "deliveries"} {
		var n int
		if err = s.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("transaction not atomic", table, n, err)
		}
	}
}

func TestSerial1243FinalAndDuplicateDelivery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, err := s.CreatePairing(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pair(ctx, code, "phone", "01234567890123456789012345678901", now); err != nil {
		t.Fatal(err)
	}
	if err = s.Register(ctx, "phone", model.Registration{InstallationID: "phone", DeviceToken: "aabb", Environment: "development"}, now); err != nil {
		t.Fatal(err)
	}
	for i, serial := range []int{1, 2, 4, 3} {
		r := report(fmt.Sprintf("report-%d", serial), "VXSE45", serial, now.Add(time.Duration(i)*time.Second))
		got, err := s.Ingest(ctx, r)
		if err != nil || !got.Inserted || got.Current != (serial != 3) {
			t.Fatal(serial, got, err)
		}
		duplicate, err := s.Ingest(ctx, r)
		if err != nil || duplicate.Inserted {
			t.Fatal("duplicate was enqueued", err)
		}
	}
	event, _, err := s.Event(ctx, "20261005120000")
	if err != nil || *event.SourceSerial != 4 {
		t.Fatal("serial rolled back", event, err)
	}
	final := report("final", "VXSE45", 4, now.Add(5*time.Second))
	final.Final = true
	if got, err := s.Ingest(ctx, final); err != nil || !got.Current {
		t.Fatal("same serial final", got, err)
	}
	old := report("after-final", "VXSE45", 5, now.Add(6*time.Second))
	if got, err := s.Ingest(ctx, old); err != nil || got.Current {
		t.Fatal("final was revived", got, err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	event, all, err := s.Event(ctx, final.EventID)
	if err != nil || !event.Final || *event.SourceSerial != 4 || len(all) != 6 {
		t.Fatal("restart lost final", event, len(all), err)
	}
	counts, err := s.DeliveryCounts(ctx)
	if err != nil || counts["pending"] != 4 {
		t.Fatal("outbox duplication or rollback", counts, err)
	}
}
