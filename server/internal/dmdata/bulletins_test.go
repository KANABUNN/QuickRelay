package dmdata

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var bulletinTime = time.Date(2026, 10, 5, 3, 0, 10, 0, time.UTC)

func bulletinFrame(t *testing.T, code string, change func(map[string]any)) []byte {
	t.Helper()
	p := products[code]
	c := map[string]any{"_schema": map[string]string{"type": p.schema, "version": "1.0.0"},
		"eventId": "20261005120000", "serialNo": nil, "status": "通常", "infoType": "発表",
		"reportDateTime": "2026-10-05T03:00:05Z", "pressDateTime": "2026-10-05T03:00:06Z",
		"title": p.title, "body": map[string]any{"text": "合成の試験資料。実際の防災情報ではありません。"}}
	if p.classification != "telegram.earthquake" {
		c["serialNo"] = "1"
	}
	if change != nil {
		change(c)
	}
	b, _ := json.Marshal(c)
	e := Envelope{Type: "data", ID: "sample-" + code, Classification: p.classification, Format: "json", Encoding: "utf-8", Body: string(b)}
	e.Head.Type = code
	b, _ = json.Marshal(e)
	return b
}
func TestAllContractProductsAndNonOperationalExclusion(t *testing.T) {
	codes := strings.Fields("VXSE44 VXSE45 VXSE43 VXSE51 VXSE52 VXSE53 VXSE56 VXSE60 VXSE61 VXSE62 VYSE50 VYSE51 VYSE52 VYSE60 VTSE41 VTSE51 VTSE52 VZSE40")
	if len(products) != 20 {
		t.Fatal("review product inventory")
	}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			raw := bulletinFrame(t, code, nil)
			r, err := Normalize(raw, bulletinTime)
			if err != nil || r.EventType == "" || r.CategoryName() != products[code].category {
				t.Fatal(code, err)
			}
			if !r.IsEEW() && (r.Final || strings.Contains(r.Title, "第")) {
				t.Fatal("ordinary bulletin given EEW numbering")
			}
			for _, status := range []string{"訓練", "試験"} {
				_, err = Normalize(bulletinFrame(t, code, func(c map[string]any) { c["status"] = status }), bulletinTime)
				if !errors.Is(err, ErrIgnored) {
					t.Fatal("non-operational accepted", status)
				}
			}
		})
	}
	raw := bulletinFrame(t, "VXSE45", nil)
	var e Envelope
	_ = json.Unmarshal(raw, &e)
	e.Head.Type = "VXSE42"
	raw, _ = json.Marshal(e)
	if _, err := Normalize(raw, bulletinTime); !errors.Is(err, ErrIgnored) {
		t.Fatal(err)
	}
}
func TestTsunamiQualifiersGroupingReleaseCorrectionAndCancellation(t *testing.T) {
	makeFrame := func(kind, info string) []byte {
		return bulletinFrame(t, "VTSE41", func(c map[string]any) {
			c["eventId"] = "20261005000000 20261005000001"
			c["serialNo"] = "9"
			c["infoType"] = info
			c["body"] = map[string]any{"tsunami": map[string]any{"forecasts": []any{
				map[string]any{"name": "合成沿岸", "kind": map[string]any{"code": kind, "name": "合成区分"},
					"firstHeight": map[string]any{"condition": "津波到達中と推測"},
					"maxHeight":   map[string]any{"height": map[string]any{"value": nil, "unit": "m", "condition": "巨大"}}}}}}
		})
	}
	r, err := Normalize(makeFrame("62", "発表"), bulletinTime)
	if err != nil || r.EventType != "tsunami_warning" || !r.Warning || !strings.HasPrefix(r.EventID, "tsunami-") {
		t.Fatal(r, err)
	}
	data, _ := json.Marshal(r.Bulletin)
	if !strings.Contains(string(data), "巨大") || strings.Contains(string(data), "0m") || !strings.Contains(string(data), "津波到達中") {
		t.Fatal(string(data))
	}
	release, err := Normalize(makeFrame("00", "発表"), bulletinTime)
	if err != nil || release.Cancelled || release.Warning || release.EventType != "tsunami_info" || release.EventID != r.EventID {
		t.Fatal(release, err)
	}
	correction, _ := Normalize(makeFrame("62", "訂正"), bulletinTime)
	if !correction.NewerThan(r) || r.NewerThan(correction) || !strings.Contains(correction.Title, "訂正") {
		t.Fatal("correction ordering")
	}
	cancel, _ := Normalize(makeFrame("62", "取消"), bulletinTime)
	if !cancel.Cancelled || cancel.Final || cancel.Warning || !strings.Contains(cancel.Body, "取り消") {
		t.Fatal("cancel is not an all-clear")
	}
	later := release
	later.ReportedAt = cancel.ReportedAt.Add(time.Minute)
	if !later.NewerThan(cancel) || cancel.NewerThan(later) {
		t.Fatal("cancel suppressed later bulletin")
	}
	observed, err := Normalize(bulletinFrame(t, "VTSE51", func(c map[string]any) {
		c["eventId"] = "20261005000000 20261005000001"
		c["body"] = map[string]any{"tsunami": map[string]any{"observations": []any{map[string]any{"name": "合成沿岸", "stations": []any{
			map[string]any{"name": "合成港", "firstHeight": map[string]any{"status": "欠測"}, "maxHeight": map[string]any{"height": map[string]any{"value": "1.2", "unit": "m", "over": true, "condition": "上昇中"}}}}}}}}
	}), bulletinTime)
	data, _ = json.Marshal(observed.Bulletin)
	if err != nil || observed.EventID != r.EventID || !strings.Contains(string(data), "1.2m以上 / 上昇中") || !strings.Contains(string(data), "欠測") {
		t.Fatal(string(data), err)
	}
}
func TestNankaiNullIdentityTextAndLongPeriod(t *testing.T) {
	r, err := Normalize(bulletinFrame(t, "VYSE50", func(c map[string]any) {
		c["eventId"] = nil
		c["body"] = map[string]any{"earthquakeInfo": map[string]any{"kind": map[string]any{"name": "南海トラフ地震臨時情報（巨大地震注意）"},
			"text": "合成本文", "appendix": "合成補足"}, "nextAdvisory": "次の発表"}
	}), bulletinTime)
	b, _ := json.Marshal(r.Bulletin)
	if err != nil || r.Serial != nil || !r.Warning || !r.Attention() || !strings.Contains(string(b), "合成補足") || !strings.Contains(string(b), "次の発表") {
		t.Fatal(err, string(b))
	}
	lp, err := Normalize(bulletinFrame(t, "VXSE62", func(c map[string]any) {
		c["body"] = map[string]any{"intensity": map[string]any{"maxInt": "5-", "maxLgInt": "3", "stations": []any{map[string]any{"name": "合成地点", "lgInt": "3"}}}}
	}), bulletinTime)
	b, _ = json.Marshal(lp.Bulletin)
	if err != nil || lp.MaxIntensity == nil || *lp.MaxIntensity != "5弱" || !strings.Contains(string(b), "最大長周期地震動階級") {
		t.Fatal(err, string(b))
	}
}
func rawFrame(code, format, designation string, data []byte) []byte {
	e := Envelope{Type: "data", ID: code + "-" + designation, Classification: "telegram.earthquake", Format: format, Encoding: "base64", Body: base64.StdEncoding.EncodeToString(data)}
	e.Head.Type = code
	e.Head.Time = bulletinTime.Add(-time.Second)
	e.Head.Author = "合成"
	e.Head.Designation = designation
	b, _ := json.Marshal(e)
	return b
}
func TestRawDocumentsAndOutOfOrderFragments(t *testing.T) {
	// BUFR length 12, edition 4, end marker; no real source data.
	whole := []byte{'B', 'U', 'F', 'R', 0, 0, 12, 4, '7', '7', '7', '7'}
	r, err := Normalize(rawFrame("IXAC41", "binary", "", whole), bulletinTime)
	if err != nil || !r.Bulletin.Document.Complete || r.PushEligible() {
		t.Fatal(r, err)
	}
	last, err := Normalize(rawFrame("IXAC41", "binary", "RRA", whole[8:]), bulletinTime)
	first, err2 := Normalize(rawFrame("IXAC41", "binary", "", whole[:8]), bulletinTime)
	if err != nil || err2 != nil || last.Bulletin.Document.Complete || first.Bulletin.Document.Complete || last.EventID != first.EventID ||
		!last.NewerThan(first) || first.NewerThan(last) {
		t.Fatal("fragment identity or ordering", err, err2)
	}
	text, err := Normalize(rawFrame("WEPA60", "a/n", "", []byte{'T', 'E', 'S', 'T', ' ', 0xb6, 0xc5}), bulletinTime)
	if err != nil || text.PushEligible() || !strings.Contains(text.Bulletin.Sections[1].Text, "ｶﾅ") {
		t.Fatal("raw text", err)
	}
	var e Envelope
	_ = json.Unmarshal(rawFrame("WEPA60", "a/n", "", []byte("TEST")), &e)
	e.Head.Test = true
	b, _ := json.Marshal(e)
	if _, err = Normalize(b, bulletinTime); !errors.Is(err, ErrIgnored) {
		t.Fatal("explicit test accepted")
	}
}

func TestTsunamiCodeTableDistinguishesAllReleaseAndForecastCodes(t *testing.T) {
	for _, code := range []string{"00", "50", "51", "52", "53", "60", "62", "71", "72", "73"} {
		expected := code == "51" || code == "52" || code == "53" || code == "62"
		raw := bulletinFrame(t, "VTSE41", func(c map[string]any) {
			c["body"] = map[string]any{"tsunami": map[string]any{"forecasts": []any{map[string]any{"kind": map[string]any{"code": code}}}}}
		})
		r, err := Normalize(raw, bulletinTime)
		if err != nil || r.Warning != expected || r.Cancelled || r.Attention() != expected {
			t.Fatal(code, err)
		}
	}
}
