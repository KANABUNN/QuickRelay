package relay

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/ingest"
)

func TestRawXMLUsesExistingOutboxAndAPNsPayload(t *testing.T) {
	for _, tc := range []struct {
		file, code, kind string
		warning, cancel  bool
	}{
		{"77_01_01_240613_VXSE45.xml", "VXSE45", "eew_forecast", false, false},
		{"37_01_01_240613_VXSE43.xml", "VXSE43", "eew_warning", true, false},
		{"77_01_33_240613_VXSE45.xml", "VXSE45", "eew_cancel", false, true},
		{"32-35_01_03_240613_VXSE53.xml", "VXSE53", "earthquake_info", false, false},
		{"38-39_02_01_250206_VTSE41.xml", "VTSE41", "tsunami_warning", true, false},
		{"38-39_02_04_250206_VTSE41.xml", "VTSE41", "tsunami_info", false, false},
	} {
		t.Run(tc.file, func(t *testing.T) {
			st, w, sender, _ := setup(t)
			data, err := os.ReadFile("../dmdata/testdata/jma/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.ReplaceAll(data, []byte("<Status>訓練</Status>"), []byte("<Status>通常</Status>"))
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			_, _ = gz.Write(data)
			_ = gz.Close()
			classification := "telegram.earthquake"
			if tc.code == "VXSE45" {
				classification = "eew.forecast"
			}
			if tc.code == "VXSE43" {
				classification = "eew.warning"
			}
			e := dmdata.Envelope{Type: "data", ID: "raw-integration-" + tc.code, Classification: classification, Format: "xml", Encoding: "base64", Compression: "gzip", Body: base64.StdEncoding.EncodeToString(buf.Bytes())}
			e.Head.Type = tc.code
			frame, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := dmdata.Normalize(frame, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			now := preview.ReportedAt.Add(time.Second)
			w.Now = func() time.Time { return now }
			ctx := context.Background()
			result, err := (ingest.Processor{Store: st}).Process(ctx, frame, now)
			if err != nil || result.State != "normalized" || !result.Ingest.Current {
				t.Fatal(result, err)
			}
			if result.Report.EventType != tc.kind || result.Report.Warning != tc.warning || result.Report.Cancelled != tc.cancel {
				t.Fatal("changed notification semantics", result.Report)
			}
			if worked, err := w.Step(ctx); err != nil || !worked || len(sender.requests) != 1 {
				t.Fatal("raw XML did not reach APNs", worked, err)
			}
			var payload map[string]any
			if err = json.Unmarshal(sender.requests[0].Payload, &payload); err != nil {
				t.Fatal(err)
			}
			aps := payload["aps"].(map[string]any)
			alert := aps["alert"].(map[string]any)
			if alert["title"] != result.Report.Title || payload["warning"] != tc.warning || payload["cancel"] != tc.cancel || payload["event_id"] != result.Report.EventID {
				t.Fatal("APNs payload differs", payload)
			}
			_, err = (ingest.Processor{Store: st}).Process(ctx, frame, now)
			if err != nil {
				t.Fatal(err)
			}
			if worked, err := w.Step(ctx); err != nil || worked || len(sender.requests) != 1 {
				t.Fatal("duplicate raw alert")
			}
			if tc.code == "VXSE43" && (strings.Contains(alert["body"].(string), "M5.8") || strings.Contains(alert["body"].(string), "深さ")) {
				t.Fatal("warning restriction bypassed")
			}
		})
	}
}
