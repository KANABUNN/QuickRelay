package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/model"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// archived stores a frame like the live processor does and returns the report and its hand-off identity.
func archived(t *testing.T, s *Store, r model.Report, frame string) (model.Report, Handoff) {
	t.Helper()
	r.Raw = []byte(frame)
	id, err := s.ArchiveFrame(context.Background(), r.Raw, r.ReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	return r, Handoff{ReceiptID: id, Upstream: "boot:1"}
}

func handoffRows(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow("SELECT count(*) FROM eew_handoff").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHandoffRecordsOnlyLiveEEWWithCommittedState(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now()
	latest, h := archived(t, s, report("r5", "VXSE45", 5, now), `{"id":"r5"}`)
	if res, err := s.IngestWithHandoff(ctx, latest, h); err != nil || !res.Current {
		t.Fatal(res, err)
	}
	late, hLate := archived(t, s, report("r4", "VXSE45", 4, now.Add(time.Second)), `{"id":"r4"}`)
	if res, err := s.IngestWithHandoff(ctx, late, hLate); err != nil || res.Current || !res.Inserted {
		t.Fatal(res, err)
	}
	warning, hWarning := archived(t, s, report("w1", "VXSE43", 1, now.Add(2*time.Second)), `{"id":"w1"}`)
	warning.Classification, warning.Warning = "eew.warning", true
	if _, err := s.IngestWithHandoff(ctx, warning, hWarning); err != nil {
		t.Fatal(err)
	}
	b, err := s.HandoffBatch(ctx, 0, "", 10, 1<<20)
	if err != nil || len(b.Items) != 3 || b.Latest != 3 || b.Gap || b.More {
		t.Fatal(b, err)
	}
	first := b.Items[0]
	if first.ReceiptID != h.ReceiptID || first.Upstream != "boot:1" || first.TelegramType != "VXSE45" || string(first.Frame) != `{"id":"r5"}` ||
		first.Applied == nil || *first.Applied.Serial != 5 || first.Applied.Cancelled || first.Applied.EventID != latest.EventID {
		t.Fatal("current report", first, first.Applied)
	}
	if b.Items[1].Applied != nil {
		t.Fatal("a report QuickRelay did not apply carried a state", b.Items[1].Applied)
	}
	if b.Items[2].TelegramType != "VXSE43" || b.Items[2].Applied == nil {
		t.Fatal("warning stream", b.Items[2])
	}

	// None of these reach the bridge: no hand-off requested, other products,
	// historical imports and duplicates already committed.
	plain := report("plain", "VXSE45", 6, now.Add(3*time.Second))
	if _, err = s.Ingest(ctx, plain); err != nil {
		t.Fatal(err)
	}
	quake, hQuake := archived(t, s, report("q", "VXSE53", 1, now), `{"id":"q"}`)
	quake.Classification, quake.EventID = "telegram.earthquake", "quake"
	if _, err = s.IngestWithHandoff(ctx, quake, hQuake); err != nil {
		t.Fatal(err)
	}
	if _, err = s.IngestHistorical(ctx, report("hist", "VXSE45", 7, now.Add(4*time.Second))); err != nil {
		t.Fatal(err)
	}
	if res, err := s.IngestWithHandoff(ctx, latest, h); err != nil || res.Inserted {
		t.Fatal("duplicate", res, err)
	}
	if n := handoffRows(t, s); n != 3 {
		t.Fatal("unexpected hand-off rows", n)
	}
}

func TestHandoffCancellationCarriesAppliedState(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now()
	latest, h := archived(t, s, report("r5", "VXSE45", 5, now), `{"id":"r5"}`)
	if _, err := s.IngestWithHandoff(ctx, latest, h); err != nil {
		t.Fatal(err)
	}
	cancel, hCancel := archived(t, s, report("c4", "VXSE45", 4, now.Add(time.Second)), `{"id":"c4"}`)
	cancel.Cancelled, cancel.Final = true, true
	if _, err := s.IngestWithHandoff(ctx, cancel, hCancel); err != nil {
		t.Fatal(err)
	}
	b, err := s.HandoffBatch(ctx, 1, h.ReceiptID, 10, 1<<20)
	if err != nil || len(b.Items) != 1 {
		t.Fatal(b, err)
	}
	if a := b.Items[0].Applied; a == nil || !a.Cancelled || !a.Final || *a.Serial != 4 {
		t.Fatal("lower-numbered cancellation must hand off as applied", a)
	}
}

func TestHandoffFailureRollsBackIngest(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_handoff BEFORE INSERT ON eew_handoff BEGIN SELECT RAISE(ABORT, 'simulated'); END`); err != nil {
		t.Fatal(err)
	}
	r, h := archived(t, s, report("atomic", "VXSE45", 1, time.Now()), `{"id":"atomic"}`)
	if _, err := s.IngestWithHandoff(ctx, r, h); err == nil {
		t.Fatal("expected hand-off failure")
	}
	for _, table := range []string{"reports", "events", "streams"} {
		var n int
		if err := s.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("ingest committed without its hand-off", table, n, err)
		}
	}
	if _, err := s.IngestWithHandoff(ctx, r, Handoff{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing receipt accepted", err)
	}
}

func TestHandoffBatchBoundsAndCursor(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now()
	receipts := []string{""}
	for i := 1; i <= 5; i++ {
		size := 100
		if i == 3 {
			size = 5000
		}
		r, h := archived(t, s, report(fmt.Sprint("r", i), "VXSE45", i, now.Add(time.Duration(i)*time.Second)),
			fmt.Sprintf(`{"id":"r%d","pad":"%s"}`, i, strings.Repeat("x", size)))
		if _, err := s.IngestWithHandoff(ctx, r, h); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, h.ReceiptID)
	}
	b, err := s.HandoffBatch(ctx, 0, "", 2, 1<<20)
	if err != nil || len(b.Items) != 2 || !b.More || b.Items[1].Seq != 2 {
		t.Fatal("row limit", b, err)
	}
	// Byte budget stops before the large frame; the large frame then comes alone.
	b, err = s.HandoffBatch(ctx, 0, "", 10, 1000)
	if err != nil || len(b.Items) != 2 || !b.More {
		t.Fatal("byte budget", len(b.Items), b.More, err)
	}
	b, err = s.HandoffBatch(ctx, 2, receipts[2], 10, 1000)
	if err != nil || len(b.Items) != 1 || b.Items[0].Seq != 3 || !b.More {
		t.Fatal("oversized frame alone", len(b.Items), err)
	}
	b, err = s.HandoffBatch(ctx, 5, receipts[5], 10, 1000)
	if err != nil || len(b.Items) != 0 || b.More || b.Latest != 5 {
		t.Fatal("caught up", b, err)
	}
	for _, tc := range []struct {
		after   int64
		receipt string
	}{{6, ""}, {3, receipts[2]}, {3, "unknown"}} {
		if _, err = s.HandoffBatch(ctx, tc.after, tc.receipt, 10, 1000); !errors.Is(err, ErrHandoffCursor) {
			t.Fatal("cursor from another history accepted", tc, err)
		}
	}
	if _, err = s.HandoffBatch(ctx, -1, "", 10, 1000); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestPruneHandoffByAgeAndCount(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now()
	receipts := []string{""}
	for i := 1; i <= 6; i++ {
		r, h := archived(t, s, report(fmt.Sprint("r", i), "VXSE45", i, now), fmt.Sprintf(`{"id":"r%d"}`, i))
		if _, err := s.IngestWithHandoff(ctx, r, h); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, h.ReceiptID)
	}
	// Age the first two rows past retention.
	if _, err := s.DB.Exec("UPDATE eew_handoff SET committed_ms=? WHERE ingress_seq<=2", now.Add(-20*time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	changed := s.HandoffChanged()
	removed, err := s.PruneHandoff(ctx, now, 15*time.Minute, 100)
	if err != nil || removed != 2 {
		t.Fatal("age", removed, err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("pruning did not wake readers")
	}
	// Count limit keeps the newest 3.
	if removed, err = s.PruneHandoff(ctx, now, 15*time.Minute, 3); err != nil || removed != 1 {
		t.Fatal("count", removed, err)
	}
	if removed, err = s.PruneHandoff(ctx, now, 15*time.Minute, 3); err != nil || removed != 0 {
		t.Fatal("idempotent", removed, err)
	}
	b, err := s.HandoffBatch(ctx, 1, receipts[1], 10, 1<<20)
	if err != nil || !b.Gap || b.PurgedThrough != 3 || len(b.Items) != 3 || b.Items[0].Seq != 4 {
		t.Fatal("reader behind the removed range", b, err)
	}
	b, err = s.HandoffBatch(ctx, 3, "", 10, 1<<20)
	if err != nil || b.Gap || len(b.Items) != 3 {
		t.Fatal("reader at the removed boundary", b, err)
	}
	if _, err = s.PruneHandoff(ctx, now.Add(time.Hour), 15*time.Minute, 3); err != nil {
		t.Fatal(err)
	}
	// Positions are never reused once every row is gone.
	b, err = s.HandoffBatch(ctx, 6, "", 10, 1<<20)
	if err != nil || b.Latest != 6 || b.PurgedThrough != 6 || len(b.Items) != 0 {
		t.Fatal("empty table", b, err)
	}
	r, h := archived(t, s, report("r7", "VXSE45", 7, now), `{"id":"r7"}`)
	if _, err = s.IngestWithHandoff(ctx, r, h); err != nil {
		t.Fatal(err)
	}
	b, err = s.HandoffBatch(ctx, 6, "", 10, 1<<20)
	if err != nil || len(b.Items) != 1 || b.Items[0].Seq != 7 || !bytes.Equal(b.Items[0].Frame, []byte(`{"id":"r7"}`)) {
		t.Fatal("position after pruning", b, err)
	}
}
