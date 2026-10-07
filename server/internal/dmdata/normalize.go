// Package dmdata consumes the official API v2 JSON conversion format.
package dmdata

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"quakerelay/server/internal/model"
)

const MaxMessageBytes = 2 << 20
const MaxDecodedBytes = 4 << 20

var ErrIgnored = errors.New("unsupported or non-operational telegram")

type Envelope struct {
	Type           string `json:"type"`
	ID             string `json:"id"`
	Classification string `json:"classification"`
	Head           struct {
		Type string `json:"type"`
		Test bool   `json:"test"`
	} `json:"head"`
	Format      string `json:"format"`
	Encoding    string `json:"encoding"`
	Compression string `json:"compression"`
	Body        string `json:"body"`
}
type scalar struct {
	Value     *string `json:"value"`
	Condition string  `json:"condition"`
}
type converted struct {
	Schema struct {
		Type    string `json:"type"`
		Version string `json:"version"`
	} `json:"_schema"`
	EventID    string          `json:"eventId"`
	Serial     json.RawMessage `json:"serialNo"`
	Status     string          `json:"status"`
	InfoType   string          `json:"infoType"`
	ReportedAt time.Time       `json:"reportDateTime"`
	Title      string          `json:"title"`
	Headline   string          `json:"headline"`
	Body       struct {
		Final       bool   `json:"isLastInfo"`
		Cancelled   bool   `json:"isCanceled"`
		Warning     bool   `json:"isWarning"`
		Text        string `json:"text"`
		Prefectures []struct {
			Name string `json:"name"`
		} `json:"prefectures"`
		Earthquake *struct {
			OriginTime *string `json:"originTime"`
			Condition  string  `json:"condition"`
			Hypocenter struct {
				Name       string `json:"name"`
				Depth      scalar `json:"depth"`
				Coordinate struct {
					Latitude  scalar `json:"latitude"`
					Longitude scalar `json:"longitude"`
				} `json:"coordinate"`
				Accuracy struct {
					Epicenters           []string `json:"epicenters"`
					MagnitudeCalculation string   `json:"magnitudeCalculation"`
				} `json:"accuracy"`
			} `json:"hypocenter"`
			Magnitude scalar `json:"magnitude"`
		} `json:"earthquake"`
		Intensity *struct {
			Max      string `json:"maxInt"`
			Forecast struct {
				From string `json:"from"`
				To   string `json:"to"`
			} `json:"forecastMaxInt"`
		} `json:"intensity"`
	} `json:"body"`
}

func limited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxDecodedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxDecodedBytes {
		return nil, errors.New("decoded telegram too large")
	}
	return b, nil
}
func DecodeBody(e Envelope) ([]byte, error) {
	b := []byte(e.Body)
	switch e.Encoding {
	case "base64":
		var err error
		b, err = base64.StdEncoding.DecodeString(e.Body)
		if err != nil {
			return nil, errors.New("invalid base64")
		}
	case "utf-8", "":
	default:
		return nil, errors.New("unsupported encoding")
	}
	switch e.Compression {
	case "":
		if len(b) > MaxDecodedBytes {
			return nil, errors.New("telegram too large")
		}
		return b, nil
	case "gzip":
		r, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, errors.New("invalid gzip")
		}
		defer r.Close()
		return limited(r)
	case "zip":
		r, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil || len(r.File) != 1 {
			return nil, errors.New("zip must contain one file")
		}
		f, err := r.File[0].Open()
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return limited(f)
	default:
		return nil, errors.New("unsupported compression")
	}
}
func number(v *string) *float64 {
	if v == nil {
		return nil
	}
	n, err := strconv.ParseFloat(*v, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil
	}
	return &n
}
func text(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func intensity(v string) string {
	names := map[string]string{"5-": "5弱", "5+": "5強", "6-": "6弱", "6+": "6強", "!5-": "5弱以上未入電"}
	if name, ok := names[v]; ok {
		return name
	}
	return v
}
func Normalize(raw []byte, now time.Time) (model.Report, error) {
	var r model.Report
	if len(raw) > MaxMessageBytes {
		return r, errors.New("frame too large")
	}
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return r, errors.New("invalid envelope JSON")
	}
	expected := ""
	switch e.Head.Type {
	case "VXSE45":
		expected = "eew.forecast"
	case "VXSE43":
		expected = "eew.warning"
	case "VXSE51", "VXSE52", "VXSE53":
		expected = "telegram.earthquake"
	default:
		return r, ErrIgnored
	}
	if e.Type != "data" || e.Head.Test || e.Classification != expected {
		return r, ErrIgnored
	}
	if e.Format != "json" {
		return r, errors.New("expected formatMode=json")
	}
	body, err := DecodeBody(e)
	if err != nil {
		return r, err
	}
	var c converted
	if err = json.Unmarshal(body, &c); err != nil {
		return r, errors.New("invalid converted JSON")
	}
	schema := "earthquake-information"
	if strings.HasPrefix(expected, "eew.") {
		schema = "eew-information"
	}
	if c.Schema.Type != schema || !strings.HasPrefix(c.Schema.Version, "1.") {
		return r, errors.New("unsupported schema")
	}
	if c.Status != "通常" {
		return r, ErrIgnored
	}
	if c.EventID == "" || len(c.EventID) > 64 || strings.Trim(c.EventID, "0123456789") != "" || c.ReportedAt.IsZero() || e.ID == "" || len(e.ID) > 128 {
		return r, errors.New("missing telegram identity or time")
	}
	if c.ReportedAt.After(now.Add(30 * time.Second)) {
		return r, errors.New("telegram time is in the future; check clock")
	}
	switch c.InfoType {
	case "発表", "訂正", "遅延", "取消":
	default:
		return r, errors.New("invalid infoType")
	}
	if len(c.Serial) > 0 && string(c.Serial) != "null" {
		var serialText string
		if err = json.Unmarshal(c.Serial, &serialText); err != nil {
			serialText = string(c.Serial)
		}
		if serialText != "" {
			n, err := strconv.Atoi(serialText)
			if err != nil || n < 0 || n > 1000000 {
				return r, errors.New("invalid serial")
			}
			r.Serial = &n
		}
	}
	if schema == "eew-information" && r.Serial == nil {
		return r, errors.New("EEW serial is required")
	}
	digest := sha256.Sum256([]byte(e.ID))
	r.ID = hex.EncodeToString(digest[:])
	r.EventID = c.EventID
	r.MessageID = e.ID
	r.Classification = expected
	r.TelegramType = e.Head.Type
	r.ReportedAt = c.ReportedAt.UTC()
	r.ReceivedAt = now.UTC()
	r.Raw = append([]byte(nil), raw...)
	r.Cancelled = c.Body.Cancelled || c.InfoType == "取消"
	r.Final = c.Body.Final || r.Cancelled
	r.Warning = c.Body.Warning || expected == "eew.warning"
	r.EventType = "earthquake_info"
	r.Title = map[string]string{"VXSE51": "震度速報", "VXSE52": "震源情報", "VXSE53": "震源・震度情報"}[e.Head.Type]
	if expected == "eew.forecast" {
		r.EventType = "eew_forecast"
		r.Title = "緊急地震速報（予報）"
	}
	if expected == "eew.warning" {
		r.EventType = "eew_warning"
		r.Title = "緊急地震速報（警報）"
	}
	if r.Cancelled {
		if r.IsEEW() {
			r.EventType = "eew_cancel"
			r.Title = "緊急地震速報（取消）"
		} else {
			r.Title = "地震情報（取消）"
		}
		r.Body = "この情報は取り消されました。"
		return r, nil
	}
	if r.IsEEW() && r.Serial != nil {
		r.Title += fmt.Sprintf(" 第%d報", *r.Serial)
	}
	if r.Final {
		r.Title += "（最終）"
	}
	if eq := c.Body.Earthquake; eq != nil {
		h := &model.Hypocenter{Status: "estimated", OriginTime: eq.OriginTime,
			Epicenter: text(eq.Hypocenter.Name), Latitude: number(eq.Hypocenter.Coordinate.Latitude.Value),
			Longitude: number(eq.Hypocenter.Coordinate.Longitude.Value), Magnitude: number(eq.Magnitude.Value),
			DepthCondition: eq.Hypocenter.Depth.Condition}
		if d := number(eq.Hypocenter.Depth.Value); d != nil && *d >= 0 && *d <= 1000 && math.Trunc(*d) == *d {
			n := int(*d)
			h.DepthKM = &n
		}
		if h.Latitude != nil && (*h.Latitude < -90 || *h.Latitude > 90) {
			h.Latitude = nil
		}
		if h.Longitude != nil && (*h.Longitude < -180 || *h.Longitude > 180) {
			h.Longitude = nil
		}
		if r.IsEEW() {
			switch {
			case eq.Condition != "":
				h.Status = "assumed"
				h.Note = "仮定震源の参考値です。実際の震源要素を示す値ではありません。"
			case eq.Hypocenter.Accuracy.MagnitudeCalculation == "8":
				h.Status = "assumed"
				h.Note = "レベル法等による仮定震源です。震央は観測点の位置、深さなどは仮の値です。"
			case c.Body.Intensity == nil || len(eq.Hypocenter.Accuracy.Epicenters) > 0 && eq.Hypocenter.Accuracy.Epicenters[0] == "1":
				h.Status = "low_accuracy"
				h.Note = "1点観測などによる精度の低い推定です。続報で大きく変わる場合があります。"
			}
		}
		if expected == "eew.warning" {
			// DMDATA's warning restriction applies to every public representation.
			h.Magnitude, h.DepthKM, h.DepthCondition = nil, nil, ""
		}
		r.Hypocenter = h
		if h.Status != "assumed" {
			r.OriginTime, r.Epicenter = h.OriginTime, h.Epicenter
			r.Latitude, r.Longitude = h.Latitude, h.Longitude
			r.Magnitude, r.DepthKM = h.Magnitude, h.DepthKM
		}
	}
	if c.Body.Intensity != nil {
		v := intensity(c.Body.Intensity.Max)
		if r.IsEEW() {
			from, to := c.Body.Intensity.Forecast.From, c.Body.Intensity.Forecast.To
			switch {
			case to == "over" && (from == "" || from == "不明"):
				v = "不明"
			case to == "over":
				v = intensity(from) + "以上"
			case to == "":
				v = intensity(from)
			case from != "" && from != to:
				v = intensity(from) + "〜" + intensity(to)
			default:
				v = intensity(to)
			}
		}
		r.MaxIntensity = text(v)
	}
	parts := []string{}
	if expected == "eew.warning" {
		parts = append(parts, "強い揺れに警戒してください。")
	}
	if h := r.Hypocenter; h != nil {
		// Put qualifications before values/regions, so notification truncation cannot remove them.
		if h.Note != "" {
			parts = append(parts, h.Note)
		}
		if h.Epicenter != nil {
			label := "震源："
			if h.Status == "assumed" {
				label = "震源（仮定値）："
			}
			parts = append(parts, label+*h.Epicenter+"。")
		}
	}
	if r.MaxIntensity != nil {
		label := "最大震度"
		if r.IsEEW() {
			label = "予想最大震度"
		}
		parts = append(parts, label+*r.MaxIntensity+"。")
	}
	if h := r.Hypocenter; h != nil {
		suffix := ""
		if h.Status == "assumed" {
			suffix = "（仮定値）"
		}
		if h.Magnitude != nil {
			parts = append(parts, fmt.Sprintf("M%.1f%s。", *h.Magnitude, suffix))
		}
		if h.DepthKM != nil {
			depth := fmt.Sprintf("%dkm", *h.DepthKM)
			switch h.DepthCondition {
			case "ごく浅い":
				depth = "ごく浅い（数値0km）"
			case "７００ｋｍ以上":
				depth = "700km以上"
			}
			parts = append(parts, "深さ"+depth+suffix+"。")
		}
	}
	if expected == "eew.warning" {
		areas := []string{}
		for _, a := range c.Body.Prefectures {
			if a.Name != "" {
				areas = append(areas, a.Name)
			}
		}
		if len(areas) > 0 {
			parts = append(parts, "対象地域："+strings.Join(areas, "、"))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "地震情報が発表されました。")
	}
	r.Body = strings.Join(parts, " ")
	return r, nil
}
