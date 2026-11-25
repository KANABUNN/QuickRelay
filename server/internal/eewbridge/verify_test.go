package eewbridge

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"quakerelay/server/internal/ingest"
	"quakerelay/server/internal/store"
)

// relayCopy ingests frames through the relay's live path and returns the closed database file.
func relayCopy(t *testing.T, frames ...[]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relay.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range frames {
		got, err := (ingest.Processor{Store: st}).Process(context.Background(), f, sampleTime.Add(3*time.Minute+time.Duration(i)*time.Second))
		if err != nil || got.Rejection != nil || !got.Ingest.Inserted {
			t.Fatal(i, got.Rejection, err)
		}
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func verify(t *testing.T, path string) VerifyReport {
	t.Helper()
	relay, err := OpenRelayCopy(path)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	report, err := Verify(context.Background(), relay, openBridge(t))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// A forecast run with a lower-numbered cancellation and an independent warning.
func scenario(t *testing.T) [][]byte {
	base := sample(t, "77_01_02_240613_VXSE45.xml")
	frames := [][]byte{frame(t, "VXSE45", "f1", sample(t, "77_01_01_240613_VXSE45.xml"))}
	for i := 2; i <= 5; i++ {
		frames = append(frames, frame(t, "VXSE45", fmt.Sprint("f", i), withSerial(base, i)))
	}
	frames = append(frames,
		frame(t, "VXSE43", "w1", sample(t, "37_01_01_240613_VXSE43.xml")),
		frame(t, "VXSE45", "late3", withSerial(bytes.Replace(base, []byte(">4.5<"), []byte(">4.4<"), 1), 1)),
		frame(t, "VXSE45", "cancel4", withSerial(sample(t, "77_01_33_240613_VXSE45.xml"), 4)))
	return frames
}

func TestVerifyAgreesWithRelay(t *testing.T) {
	path := relayCopy(t, scenario(t)...)
	r := verify(t, path)
	if !r.Clean() || r.Reports != 8 || r.Series != 2 || len(r.StoredDrift) != 0 || len(r.Quarantined) != 0 || r.PartialWarnings != 0 {
		t.Fatalf("%+v", r)
	}
	if r.Outcomes["applied"] != 7 || r.Outcomes["stale:older_serial"] != 1 {
		t.Fatal(r.Outcomes)
	}
}

func TestVerifyReportsStoredDriftAndDisagreement(t *testing.T) {
	path := relayCopy(t, scenario(t)...)
	// A relay that stored the stream before the lower-serial cancellation fix.
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec(`UPDATE streams SET payload=(SELECT payload FROM reports WHERE telegram_type='VXSE45' ORDER BY sequence LIMIT 1 OFFSET 4)
 WHERE telegram_type='VXSE45'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r := verify(t, path)
	if !r.Clean() || len(r.StoredDrift) != 1 || r.StoredDrift[0].Relay != "active#5" || r.StoredDrift[0].Bridge != "canceled#4" {
		t.Fatalf("%+v", r.StoredDrift)
	}

	// Same serial, other content, published later: the relay takes it, the bridge quarantines it.
	base := sample(t, "77_01_02_240613_VXSE45.xml")
	conflict := bytes.Replace(base, []byte(">4.5<"), []byte(">4.9<"), 1)
	conflict = bytes.Replace(conflict, []byte("2024-04-17T23:14:58+09:00</ReportDateTime>"), []byte("2024-04-17T23:14:59+09:00</ReportDateTime>"), 1)
	path = relayCopy(t, frame(t, "VXSE45", "a", base), frame(t, "VXSE45", "b", conflict))
	r = verify(t, path)
	if r.Clean() || len(r.Disagreements) != 1 || !r.Disagreements[0].RelayCurrent || r.Disagreements[0].Reason != "same_serial_conflict" ||
		len(r.Quarantined) != 1 || len(r.Mismatches) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestVerifyNeedsEmptyBridge(t *testing.T) {
	path := relayCopy(t, scenario(t)[0])
	relay, err := OpenRelayCopy(path)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	bridge := openBridge(t)
	if _, err = Verify(context.Background(), relay, bridge); err != nil {
		t.Fatal(err)
	}
	if _, err = Verify(context.Background(), relay, bridge); err == nil {
		t.Fatal("verification mixed into an existing bridge database")
	}
	if _, err = OpenRelayCopy(filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Fatal("missing relay database opened")
	}
}
