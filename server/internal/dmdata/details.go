package dmdata

import (
	"math"
	"strconv"
	"strings"

	"quakerelay/server/internal/model"
)

// Structured fields built from the adapter document (XML) or converted JSON.
// Both transports pass through these functions, so their output stays identical.

// rangeText is the display form of a forecast range, as used in summaries.
func rangeText(from, to string) string {
	switch {
	case to == "over" && (from == "" || from == "不明"):
		return "不明"
	case to == "over":
		return intensity(from) + "以上"
	case to == "":
		return intensity(from)
	case from != "" && from != to:
		return intensity(from) + "〜" + intensity(to)
	default:
		return intensity(to)
	}
}

func intensityRange(v any) *model.IntensityRange {
	f := obj(v)
	from, to := str(f["from"]), str(f["to"])
	if from == "" && to == "" {
		return nil
	}
	display := func(s string) string {
		if s == "over" {
			return s
		}
		return intensity(s)
	}
	return &model.IntensityRange{From: display(from), To: display(to), Text: rangeText(from, to)}
}

// JMA EEW Forecast/Appendix codes (緊急地震速報 XML解説資料).
var (
	maxIntChangeText = map[string]string{"1": "最大予想震度が1.0以上大きくなりました", "2": "最大予想震度が1.0以上小さくなりました"}
	maxLgChangeText  = map[string]string{"1": "最大予想長周期地震動階級が1以上大きくなりました", "2": "最大予想長周期地震動階級が1以上小さくなりました"}
	changeReasonText = map[string]string{"1": "主としてMが変化したため", "2": "主として震央の位置が変化したため",
		"3": "Mと震央の位置が変化したため", "4": "震源の深さが変化したため", "9": "PLUM法による予想により変化したため"}
)

func eewChange(v any) *model.EEWChange {
	a := obj(v)
	c := &model.EEWChange{MaxIntensity: str(a["maxIntChange"]), MaxLongPeriodClass: str(a["maxLgIntChange"]), Reason: str(a["maxIntChangeReason"])}
	if c.MaxIntensity == "" && c.MaxLongPeriodClass == "" && c.Reason == "" {
		return nil
	}
	describe := func(table map[string]string, code string) string {
		if code == "" || code == "0" {
			return ""
		}
		if s, ok := table[code]; ok {
			return s
		}
		return "変化あり（コード" + code + "）"
	}
	parts := []string{}
	for _, s := range []string{describe(maxIntChangeText, c.MaxIntensity), describe(maxLgChangeText, c.MaxLongPeriodClass)} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) > 0 {
		c.Description = strings.Join(parts, "。") + "。"
		if reason := describe(changeReasonText, c.Reason); reason != "" {
			c.Description += "理由：" + reason + "。"
		}
	}
	return c
}

func eewDetail(top object) *model.EEWDetail {
	b := obj(top["body"])
	in := obj(b["intensity"])
	d := &model.EEWDetail{Headline: strings.TrimSpace(str(top["headline"])),
		ForecastMaxIntensity: intensityRange(in["forecastMaxInt"]), ForecastMaxLongPeriod: intensityRange(in["forecastMaxLgInt"]),
		Change: eewChange(in["appendix"])}
	for _, level := range []struct{ key, name string }{{"zones", "zone"}, {"prefectures", "prefecture"}, {"regions", "region"}} {
		for _, v := range items(b[level.key]) {
			a := obj(v)
			if name := str(a["name"]); name != "" {
				d.WarningAreas = append(d.WarningAreas, model.EEWZone{Level: level.name, Name: name, Code: str(a["code"])})
			}
		}
	}
	add := func(a, pref object) {
		name := str(a["name"])
		if name == "" {
			return
		}
		kind := obj(a["kind"])
		area := model.EEWArea{Prefecture: str(pref["name"]), PrefectureCode: str(pref["code"]), Name: name, Code: str(a["code"]),
			Kind: str(kind["name"]), KindCode: str(kind["code"]),
			ForecastIntensity: intensityRange(a["forecastMaxInt"]), ForecastLongPeriodClass: intensityRange(a["forecastMaxLgInt"]),
			ArrivalTime: text(str(a["arrivalTime"])), Condition: str(a["condition"])}
		area.Warning = a["isWarning"] == true || strings.Contains(area.Kind, "警報")
		d.Areas = append(d.Areas, area)
	}
	// XML: Pref/Area. DMDATA converted JSON lists forecast areas as intensity.regions.
	for _, v := range items(in["prefectures"]) {
		p := obj(v)
		for _, a := range items(p["regions"]) {
			add(obj(a), p)
		}
	}
	for _, a := range items(in["regions"]) {
		add(obj(a), nil)
	}
	comments := obj(b["comments"])
	for _, s := range []string{str(at(comments, "warning", "text")), str(at(comments, "forecast", "text")),
		str(at(comments, "var", "text")), str(comments["free"])} {
		if s = strings.TrimSpace(s); s != "" {
			d.Comments = append(d.Comments, s)
		}
	}
	if d.Headline == "" && d.ForecastMaxIntensity == nil && d.ForecastMaxLongPeriod == nil && d.Change == nil &&
		len(d.WarningAreas) == 0 && len(d.Areas) == 0 && len(d.Comments) == 0 {
		return nil
	}
	return d
}

// extendHypocenter adds source details. DMDATA's display condition forbids
// magnitude and depth for warnings, so their texts and ranks are omitted there
// (the coordinate text also states the depth).
func extendHypocenter(h *model.Hypocenter, eq object, warning bool) {
	hy := obj(eq["hypocenter"])
	h.Code = str(hy["code"])
	h.DetailedName = str(at(hy, "detailed", "name"))
	h.NameFromMark = str(hy["nameFromMark"])
	h.Source = str(hy["source"])
	h.LandOrSea = str(hy["landOrSea"])
	h.ArrivalTime = text(str(eq["arrivalTime"]))
	acc := obj(hy["accuracy"])
	a := model.HypocenterAccuracy{}
	if ranks := items(acc["epicenters"]); len(ranks) > 0 {
		a.Epicenter = str(ranks[0])
		if len(ranks) > 1 {
			a.EpicenterSecondary = str(ranks[1])
		}
	}
	if !warning {
		mag := obj(eq["magnitude"])
		h.CoordinateText = str(at(hy, "coordinate", "description"))
		h.MagnitudeType, h.MagnitudeCondition, h.MagnitudeText = str(mag["type"]), str(mag["condition"]), str(mag["description"])
		a.Depth, a.Magnitude = str(acc["depth"]), str(acc["magnitudeCalculation"])
		// NumberOfMagnitudeCalculation is a JMA code, not a count: 1 is one station,
		// the level method or an assumed hypocenter, 2–4 the number of stations and
		// 5 five or more. 0 is unknown (magnitudes from the NIED system carry it);
		// it and undefined codes are left out so that no client reads them as none.
		if n, err := strconv.Atoi(str(acc["numberOfMagnitudeCalculation"])); err == nil && n >= 1 && n <= 5 {
			a.MagnitudePoints = &n
		}
	}
	if a != (model.HypocenterAccuracy{}) {
		h.Accuracy = &a
	}
}

// relatedEarthquakes lists the earthquakes attached to tsunami and advisory bulletins.
func relatedEarthquakes(top object) []model.Hypocenter {
	var out []model.Hypocenter
	for _, v := range items(at(top, "body", "earthquakes")) {
		eq := obj(v)
		hy := obj(eq["hypocenter"])
		value := func(v any) *float64 {
			s := str(obj(v)["value"])
			if s == "" {
				return nil
			}
			return number(&s)
		}
		h := model.Hypocenter{Status: "estimated", OriginTime: text(str(eq["originTime"])), Epicenter: text(str(hy["name"])),
			Latitude: value(at(hy, "coordinate", "latitude")), Longitude: value(at(hy, "coordinate", "longitude")),
			Magnitude: value(eq["magnitude"]), DepthCondition: str(at(hy, "depth", "condition"))}
		if d := value(hy["depth"]); d != nil && *d >= 0 && *d <= 1000 && math.Trunc(*d) == *d {
			n := int(*d)
			h.DepthKM = &n
		}
		if h.Latitude != nil && math.Abs(*h.Latitude) > 90 || h.Longitude != nil && math.Abs(*h.Longitude) > 180 {
			h.Latitude, h.Longitude = nil, nil
		}
		if c := str(eq["condition"]); c != "" {
			h.Note = c
		}
		extendHypocenter(&h, eq, false)
		out = append(out, h)
	}
	return out
}

func intensityObservation(top object) *model.IntensityObservation {
	in := obj(at(top, "body", "intensity"))
	if len(in) == 0 {
		return nil
	}
	o := &model.IntensityObservation{MaxIntensity: intensity(str(in["maxInt"])), MaxLongPeriodClass: str(in["maxLgInt"]),
		LongPeriodCategory: str(in["lgCategory"])}
	levels := []struct{ key, level string }{{"prefectures", "prefecture"}, {"regions", "region"}, {"cities", "city"},
		{"stations", "station"}, {"intensityStations", "station"}}
	var walk func(object) []model.ObservedArea
	walk = func(parent object) []model.ObservedArea {
		var out []model.ObservedArea
		for _, l := range levels {
			for _, v := range items(parent[l.key]) {
				if n, ok := observedArea(obj(v), l.level); ok {
					n.Children = walk(obj(v))
					out = append(out, n)
				}
			}
		}
		return out
	}
	o.Areas = walk(in)
	if o.MaxIntensity == "" && o.MaxLongPeriodClass == "" && o.LongPeriodCategory == "" && len(o.Areas) == 0 {
		return nil
	}
	return o
}

func observedArea(a object, level string) (model.ObservedArea, bool) {
	name := str(a["name"])
	if name == "" {
		return model.ObservedArea{}, false
	}
	value := str(a["maxInt"])
	if value == "" {
		value = str(a["int"])
	}
	lg := str(a["maxLgInt"])
	if lg == "" {
		lg = str(a["lgInt"])
	}
	n := model.ObservedArea{Level: level, Name: name, Code: str(a["code"]), Intensity: intensity(value), LongPeriodClass: lg,
		Condition: str(a["condition"]), Revise: str(a["revise"])}
	if s := obj(a["sva"]); len(s) > 0 {
		n.SVA = measurement(s)
	}
	index := map[string]int{}
	for _, v := range items(a["perPeriod"]) {
		p := obj(v)
		band := str(p["band"])
		if band == "" {
			continue
		}
		i, ok := index[band]
		if !ok {
			i = len(n.Periods)
			index[band] = i
			n.Periods = append(n.Periods, model.LongPeriodBand{Band: band, PeriodUnit: str(p["periodUnit"])})
		}
		if lg := str(p["lgInt"]); lg != "" {
			n.Periods[i].LongPeriodClass = lg
		}
		if s := obj(p["sva"]); len(s) > 0 {
			m := measurement(s)
			n.Periods[i].SVA, n.Periods[i].SVAUnit = m.Value, m.Unit
		}
	}
	return n, true
}

func measurement(m object) *model.Measurement {
	out := &model.Measurement{Unit: str(m["unit"]), Condition: str(m["condition"])}
	if s := str(m["value"]); s != "" {
		out.Value = number(&s)
	}
	return out
}
