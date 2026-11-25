package eewbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/eew"
)

var sampleTime = time.Date(2024, 4, 17, 14, 14, 58, 0, time.UTC)

func sample(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "dmdata", "testdata", "jma", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func withSerial(xml []byte, serial int) []byte {
	start := bytes.Index(xml, []byte("<Serial>"))
	end := bytes.Index(xml, []byte("</Serial>"))
	out := append([]byte{}, xml[:start]...)
	out = append(out, fmt.Sprintf("<Serial>%d</Serial>", serial)...)
	return append(out, xml[end+len("</Serial>"):]...)
}

func frame(t testing.TB, code, id string, xml []byte) []byte {
	t.Helper()
	classification := map[string]string{"VXSE45": "eew.forecast", "VXSE43": "eew.warning", "VXSE53": "telegram.earthquake"}[code]
	e := dmdata.Envelope{Type: "data", ID: id, Classification: classification, Format: "xml", Encoding: "utf-8", Body: string(xml)}
	e.Head.Type = code
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func openBridge(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func live(frame []byte, relay *eew.RelayState) Input {
	return Input{Frame: frame, ReceivedAt: sampleTime, Origin: eew.OriginHistorical, Upstream: "u1", Relay: relay}
}

func applied(serial int) *eew.RelayState { return &eew.RelayState{Serial: &serial} }

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestProcessAppliesRecordsAndLogs(t *testing.T) {
	ctx := context.Background()
	s := openBridge(t)
	now := time.Now()
	first := frame(t, "VXSE45", "f1", sample(t, "77_01_01_240613_VXSE45.xml"))
	out, err := s.Process(ctx, live(first, applied(1)), now)
	if err != nil || out.Outcome != eew.Applied || out.LogSeq != 1 || out.EventID != "20240417231454" || out.TelegramID != "f1" {
		t.Fatal(out, err)
	}
	state, err := s.State(ctx, eew.EnvironmentProduction, "20240417231454")
	if err != nil || state == nil || state.Event.Forecast == nil || state.Event.Revision != 1 || state.Meta.Forecast.Upstream != "u1" {
		t.Fatal("state", state, err)
	}
	warning := frame(t, "VXSE43", "w1", sample(t, "37_01_01_240613_VXSE43.xml"))
	if out, err = s.Process(ctx, live(warning, applied(1)), now); err != nil || out.LogSeq != 2 || out.ChangedSeries != eew.SeriesWarning {
		t.Fatal(out, err)
	}
	log, err := s.Log(ctx, StreamProduction, 0, 10)
	if err != nil || len(log) != 2 || log[0].Seq != 1 || log[1].Seq != 2 || log[1].Event.Revision != 2 || log[1].Cause != "telegram" ||
		log[1].Event.Warning == nil || log[1].Event.Forecast == nil {
		t.Fatal("log", log, err)
	}
	stream, err := s.Stream(ctx, StreamProduction)
	if err != nil || stream.LastSeq != 2 || len(stream.Epoch) != 17 {
		t.Fatal(stream, err)
	}

	// Same frame: nothing new. Same telegram in other bytes: duplicate.
	if out, err = s.Process(ctx, live(first, applied(1)), now); err != nil || out.Outcome != eew.Duplicate || out.Reason != "receipt_seen" {
		t.Fatal(out, err)
	}
	reencoded := bytes.Replace(first, []byte(`"head":{`), []byte(`"head":{"author":"x",`), 1)
	if out, err = s.Process(ctx, live(reencoded, applied(1)), now); err != nil || out.Outcome != eew.Duplicate || out.Reason != "telegram_seen" {
		t.Fatal(out, err)
	}
	// Same telegram ID with another body is quarantined, not applied.
	other := frame(t, "VXSE45", "f1", withSerial(sample(t, "77_01_02_240613_VXSE45.xml"), 2))
	if out, err = s.Process(ctx, live(other, applied(2)), now); err != nil || out.Outcome != eew.Quarantined || out.Reason != "telegram_id_conflict" {
		t.Fatal(out, err)
	}
	if countRows(t, s, "inputs") != 4 || countRows(t, s, "stream_log") != 2 {
		t.Fatal("rows")
	}
}

func TestProcessRecordsIgnoredAndUnparsedFrames(t *testing.T) {
	ctx := context.Background()
	s := openBridge(t)
	training := bytes.Replace(sample(t, "77_01_01_240613_VXSE45.xml"), []byte("<Status>通常</Status>"), []byte("<Status>訓練</Status>"), 1)
	converted := frame(t, "VXSE45", "json", sample(t, "77_01_01_240613_VXSE45.xml"))
	converted = bytes.Replace(converted, []byte(`"format":"xml"`), []byte(`"format":"json"`), 1)
	for _, tc := range []struct {
		name, outcome string
		frame         []byte
	}{
		{"training", eew.Ignored, frame(t, "VXSE45", "training", training)},
		{"json", eew.Quarantined, converted},
		{"malformed", eew.Quarantined, []byte("{")},
	} {
		out, err := s.Process(ctx, live(tc.frame, nil), time.Now())
		if err != nil || out.Outcome != tc.outcome {
			t.Fatal(tc.name, out, err)
		}
	}
	if countRows(t, s, "inputs") != 3 || countRows(t, s, "events") != 0 || countRows(t, s, "stream_log") != 0 {
		t.Fatal("unparsed input changed state")
	}
	if _, err := s.Process(ctx, Input{Frame: []byte("{}")}, time.Now()); err != ErrInvalid {
		t.Fatal("missing origin accepted", err)
	}
}

func TestProcessIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := openBridge(t)
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_log BEFORE INSERT ON stream_log BEGIN SELECT RAISE(ABORT, 'simulated'); END`); err != nil {
		t.Fatal(err)
	}
	f := frame(t, "VXSE45", "f1", sample(t, "77_01_01_240613_VXSE45.xml"))
	if _, err := s.Process(ctx, live(f, applied(1)), time.Now()); err == nil {
		t.Fatal("expected failure")
	}
	stream, _ := s.Stream(ctx, StreamProduction)
	if countRows(t, s, "inputs") != 0 || countRows(t, s, "events") != 0 || stream.LastSeq != 0 {
		t.Fatal("partial commit")
	}
	// Retrying after the fault applies exactly once.
	if _, err := s.DB.Exec("DROP TRIGGER fail_log"); err != nil {
		t.Fatal(err)
	}
	if out, err := s.Process(ctx, live(f, applied(1)), time.Now()); err != nil || out.LogSeq != 1 {
		t.Fatal(out, err)
	}
}

func TestPruneAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bridge.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	base := sample(t, "77_01_02_240613_VXSE45.xml")
	for i := 2; i <= 6; i++ {
		f := frame(t, "VXSE45", fmt.Sprint("f", i), withSerial(base, i))
		if out, err := s.Process(ctx, live(f, applied(i)), now.Add(-time.Duration(30-i)*time.Minute)); err != nil || out.Outcome != eew.Applied {
			t.Fatal(i, out, err)
		}
	}
	removed, err := s.Prune(ctx, now, Retention{LogAge: 26 * time.Minute, LogRows: 2, StateAge: 24 * time.Hour})
	if err != nil || removed != 3 {
		t.Fatal("count limit", removed, err)
	}
	stream, _ := s.Stream(ctx, StreamProduction)
	if stream.PurgedThrough != 3 || stream.LastSeq != 5 {
		t.Fatal(stream)
	}
	if removed, err = s.Prune(ctx, now.Add(25*time.Hour), DefaultRetention); err != nil || removed != 2 {
		t.Fatal("age", removed, err)
	}
	if countRows(t, s, "events") != 0 || countRows(t, s, "inputs") != 0 {
		t.Fatal("expired state kept")
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	again, err := s.Stream(ctx, StreamProduction)
	if err != nil || again.Epoch != stream.Epoch || again.LastSeq != 5 || again.PurgedThrough != 5 {
		t.Fatal("restart changed the stream", again, stream, err)
	}
}
