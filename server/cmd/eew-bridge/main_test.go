package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/ingest"
	"quakerelay/server/internal/store"
)

func relayWith(t *testing.T, frames map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relay.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	received := time.Date(2024, 4, 17, 14, 20, 0, 0, time.UTC)
	for _, id := range []string{"a", "b"} {
		e := dmdata.Envelope{Type: "data", ID: id, Classification: "eew.forecast", Format: "xml", Encoding: "utf-8", Body: string(frames[id])}
		e.Head.Type = "VXSE45"
		frame, _ := json.Marshal(e)
		if _, err = (ingest.Processor{Store: st}).Process(context.Background(), frame, received); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	return path
}

func TestVerifyCommand(t *testing.T) {
	xml, err := os.ReadFile(filepath.Join("..", "..", "internal", "dmdata", "testdata", "jma", "77_01_02_240613_VXSE45.xml"))
	if err != nil {
		t.Fatal(err)
	}
	cancel, err := os.ReadFile(filepath.Join("..", "..", "internal", "dmdata", "testdata", "jma", "77_01_33_240613_VXSE45.xml"))
	if err != nil {
		t.Fatal(err)
	}
	path := relayWith(t, map[string][]byte{"a": xml, "b": cancel})
	var out bytes.Buffer
	if err = run([]string{"verify", "-source-db", path}, &out); err != nil {
		t.Fatal(err, out.String())
	}
	if !strings.Contains(out.String(), "EEW reports: 2") || !strings.Contains(out.String(), "Series mismatches: 0") {
		t.Fatal(out.String())
	}
	kept := filepath.Join(t.TempDir(), "bridge.db")
	out.Reset()
	if err = run([]string{"verify", "-source-db", path, "-state-db", kept, "-json"}, &out); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Reports  int            `json:"reports"`
		Outcomes map[string]int `json:"outcomes"`
	}
	if err = json.Unmarshal(out.Bytes(), &report); err != nil || report.Reports != 2 || report.Outcomes["applied"] != 2 {
		t.Fatal(out.String(), err)
	}
	if err = run([]string{"verify", "-source-db", path, "-state-db", kept}, &out); err == nil {
		t.Fatal("existing state database reused")
	}

	// Same serial with other content published later: the command fails.
	conflict := bytes.Replace(xml, []byte(">4.5<"), []byte(">4.9<"), 1)
	conflict = bytes.Replace(conflict, []byte("23:14:58+09:00</ReportDateTime>"), []byte("23:14:59+09:00</ReportDateTime>"), 1)
	path = relayWith(t, map[string][]byte{"a": xml, "b": conflict})
	if err = run([]string{"verify", "-source-db", path}, &out); !errors.Is(err, errMismatch) {
		t.Fatal("disagreement not reported", err)
	}
	for _, args := range [][]string{nil, {"serve"}, {"verify"}, {"verify", "-source-db", path, "extra"}} {
		if err = run(args, &out); err == nil {
			t.Fatal("accepted", args)
		}
	}
}
