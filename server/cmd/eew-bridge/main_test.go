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
	for _, args := range [][]string{nil, {"unknown"}, {"serve", "extra"}, {"token"}, {"verify"}, {"verify", "-source-db", path, "extra"}} {
		if err = run(args, &out); err == nil {
			t.Fatal("accepted", args)
		}
	}
}

func TestTokenCommands(t *testing.T) {
	t.Setenv("EEW_BRIDGE_DATABASE", filepath.Join(t.TempDir(), "bridge.db"))
	var out bytes.Buffer
	if err := run([]string{"token", "issue", "-name", "living room"}, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	secret := lines[len(lines)-1]
	id := strings.Fields(lines[0])[1]
	if !strings.HasPrefix(secret, "eqb_") || !strings.HasPrefix(id, "t_") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := run([]string{"token", "list"}, &out); err != nil || !strings.Contains(out.String(), "living room") || strings.Contains(out.String(), secret) {
		t.Fatal("list", out.String(), err)
	}
	if err := run([]string{"token", "revoke", id}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"token", "revoke", id}, &out); err == nil {
		t.Fatal("revoked twice")
	}
	out.Reset()
	if err := run([]string{"rotate-epoch"}, &out); err != nil || !strings.Contains(out.String(), "New delivery epoch g") {
		t.Fatal(out.String(), err)
	}
	for _, args := range [][]string{{"token", "issue"}, {"token", "revoke"}, {"token", "list", "x"}} {
		if err := run(args, &out); err == nil {
			t.Fatal("accepted", args)
		}
	}
}

func TestServeRejectsPublicListener(t *testing.T) {
	t.Setenv("EEW_BRIDGE_LISTEN", "0.0.0.0:9082")
	t.Setenv("EEW_BRIDGE_DATABASE", filepath.Join(t.TempDir(), "bridge.db"))
	for _, command := range []string{"serve", "check"} {
		if err := run([]string{command}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Fatal(command, err)
		}
	}
}

func TestCheckAndBackupCommands(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "bridge.db")
	// The env file sets the process environment; restore it afterwards.
	t.Setenv("EEW_BRIDGE_DATABASE", "")
	os.Unsetenv("EEW_BRIDGE_DATABASE")
	env := filepath.Join(dir, "bridge.env")
	if err := os.WriteFile(env, []byte("# comment\nEEW_BRIDGE_DATABASE="+database+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"-env-file", env, "check"}, &out); err != nil || !strings.Contains(out.String(), "valid") {
		t.Fatal(out.String(), err)
	}
	// Before the first start there is nothing to save, and nothing is created.
	if err := run([]string{"-env-file", env, "backup", filepath.Join(dir, "none.db")}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(database); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("backup created the bridge database")
	}
	if err := run([]string{"-env-file", env, "token", "issue", "-name", "pc"}, &out); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dir, "snapshot.db")
	if err := run([]string{"-env-file", env, "backup", snapshot}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-env-file", env, "backup", snapshot}, &out); err == nil {
		t.Fatal("overwrote an existing snapshot")
	}
	// The snapshot keeps the device tokens.
	out.Reset()
	t.Setenv("EEW_BRIDGE_DATABASE", snapshot)
	if err := run([]string{"token", "list"}, &out); err != nil || !strings.Contains(out.String(), "pc") {
		t.Fatal(out.String(), err)
	}
	for _, args := range [][]string{{"check", "x"}, {"backup"}} {
		if err := run(args, &out); err == nil {
			t.Fatal("accepted", args)
		}
	}
}
