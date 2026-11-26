package dmdata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"quakerelay/server/internal/eew"
)

// ErrEEWFormat rejects frames the EEW bridge does not parse (DMDATA JSON conversions).
var ErrEEWFormat = errors.New("EEW bridge accepts raw XML telegrams only")

const eewFinalAdvisory = "この情報をもって、緊急地震速報：最終報とします。"

var eewSeries = map[string]string{"VXSE45": eew.SeriesForecast, "VXSE43": eew.SeriesWarning}

var eewInfoTypes = map[string]string{"発表": eew.InfoIssue, "訂正": eew.InfoCorrection, "取消": eew.InfoCancel}

var warningLevels = map[string]string{
	"緊急地震速報（地方予報区）": eew.LevelZone,
	"緊急地震速報（府県予報区）": eew.LevelPrefecture,
	"緊急地震速報（細分区域）":  eew.LevelRegion,
}

const defaultAreaCodeType = "地震情報／細分区域"

// ParseEEW extracts the EEW bridge representation of one raw VXSE45 or VXSE43
// frame with the transport limits and XML hardening of Normalize. Training,
// test and other products return ErrIgnored. Values are copied as published.
func ParseEEW(frame []byte) (eew.Telegram, error) {
	var t eew.Telegram
	if len(frame) > MaxMessageBytes {
		return t, errors.New("frame too large")
	}
	var e Envelope
	if err := json.Unmarshal(frame, &e); err != nil {
		return t, errors.New("invalid envelope JSON")
	}
	series := eewSeries[e.Head.Type]
	p := products[e.Head.Type]
	if series == "" || e.Type != "data" || e.Head.Test || e.Classification != p.classification {
		return t, ErrIgnored
	}
	if e.ID == "" || len(e.ID) > 128 {
		return t, errors.New("missing telegram identity")
	}
	if e.Format != "xml" {
		return t, ErrEEWFormat
	}
	body, err := DecodeBody(e)
	if err != nil {
		return t, err
	}
	root, err := readXML(body)
	if err != nil {
		return t, err
	}
	control, head, b := root.child("Control"), root.child("Head"), root.child("Body")
	if control == nil || head == nil || b == nil || control.get("Status") == "" {
		return t, errors.New("missing XML control, head or body")
	}
	if control.get("Status") != "通常" {
		return t, ErrIgnored
	}
	kind := xmlInfoKinds[e.Head.Type]
	if head.get("InfoKind") != kind.kind || head.get("InfoKindVersion") != kind.versions {
		return t, errors.New("unsupported XML information kind or version")
	}
	controlAt, err := time.Parse(time.RFC3339, control.get("DateTime"))
	if err != nil {
		return t, errors.New("invalid XML control time")
	}
	issuedAt, err := time.Parse(time.RFC3339, head.get("ReportDateTime"))
	if err != nil {
		return t, errors.New("invalid XML report time")
	}
	eventID := head.get("EventID")
	if eventID == "" || len(eventID) > 64 {
		return t, errors.New("missing EventID")
	}
	info, ok := eewInfoTypes[head.get("InfoType")]
	if !ok {
		return t, errors.New("unsupported EEW info type")
	}
	var serial *int
	if s := head.get("Serial"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 1000000 {
			return t, errors.New("invalid serial")
		}
		serial = &n
	}
	final := false
	if advisory := b.child("NextAdvisory"); advisory != nil && info != eew.InfoCancel {
		if series != eew.SeriesForecast || advisory.value() != eewFinalAdvisory {
			return t, errors.New("unknown XML EEW termination advisory")
		}
		final = true
	}
	sum := sha256.Sum256(body)
	t = eew.Telegram{Code: e.Head.Type, Series: series, TelegramID: e.ID, EventID: eventID, Serial: serial, InfoType: info,
		IssuedAt: issuedAt.UTC(), ControlAt: controlAt.UTC(), Final: final, ContentHash: hex.EncodeToString(sum[:])}
	if info == eew.InfoCancel {
		return t, nil
	}
	if series == eew.SeriesForecast {
		t.Forecast, err = forecastData(b)
	} else {
		t.Warning, err = warningReport(head)
	}
	return t, err
}

func xmlStamp(v string) *string {
	parsed, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil
	}
	s := parsed.UTC().Format(time.RFC3339)
	return &s
}

func forecastData(b *xmlNode) (*eew.ForecastData, error) {
	quakes := b.list("Earthquake")
	if len(quakes) != 1 {
		return nil, errors.New("EEW forecast needs one earthquake")
	}
	eq := quakes[0]
	h := eq.child("Hypocenter")
	a, ac := h.child("Area"), h.child("Accuracy")
	d := &eew.ForecastData{OriginTime: xmlStamp(eq.get("OriginTime"))}
	quality := eew.HypocenterQuality{Kind: "estimated", EpicenterRank: ac.child("Epicenter").attr("rank"),
		EpicenterRank2: ac.child("Epicenter").attr("rank2"), DepthRank: ac.child("Depth").attr("rank"),
		MagnitudeRank: ac.child("MagnitudeCalculation").attr("rank")}
	// NumberOfMagnitudeCalculation is a JMA code, not a count: 1 is one station,
	// the level method or an assumed hypocenter, 2–4 the number of stations and
	// 5 five or more. 0 is unknown (magnitudes from the NIED system carry it);
	// it and undefined codes are left out so that no receiver reads them as none.
	if n, err := strconv.Atoi(ac.get("NumberOfMagnitudeCalculation")); err == nil && n >= 1 && n <= 5 {
		quality.MagnitudePoints = &n
	}
	// Same qualification as the relay's notification text.
	switch {
	case eq.get("Condition") != "" || quality.MagnitudeRank == "8":
		quality.Kind = "assumed"
	case quality.EpicenterRank == "1":
		quality.Kind = "lowAccuracy"
	}
	hyp := &eew.Hypocenter{Code: a.get("Code"), Name: a.get("Name"), Quality: quality}
	for _, n := range a.list("Coordinate") {
		if n.attr("type") != "" {
			continue
		}
		m := coordinatePattern.FindStringSubmatch(n.value())
		if len(m) == 0 {
			continue
		}
		lat, e1 := strconv.ParseFloat(m[1], 64)
		lon, e2 := strconv.ParseFloat(m[2], 64)
		if e1 == nil && e2 == nil && math.Abs(lat) <= 90 && math.Abs(lon) <= 180 {
			hyp.Latitude, hyp.Longitude = &lat, &lon
		}
		if m[3] != "" {
			if altitude, err := strconv.ParseFloat(m[3], 64); err == nil && altitude <= 0 && altitude >= -1000000 && math.Mod(altitude, 1000) == 0 {
				km := int(-altitude / 1000)
				d.DepthKm = &km
			}
		}
		break
	}
	if hyp.Name != "" || hyp.Code != "" {
		d.Hypocenter = hyp
	}
	var selected *xmlNode
	magnitudes := eq.list("Magnitude")
	for _, m := range magnitudes {
		if m.attr("type") == "Mj" || len(magnitudes) == 1 {
			selected = m
			break
		}
	}
	if v, err := strconv.ParseFloat(selected.value(), 64); err == nil && !math.IsNaN(v) && v >= -1 && v <= 10 {
		d.Magnitude = &v
	}
	if quality.Kind == "assumed" {
		d.OriginTime, d.Magnitude, d.DepthKm = nil, nil, nil
	}
	forecast := b.child("Intensity").child("Forecast")
	d.MaxIntensity = eew.Intensity{Kind: "unknown"}
	if n := forecast.child("ForecastInt"); n != nil {
		d.MaxIntensity = eew.RangeOf(n.get("From"), n.get("To"), eew.IntensityCodes)
	}
	if n := forecast.child("ForecastLgInt"); n != nil {
		v := eew.RangeOf(n.get("From"), n.get("To"), eew.LongPeriodCodes)
		d.MaxLongPeriodClass = &v
	}
	codeType := defaultAreaCodeType
	for _, n := range forecast.child("CodeDefine").list("Type") {
		if n.attr("xpath") == "Pref/Area/Code" && n.value() != "" {
			codeType = n.value()
		}
	}
	d.RegionForecasts = []eew.RegionForecast{}
	for _, pref := range forecast.list("Pref") {
		for _, area := range pref.list("Area") {
			kind := area.child("Category").child("Kind")
			r := eew.RegionForecast{CodeType: codeType, Code: area.get("Code"), Name: area.get("Name"),
				PrefectureCode: pref.get("Code"), PrefectureName: pref.get("Name"), KindCode: kind.get("Code"), KindName: kind.get("Name"),
				MaxIntensity: eew.Intensity{Kind: "unknown"}, Arrival: arrival(area)}
			if r.Code == "" {
				return nil, errors.New("EEW forecast area without code")
			}
			r.IsWarning = strings.Contains(r.KindName, "警報")
			if n := area.child("ForecastInt"); n != nil {
				r.MaxIntensity = eew.RangeOf(n.get("From"), n.get("To"), eew.IntensityCodes)
			}
			if n := area.child("ForecastLgInt"); n != nil {
				v := eew.RangeOf(n.get("From"), n.get("To"), eew.LongPeriodCodes)
				r.MaxLongPeriodClass = &v
			}
			d.RegionForecasts = append(d.RegionForecasts, r)
		}
	}
	return d, nil
}

func arrival(area *xmlNode) eew.Arrival {
	condition := area.get("Condition")
	out := eew.Arrival{Time: xmlStamp(area.get("ArrivalTime")), Condition: "unknown", Text: condition}
	switch {
	case condition == "既に主要動到達と推測":
		out.Condition = "arrived"
	case strings.Contains(condition, "到達予想なし"):
		out.Condition = "noPrediction"
	case out.Time != nil:
		out.Condition = "predicted"
	}
	return out
}

// warningReport lists the regions of Head/Headline under warning (Kind 31) with
// the kind each had before this report (LastKind).
func warningReport(head *xmlNode) (*eew.WarningReport, error) {
	out := &eew.WarningReport{}
	seen := map[string]bool{}
	for _, information := range head.child("Headline").list("Information") {
		level := warningLevels[information.attr("type")]
		if level == "" {
			continue
		}
		for _, item := range information.list("Item") {
			if item.get("Kind", "Code") != "31" {
				continue
			}
			areas := item.child("Areas")
			last := item.get("LastKind", "Code")
			for _, a := range areas.list("Area") {
				r := eew.ReportRegion{Region: eew.Region{CodeType: areas.attr("codeType"), Code: a.get("Code"), Name: a.get("Name"), Level: level}, LastKind: last}
				if r.Code == "" || r.CodeType == "" {
					return nil, errors.New("EEW warning area without code")
				}
				if key := r.CodeType + "\x00" + r.Code; !seen[key] {
					seen[key] = true
					out.Regions = append(out.Regions, r)
				}
			}
		}
	}
	if len(out.Regions) == 0 {
		return nil, errors.New("EEW warning without target regions")
	}
	return out, nil
}
