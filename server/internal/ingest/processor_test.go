package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/model"
	"quakerelay/server/internal/store"
)

func setup(t testing.TB) (*store.Store, []byte, time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	data, err := os.ReadFile("../dmdata/testdata/jma/77_01_01_240613_VXSE45.xml")
	if err != nil {
		t.Fatal(err)
	}
	e := dmdata.Envelope{Type: "data", ID: "receipt-test", Classification: "eew.forecast", Format: "xml", Encoding: "utf-8", Body: string(data)}
	e.Head.Type = "VXSE45"
	frame, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2024, 4, 17, 14, 14, 58, 0, time.UTC)
	ctx := context.Background()
	code, err := st.CreatePairing(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pair(ctx, code, "phone", strings.Repeat("s", 32), now); err != nil {
		t.Fatal(err)
	}
	if err = st.Register(ctx, "phone", model.Registration{InstallationID: "phone", DeviceToken: "aabb", Environment: "development"}, now); err != nil {
		t.Fatal(err)
	}
	return st, frame, now
}
func count(t testing.TB, st *store.Store, query string) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestArchiveRoundTripDuplicatesAndHistoricalReplay(t *testing.T) {
	st, frame, now := setup(t)
	p := Processor{Store: st}
	ctx := context.Background()
	a, err := p.Process(ctx, frame, now)
	if err != nil || a.State != "normalized" || !a.Ingest.Inserted {
		t.Fatal(a, err)
	}
	b, err := p.Process(ctx, frame, now.Add(time.Second))
	if err != nil || b.Ingest.Inserted {
		t.Fatal("duplicate entered history", err)
	}
	var saved []byte
	var state string
	var receptions int
	if err = st.DB.QueryRow("SELECT frame,state,receptions FROM received_telegrams WHERE frame_sha256=?", a.ReceiptID).Scan(&saved, &state, &receptions); err != nil || !bytes.Equal(saved, frame) || state != "normalized" || receptions != 2 {
		t.Fatal("lossless receipt journal", err, state, receptions)
	}
	if count(t, st, "SELECT count(*) FROM reports") != 1 || count(t, st, "SELECT count(*) FROM deliveries") != 1 {
		t.Fatal("duplicates enqueued")
	}
	// Same upstream ID in JSON rollback mode must not enqueue a second alert.
	data, err := os.ReadFile("../dmdata/testdata/eew-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var e dmdata.Envelope
	_ = json.Unmarshal(frame, &e)
	e.Format, e.Body = "json", string(data)
	alias, _ := json.Marshal(e)
	b, err = p.Process(ctx, alias, now)
	if err != nil || b.Ingest.Inserted || count(t, st, "SELECT count(*) FROM deliveries") != 1 {
		t.Fatal("mode transition duplicated", err)
	}
	e.ID = "historical-message"
	e.Body = strings.Replace(e.Body, "\"serialNo\": \"1\"", "\"serialNo\": \"2\"", 1)
	history, _ := json.Marshal(e)
	b, err = (Processor{Store: st, Historical: true}).Process(ctx, history, now)
	if err != nil || b.State != "normalized" || !b.Ingest.Inserted || count(t, st, "SELECT count(*) FROM deliveries") != 1 {
		t.Fatal("replay enqueued alert", err)
	}
}
func TestRejectedIgnoredAndMalformedFramesAreDurable(t *testing.T) {
	st, frame, now := setup(t)
	p := Processor{Store: st}
	ctx := context.Background()
	var e dmdata.Envelope
	_ = json.Unmarshal(frame, &e)
	for _, tc := range []struct {
		name, state string
		change      func(*dmdata.Envelope)
	}{
		{"unknown_format", "ignored", func(e *dmdata.Envelope) { e.Format = "future" }},
		{"unknown_code", "ignored", func(e *dmdata.Envelope) { e.Head.Type = "NEW000" }},
		{"missing_xml", "rejected", func(e *dmdata.Envelope) { e.Body = "<Report>" }},
		{"bad_base64", "rejected", func(e *dmdata.Envelope) { e.Encoding = "base64"; e.Body = "!bad" }},
		{"xml_training", "ignored", func(e *dmdata.Envelope) {
			e.Body = strings.Replace(e.Body, "<Status>通常</Status>", "<Status>訓練</Status>", 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := e
			tc.change(&v)
			raw, _ := json.Marshal(v)
			got, err := p.Process(ctx, raw, now)
			if err != nil || got.State != tc.state {
				t.Fatal(got, err)
			}
		})
	}
	got, err := p.Process(ctx, []byte("{malformed"), now)
	if err != nil || got.State != "rejected" {
		t.Fatal(got, err)
	}
	if count(t, st, "SELECT count(*) FROM received_telegrams") != 6 || count(t, st, "SELECT count(*) FROM reports") != 0 || count(t, st, "SELECT count(*) FROM deliveries") != 0 {
		t.Fatal("invalid input reached outbox or was discarded")
	}
}
func TestPersistenceFailuresStopBeforeNotification(t *testing.T) {
	for _, stage := range []string{"archive", "ingest", "completion"} {
		t.Run(stage, func(t *testing.T) {
			st, frame, now := setup(t)
			var query string
			switch stage {
			case "archive":
				query = "CREATE TRIGGER fail_archive BEFORE INSERT ON received_telegrams BEGIN SELECT RAISE(ABORT,'test'); END"
			case "ingest":
				query = "CREATE TRIGGER fail_ingest BEFORE INSERT ON reports BEGIN SELECT RAISE(ABORT,'test'); END"
			case "completion":
				query = "CREATE TRIGGER fail_completion BEFORE UPDATE ON received_telegrams BEGIN SELECT RAISE(ABORT,'test'); END"
			}
			if _, err := st.DB.Exec(query); err != nil {
				t.Fatal(err)
			}
			got, err := (Processor{Store: st}).Process(context.Background(), frame, now)
			if err == nil {
				t.Fatal("persistence failure ignored")
			}
			if stage == "archive" && count(t, st, "SELECT count(*) FROM reports") != 0 {
				t.Fatal("processed before archiving")
			}
			if stage == "ingest" {
				var state string
				if err := st.DB.QueryRow("SELECT state FROM received_telegrams WHERE frame_sha256=?", got.ReceiptID).Scan(&state); err != nil || state != "pending" {
					t.Fatal("receipt lost on failed ingestion", err, state)
				}
				if count(t, st, "SELECT count(*) FROM deliveries") != 0 {
					t.Fatal("outbox created without report")
				}
			}
			// A completion failure is fatal; the preceding report/outbox commit is valid.
			if stage == "completion" && count(t, st, "SELECT count(*) FROM reports") != 1 {
				t.Fatal("committed report lost")
			}
		})
	}
}
func TestReceiptSurvivesCloseAndReopen(t *testing.T) {
	st, frame, now := setup(t)
	path := ""
	if err := st.DB.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	id, err := st.ArchiveFrame(context.Background(), frame, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var raw []byte
	var state string
	if err = reopened.DB.QueryRow("SELECT frame,state FROM received_telegrams WHERE frame_sha256=?", id).Scan(&raw, &state); err != nil || state != "pending" || !bytes.Equal(raw, frame) {
		t.Fatal("pending receipt not durable", state, err)
	}
	got, err := (Processor{Store: reopened, Historical: true}).Process(context.Background(), raw, now)
	if err != nil || got.State != "normalized" || count(t, reopened, "SELECT count(*) FROM deliveries") != 0 {
		t.Fatal("pending recovery", got, err)
	}
}

func TestEmptyFrameIsArchivedAndReceptionCanContinue(t *testing.T) {
	st, frame, now := setup(t)
	processor := Processor{Store: st}
	ctx := context.Background()
	result, err := processor.Process(ctx, nil, now)
	if err != nil || result.State != "rejected" || result.Rejection == nil {
		t.Fatal("empty frame stopped reception", result, err)
	}
	var size int
	if err := st.DB.QueryRow("SELECT length(frame) FROM received_telegrams WHERE frame_sha256=?", result.ReceiptID).Scan(&size); err != nil || size != 0 {
		t.Fatal("empty receipt missing", size, err)
	}
	result, err = processor.Process(ctx, frame, now)
	if err != nil || result.State != "normalized" || !result.Ingest.Inserted {
		t.Fatal("next valid frame lost", result, err)
	}
}

// frameWithSerial returns a distinct live VXSE45 frame for the sample telegram.
func frameWithSerial(t testing.TB, frame []byte, serial int) []byte {
	t.Helper()
	var e dmdata.Envelope
	if err := json.Unmarshal(frame, &e); err != nil {
		t.Fatal(err)
	}
	e.ID = fmt.Sprintf("receipt-%d", serial)
	e.Body = strings.Replace(e.Body, "<Serial>1</Serial>", fmt.Sprintf("<Serial>%d</Serial>", serial), 1)
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLiveHandoffRecordsReceiptAndGeneration(t *testing.T) {
	st, frame, now := setup(t)
	ctx := context.Background()
	got, err := (Processor{Store: st, Handoff: true, Upstream: "boot:7"}).Process(ctx, frame, now)
	if err != nil || !got.Ingest.Inserted {
		t.Fatal(got, err)
	}
	var receipt, upstream string
	var reportSequence int64
	if err = st.DB.QueryRow("SELECT receipt_id,upstream_generation,report_sequence FROM eew_handoff").Scan(&receipt, &upstream, &reportSequence); err != nil {
		t.Fatal(err)
	}
	if receipt != got.ReceiptID || upstream != "boot:7" || reportSequence != got.Ingest.Sequence {
		t.Fatal("hand-off identity", receipt, upstream, reportSequence)
	}
	// Disabled hand-off and historical replay leave no rows; APNs outbox is unchanged.
	if _, err = (Processor{Store: st}).Process(ctx, frameWithSerial(t, frame, 2), now); err != nil {
		t.Fatal(err)
	}
	if _, err = (Processor{Store: st, Historical: true, Handoff: true}).Process(ctx, frameWithSerial(t, frame, 3), now); err != nil {
		t.Fatal(err)
	}
	if count(t, st, "SELECT count(*) FROM eew_handoff") != 1 || count(t, st, "SELECT count(*) FROM deliveries") != 2 {
		t.Fatal("hand-off scope or outbox changed")
	}
}

// Compare with: go test ./internal/ingest -bench Process -benchtime 500x
func BenchmarkProcess(b *testing.B) {
	for _, handoff := range []bool{false, true} {
		b.Run(fmt.Sprintf("handoff=%v", handoff), func(b *testing.B) {
			st, frame, now := setup(b)
			p := Processor{Store: st, Handoff: handoff, Upstream: "boot:1"}
			frames := make([][]byte, b.N)
			for i := range frames {
				frames[i] = frameWithSerial(b, frame, i+1)
			}
			b.ResetTimer()
			for i := range b.N {
				if _, err := p.Process(context.Background(), frames[i], now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
