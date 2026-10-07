package dmdata

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func frame(t *testing.T, code string, body map[string]any) []byte {
	t.Helper()
	classification := "telegram.earthquake"
	schema := "earthquake-information"
	if code == "VXSE45" {
		classification = "eew.forecast"
		schema = "eew-information"
	}
	if code == "VXSE43" {
		classification = "eew.warning"
		schema = "eew-information"
	}
	converted := map[string]any{"_schema": map[string]string{"type": schema, "version": "1.0.0"}, "eventId": "20261005120000", "serialNo": "3", "status": "通常", "infoType": "発表", "reportDateTime": "2026-10-05T03:00:05Z", "title": "震源・震度情報", "body": body}
	b, _ := json.Marshal(converted)
	var zip bytes.Buffer
	gz := gzip.NewWriter(&zip)
	_, _ = gz.Write(b)
	_ = gz.Close()
	e := map[string]any{"type": "data", "id": "message-1", "classification": classification, "head": map[string]any{"type": code, "test": false}, "format": "json", "encoding": "base64", "compression": "gzip", "body": base64.StdEncoding.EncodeToString(zip.Bytes())}
	out, _ := json.Marshal(e)
	return out
}
func TestOfficialEnvelopeAndNormalization(t *testing.T) {
	now := time.Date(2026, 10, 5, 3, 0, 10, 0, time.UTC)
	for _, code := range []string{"VXSE45", "VXSE43", "VXSE51", "VXSE52", "VXSE53"} {
		t.Run(code, func(t *testing.T) {
			b := map[string]any{"isLastInfo": true, "isWarning": true, "earthquake": map[string]any{"originTime": "2026-10-05T03:00:00Z", "hypocenter": map[string]any{"name": "日向灘", "depth": map[string]string{"value": "10"}}, "magnitude": map[string]string{"value": "6.3"}}, "intensity": map[string]any{"maxInt": "5-", "forecastMaxInt": map[string]string{"from": "5-", "to": "5+"}}}
			r, err := Normalize(frame(t, code, b), now)
			if err != nil {
				t.Fatal(err)
			}
			if r.EventID != "20261005120000" || *r.Serial != 3 || r.Final != r.IsEEW() {
				t.Fatal(r)
			}
			if code == "VXSE43" && (r.Magnitude != nil || r.DepthKM != nil || strings.Contains(r.Body, "6.3")) {
				t.Fatal("warning leaked magnitude/depth")
			}
			if code == "VXSE43" && (r.Hypocenter.Magnitude != nil || r.Hypocenter.DepthKM != nil ||
				!strings.Contains(r.Body, "日向灘") || !strings.Contains(r.Body, "予想最大震度5弱〜5強")) {
				t.Fatal("warning details or restrictions differ", r)
			}
			if code == "VXSE45" && !strings.Contains(r.Body, "予想最大震度5弱〜5強") {
				t.Fatal(r.Body)
			}
		})
	}
}
func TestCancellationAssumedTrainingAndMalformed(t *testing.T) {
	now := time.Date(2026, 10, 5, 3, 0, 10, 0, time.UTC)
	r, err := Normalize(frame(t, "VXSE45", map[string]any{"isCanceled": true}), now)
	if err != nil || !r.Cancelled || !r.Final || r.EventType != "eew_cancel" {
		t.Fatal(r, err)
	}
	r, err = Normalize(frame(t, "VXSE45", map[string]any{"earthquake": map[string]any{"condition": "仮定震源要素", "hypocenter": map[string]any{"name": "dummy", "depth": map[string]string{"value": "10"}}, "magnitude": map[string]string{"value": "1.0"}}}), now)
	if err != nil || r.Magnitude != nil || r.Epicenter != nil || !strings.Contains(r.Body, "仮定") {
		t.Fatal(r, err)
	}
	b := frame(t, "VXSE45", map[string]any{})
	var e Envelope
	_ = json.Unmarshal(b, &e)
	e.Head.Test = true
	b, _ = json.Marshal(e)
	if _, err = Normalize(b, now); !errors.Is(err, ErrIgnored) {
		t.Fatal("training accepted")
	}
	e.Head.Test = false
	e.Body = "broken"
	b, _ = json.Marshal(e)
	if _, err = Normalize(b, now); err == nil {
		t.Fatal("invalid base64 accepted")
	}
	if _, err = Normalize([]byte("{"), now); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	var z bytes.Buffer
	gz := gzip.NewWriter(&z)
	_, _ = gz.Write([]byte(strings.Repeat("a", MaxDecodedBytes+1)))
	_ = gz.Close()
	if _, err = DecodeBody(Envelope{Encoding: "base64", Compression: "gzip", Body: base64.StdEncoding.EncodeToString(z.Bytes())}); err == nil {
		t.Fatal("decompression limit")
	}
}

func TestNotificationKindsAndWarningAreaPreservation(t *testing.T) {
	now := time.Date(2026, 10, 5, 3, 0, 10, 0, time.UTC)
	for typ, title := range map[string]string{"VXSE51": "震度速報", "VXSE52": "震源情報", "VXSE53": "震源・震度情報"} {
		r, err := Normalize(frame(t, typ, map[string]any{"intensity": map[string]any{"maxInt": "5+"}}), now)
		if err != nil || r.Title != title || !strings.Contains(r.Body, "最大震度5強") {
			t.Fatal(r, err)
		}
	}
	areas := []map[string]string{}
	for i := 0; i < 15; i++ {
		areas = append(areas, map[string]string{"name": fmt.Sprintf("地域%d", i)})
	}
	r, err := Normalize(frame(t, "VXSE43", map[string]any{"prefectures": areas}), now)
	if err != nil || !strings.Contains(r.Body, "地域14") || !strings.HasPrefix(r.Title, "緊急地震速報（警報）") {
		t.Fatal("warning targets lost", r, err)
	}
}
