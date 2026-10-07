package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"quakerelay/server/internal/model"
)

// Shared examples are a wire-format regression boundary for server and iOS.
func TestSharedContractExamples(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "examples", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	assertJSON := func(want, got []byte) {
		t.Helper()
		var expected, actual any
		if err := json.Unmarshal(want, &expected); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(got, &actual); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(expected, actual) {
			t.Fatalf("shared example differs from wire output\nwant: %s\ngot: %s", want, got)
		}
	}
	marshal := func(v any) []byte {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var report model.Report
	reportJSON := read("report.valid.json")
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		t.Fatal(err)
	}
	assertJSON(reportJSON, marshal(report))
	assertJSON(read("event.valid.json"), marshal(report.Event()))
	payload, err := Payload(report, model.DefaultPreferences())
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(read("push-payload.valid.json"), payload)
	page := model.SyncPage{OK: true, Items: []model.SyncItem{{Report: report, Event: report.Event()}},
		Next: 1, HasMore: false, Latest: 1, ServerTime: "2026-10-05T00:00:03Z"}
	assertJSON(read("sync.valid.json"), marshal(page))
}
