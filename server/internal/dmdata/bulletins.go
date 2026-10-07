package dmdata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"quakerelay/server/internal/model"
	"strings"
	"time"
	"unicode/utf8"
)

func bulletinEventID(p product, kind, source, message string) string {
	if p.category == "earthquake" {
		return source
	}
	family := kind
	if strings.HasPrefix(kind, "VTSE") {
		family = "tsunami"
	}
	if strings.HasPrefix(kind, "VYSE5") {
		family = "nankai"
	}
	if source == "" {
		source = message
	}
	sum := sha256.Sum256([]byte(family + ":" + source))
	return p.category + "-" + hex.EncodeToString(sum[:20])
}

type object map[string]any

func obj(v any) object  { m, _ := v.(map[string]any); return m }
func str(v any) string  { s, _ := v.(string); return s }
func items(v any) []any { a, _ := v.([]any); return a }
func at(m object, path ...string) any {
	var v any = map[string]any(m)
	for _, key := range path {
		v = obj(v)[key]
	}
	return v
}
func join(parts ...string) string {
	out := []string{}
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, " / ")
}
func displayTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.In(time.FixedZone("JST", 9*60*60)).Format("01/02 15:04 JST")
}
func height(v any) string {
	h := obj(v)
	value := str(h["value"])
	if value != "" {
		value += str(h["unit"])
		if h["over"] == true {
			value += "以上"
		}
	}
	return join(value, str(h["condition"]))
}
func wave(v any) string {
	w := obj(v)
	return join(displayTime(str(w["arrivalTime"])), displayTime(str(w["dateTime"])),
		height(w["height"]), str(w["initial"]), str(w["condition"]), str(w["status"]), str(w["revise"]))
}
func doc(e Envelope, data []byte) *model.SourceDocument {
	sum := sha256.Sum256(data)
	return &model.SourceDocument{Format: e.Format, ByteCount: len(data), SHA256: hex.EncodeToString(sum[:]), Designation: e.Head.Designation, Complete: true}
}
func describeBulletin(e Envelope, data []byte, headline string) *model.Bulletin {
	var top object
	_ = json.Unmarshal(data, &top)
	b := obj(top["body"])
	out := &model.Bulletin{Headline: headline, Document: doc(e, data)}
	addText := func(title, text string) {
		if strings.TrimSpace(text) != "" {
			out.Sections = append(out.Sections, model.BulletinSection{Title: title, Text: text})
		}
	}
	addRows := func(title string, rows []model.BulletinRow) {
		if len(rows) > 0 {
			out.Sections = append(out.Sections, model.BulletinSection{Title: title, Rows: rows})
		}
	}
	row := func(rows *[]model.BulletinRow, label, value string) {
		if value != "" {
			*rows = append(*rows, model.BulletinRow{Label: label, Value: value})
		}
	}
	for _, v := range items(at(b, "tsunami", "forecasts")) {
		a := obj(v)
		rows := []model.BulletinRow{}
		row(&rows, "発表", str(at(a, "kind", "name")))
		row(&rows, "前回", str(at(a, "kind", "lastKind", "name")))
		row(&rows, "第1波の到達予想", wave(a["firstHeight"]))
		row(&rows, "予想される高さ", wave(a["maxHeight"]))
		for _, sv := range items(a["stations"]) {
			s := obj(sv)
			row(&rows, str(s["name"])+" 到達予想", wave(s["firstHeight"]))
			row(&rows, str(s["name"])+" 満潮", displayTime(str(s["highTideDateTime"])))
		}
		addRows("津波予報区："+str(a["name"]), rows)
	}
	for _, v := range items(at(b, "tsunami", "observations")) {
		a := obj(v)
		for _, sv := range items(a["stations"]) {
			s := obj(sv)
			rows := []model.BulletinRow{}
			row(&rows, "地域", str(a["name"]))
			row(&rows, "観測方法", str(s["sensor"]))
			row(&rows, "第1波", wave(s["firstHeight"]))
			row(&rows, "最大波", wave(s["maxHeight"]))
			addRows("津波観測："+str(s["name"]), rows)
		}
	}
	for _, v := range items(at(b, "tsunami", "estimations")) {
		a := obj(v)
		rows := []model.BulletinRow{}
		row(&rows, "第1波の推定", wave(a["firstHeight"]))
		row(&rows, "最大波の推定", wave(a["maxHeight"]))
		addRows("沿岸の推定："+str(a["name"]), rows)
	}

	for _, v := range items(b["earthquakes"]) {
		eq := obj(v)
		rows := []model.BulletinRow{}
		row(&rows, "発生時刻", displayTime(str(eq["originTime"])))
		row(&rows, "震源", str(at(eq, "hypocenter", "name")))
		row(&rows, "深さ", height(at(eq, "hypocenter", "depth")))
		row(&rows, "マグニチュード", str(at(eq, "magnitude", "value")))
		row(&rows, "補足", str(eq["condition"]))
		addRows("関連する地震", rows)
	}
	info := obj(b["earthquakeInfo"])
	addText("情報の種類", str(at(info, "kind", "name")))
	addText("発表内容", str(info["text"]))
	addText("補足", str(info["appendix"]))
	addText("本文", str(b["text"]))
	addText("次の発表", str(b["nextAdvisory"]))
	for _, v := range items(b["earthquakeCounts"]) {
		c := obj(v)
		rows := []model.BulletinRow{}
		row(&rows, "期間", join(displayTime(str(at(c, "targetTime", "start"))), displayTime(str(at(c, "targetTime", "end")))))
		row(&rows, "地震回数", str(at(c, "values", "all")))
		row(&rows, "有感地震回数", str(at(c, "values", "felt")))
		addRows("地震回数："+str(c["type"]), rows)
	}
	in := obj(b["intensity"])
	rows := []model.BulletinRow{}
	row(&rows, "最大長周期地震動階級", str(in["maxLgInt"]))
	row(&rows, "長周期地震動の区分", str(in["lgCategory"]))
	addRows("長周期地震動", rows)
	var areas func([]any, string)
	areas = func(values []any, parent string) {
		for _, v := range values {
			a := obj(v)
			name := str(a["name"])
			rows := []model.BulletinRow{}
			row(&rows, "震度", intensity(str(a["int"])))
			row(&rows, "最大震度", intensity(str(a["maxInt"])))
			row(&rows, "長周期地震動階級", str(a["lgInt"]))
			row(&rows, "最大長周期地震動階級", str(a["maxLgInt"]))
			row(&rows, "絶対速度応答", height(a["sva"]))
			addRows(join(parent, name), rows)
			for _, key := range []string{"regions", "cities", "stations"} {
				areas(items(a[key]), join(parent, name))
			}
		}
	}
	for _, key := range []string{"prefectures", "regions", "stations"} {
		areas(items(in[key]), "観測")
	}
	comments := obj(b["comments"])
	for _, i := range []struct{ key, title string }{{"warning", "防災上の留意事項"}, {"forecast", "地震・津波への留意事項"}, {"var", "関連情報"}} {
		addText(i.title, str(at(comments, i.key, "text")))
	}
	addText("付記", str(comments["free"]))
	return out
}
func bulletinSummary(b *model.Bulletin, fallback string) string {
	if strings.TrimSpace(b.Headline) != "" {
		return b.Headline
	}
	for _, s := range b.Sections {
		if s.Text != "" {
			return s.Text
		}
		if len(s.Rows) > 0 {
			return s.Title + " " + s.Rows[0].Label + "：" + s.Rows[0].Value
		}
	}
	return fallback
}
func tsunamiAlert(data []byte) bool {
	var top object
	_ = json.Unmarshal(data, &top)
	for _, v := range items(at(top, "body", "tsunami", "forecasts")) {
		code := str(at(obj(v), "kind", "code"))
		// JMA: 51/52/53 warnings; 62 advisory. 50/60 are withdrawals,
		// and 71/72/73 indicate slight sea-level changes, not warnings.
		if code == "51" || code == "52" || code == "53" || code == "62" {
			return true
		}
	}
	return false
}
func nankaiAlert(data []byte) bool {
	var top object
	_ = json.Unmarshal(data, &top)
	name := str(at(top, "body", "earthquakeInfo", "kind", "name"))
	return strings.Contains(name, "巨大地震警戒") || strings.Contains(name, "巨大地震注意")
}
func normalizeDocument(e Envelope, data, raw []byte, now time.Time, p product) (model.Report, error) {
	var r model.Report
	if !((e.Head.Type == "IXAC41" && e.Format == "binary") || (e.Head.Type == "WEPA60" && e.Format == "a/n")) {
		return r, ErrIgnored
	}
	if e.ID == "" || len(e.ID) > 128 || e.Head.Time.IsZero() || e.Head.Time.After(now.Add(30*time.Second)) || len(data) == 0 {
		return r, errors.New("invalid source document identity or time")
	}
	sum := sha256.Sum256([]byte(e.ID))
	r = model.Report{ID: hex.EncodeToString(sum[:]), MessageID: e.ID, Category: p.category, EventType: p.eventType,
		Classification: p.classification, TelegramType: e.Head.Type, Title: p.title,
		ReportedAt: e.Head.Time.UTC(), ReceivedAt: now.UTC(), Raw: append([]byte(nil), raw...)}
	r.EventID = bulletinEventID(p, e.Head.Type, e.Head.Author+":"+e.Head.Time.UTC().Format(time.RFC3339), e.ID)
	r.Bulletin = &model.Bulletin{Document: doc(e, data)}
	r.Body = "原電文を受信しました。配信試験が含まれる可能性があるため、プッシュ通知は行いません。"
	r.Bulletin.Sections = append(r.Bulletin.Sections, model.BulletinSection{Title: "受信資料", Text: r.Body})
	if e.Format == "a/n" {
		r.Bulletin.Sections = append(r.Bulletin.Sections, model.BulletinSection{Title: "原文", Text: alphanumeric(data)})
	} else {
		part := 0
		d := e.Head.Designation
		if strings.HasPrefix(d, "RR") && len(d) == 3 && d[2] >= 'A' && d[2] <= 'X' {
			part = int(d[2]-'A') + 1
		}
		r.Bulletin.Document.Part = &part
		complete := len(data) >= 12 && bytes.HasPrefix(data, []byte("BUFR")) && bytes.HasSuffix(data, []byte("7777"))
		if complete {
			complete = (int(data[4])<<16 | int(data[5])<<8 | int(data[6])) == len(data)
		}
		r.Bulletin.Document.Complete = complete
		note := "BUFR形式の推計震度分布データです。地図の描画には対応していません。原電文を保存できます。"
		if !complete {
			note = fmt.Sprintf("分割されたBUFRデータ（受信片 %d）です。単独では完全な資料ではありません。各受信片を履歴から保存できます。", part+1)
		}
		r.Bulletin.Sections = append(r.Bulletin.Sections, model.BulletinSection{Title: "数値データ", Text: note})
	}
	return r, nil
}
func alphanumeric(data []byte) string {
	if utf8.Valid(data) {
		return strings.TrimSpace(string(data))
	}
	var out strings.Builder
	for _, b := range data {
		switch {
		case b == '\n' || b == '\r' || b == '\t' || b >= 32 && b <= 126:
			out.WriteByte(b)
		case b >= 0xa1 && b <= 0xdf:
			out.WriteRune(rune(0xff61 + int(b) - 0xa1))
		default:
			out.WriteRune('\ufffd')
		}
	}
	return strings.TrimSpace(out.String())
}
