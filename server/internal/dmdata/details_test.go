package dmdata

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"quakerelay/server/internal/model"
)

func normalizeFixture(t *testing.T, code string, data []byte) model.Report {
	t.Helper()
	r, err := Normalize(transport(t, code, "xml", operational(data)), xmlNow)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEEWWarningDetailsKeepDisplayRestriction(t *testing.T) {
	r := normalizeFixture(t, "VXSE43", fixture(t, "37_01_01_240613_VXSE43.xml"))
	e := r.EEW
	if e == nil || e.Headline != "豊後水道で地震　四国　九州　中国で強い揺れ" || len(e.Areas) != 9 || len(e.WarningAreas) != 17 {
		t.Fatalf("warning details: %+v", e)
	}
	if e.ForecastMaxIntensity == nil || e.ForecastMaxIntensity.Text != "5弱" || e.ForecastMaxLongPeriod == nil || e.ForecastMaxLongPeriod.Text != "1" {
		t.Fatalf("forecast maxima: %+v %+v", e.ForecastMaxIntensity, e.ForecastMaxLongPeriod)
	}
	if e.WarningAreas[0] != (model.EEWZone{Level: "zone", Name: "四国", Code: "9943"}) || e.WarningAreas[3].Level != "prefecture" || e.WarningAreas[16].Level != "region" {
		t.Fatalf("warning areas: %+v", e.WarningAreas)
	}
	first, second := e.Areas[0], e.Areas[1]
	if first.Name != "愛媛県南予" || first.Code != "622" || first.Prefecture != "愛媛" || first.KindCode != "11" || !first.Warning ||
		first.Condition != "既に主要動到達と推測" || first.ArrivalTime != nil || first.ForecastIntensity.Text != "5弱" || first.ForecastLongPeriodClass.Text != "0" {
		t.Fatalf("first area: %+v", first)
	}
	if second.ArrivalTime == nil || *second.ArrivalTime != "2024-04-17T23:15:02+09:00" || second.Condition != "" {
		t.Fatalf("arrival time: %+v", second)
	}
	if last := e.Areas[8]; last.ForecastIntensity.From != "3" || last.ForecastIntensity.To != "4" || last.ForecastIntensity.Text != "3〜4" {
		t.Fatalf("range: %+v", last.ForecastIntensity)
	}
	if e.Change == nil || e.Change.MaxLongPeriodClass != "1" || !strings.Contains(e.Change.Description, "長周期地震動階級が1以上大きく") {
		t.Fatalf("change: %+v", e.Change)
	}
	if len(e.Comments) != 1 || e.Comments[0] != "強い揺れに警戒してください。" {
		t.Fatalf("comments: %v", e.Comments)
	}
	h := r.Hypocenter
	if h.Code != "681" || h.LandOrSea != "海域" || h.ArrivalTime == nil || h.Accuracy == nil || h.Accuracy.Epicenter != "4" {
		t.Fatalf("hypocenter details: %+v", h)
	}
	// DMDATA forbids magnitude and depth for warnings in any representation.
	if h.MagnitudeText != "" || h.MagnitudeType != "" || h.CoordinateText != "" || h.Accuracy.Depth != "" || h.Accuracy.Magnitude != "" || h.Accuracy.MagnitudePoints != nil {
		t.Fatalf("warning exposed magnitude or depth: %+v %+v", h, h.Accuracy)
	}
	wire, _ := json.Marshal(r)
	for _, forbidden := range []string{"Ｍ５．８", "３０ｋｍ", "5.8", "magnitude_text", "coordinate_text"} {
		if bytes.Contains(wire, []byte(forbidden)) {
			t.Fatalf("warning wire output contains %q", forbidden)
		}
	}
}

func TestEEWForecastDetailsRangesAndChanges(t *testing.T) {
	r := normalizeFixture(t, "VXSE45", fixture(t, "77_01_02_240613_VXSE45.xml"))
	h := r.Hypocenter
	if h.MagnitudeType != "Mj" || h.MagnitudeText != "Ｍ４．５" || h.CoordinateText != "北緯３３．２度　東経１３２．４度　深さ　３０ｋｍ" ||
		h.Accuracy == nil || h.Accuracy.Depth != "4" || h.Accuracy.Magnitude != "2" || h.Accuracy.MagnitudePoints == nil || *h.Accuracy.MagnitudePoints != 0 {
		t.Fatalf("forecast hypocenter: %+v %+v", h, h.Accuracy)
	}
	if r.EEW == nil || r.EEW.Change == nil || r.EEW.Change.Reason != "0" || r.EEW.Change.Description != "" || r.EEW.Headline != "" {
		t.Fatalf("unchanged forecast: %+v", r.EEW)
	}

	data := fixture(t, "37_01_01_240613_VXSE43.xml")
	data = bytes.Replace(data, []byte("<ForecastInt><From>5-</From><To>5-</To></ForecastInt><ForecastLgInt><From>0</From>"),
		[]byte("<ForecastInt><From>5-</From><To>over</To></ForecastInt><ForecastLgInt><From>0</From>"), 1)
	data = bytes.Replace(data, []byte("<MaxIntChange>0</MaxIntChange>"), []byte("<MaxIntChange>1</MaxIntChange>"), 1)
	data = bytes.Replace(data, []byte("<MaxIntChangeReason>0</MaxIntChangeReason>"), []byte("<MaxIntChangeReason>1</MaxIntChangeReason>"), 1)
	r = normalizeFixture(t, "VXSE43", data)
	if a := r.EEW.Areas[0]; a.ForecastIntensity.To != "over" || a.ForecastIntensity.Text != "5弱以上" {
		t.Fatalf("over range: %+v", a.ForecastIntensity)
	}
	if c := r.EEW.Change; !strings.Contains(c.Description, "最大予想震度が1.0以上大きく") || !strings.Contains(c.Description, "理由：主としてMが変化したため") {
		t.Fatalf("change reason: %+v", c)
	}

	cancel := normalizeFixture(t, "VXSE45", fixture(t, "77_01_33_240613_VXSE45.xml"))
	if cancel.EEW != nil {
		t.Fatal("cancellation carries EEW details")
	}
}

func TestMagnitudeConditionIsKeptWhenValueIsUnknown(t *testing.T) {
	data := bytes.Replace(fixture(t, firstEEW), []byte(`<jmx_eb:Magnitude type="Mj" description="Ｍ４．２">4.2</jmx_eb:Magnitude>`),
		[]byte(`<jmx_eb:Magnitude type="Mj" condition="不明" description="Ｍ不明">NaN</jmx_eb:Magnitude>`), 1)
	r := normalizeFixture(t, "VXSE45", data)
	if r.Magnitude != nil || r.Hypocenter.Magnitude != nil || r.Hypocenter.MagnitudeCondition != "不明" || r.Hypocenter.MagnitudeText != "Ｍ不明" {
		t.Fatalf("magnitude condition: %+v", r.Hypocenter)
	}
}

func TestObservedIntensityTreeCodesConditionsAndRevisions(t *testing.T) {
	data := fixture(t, "32-35_01_03_240613_VXSE53.xml")
	extra := []byte("</City><City><Name>未入電町</Name><Code>2299901</Code><Condition>震度５弱以上未入電</Condition></City>" +
		"<City><Name>追加町</Name><Code>2299902</Code><MaxInt>4</MaxInt><Revise>追加</Revise></City>")
	data = bytes.Replace(data, []byte("</City>"), extra, 1)
	r := normalizeFixture(t, "VXSE53", data)
	o := r.IntensityObservation
	if o == nil || o.MaxIntensity != "5弱" || len(o.Areas) == 0 {
		t.Fatalf("observation: %+v", o)
	}
	pref := o.Areas[0]
	region := pref.Children[0]
	if pref.Level != "prefecture" || pref.Code != "22" || region.Level != "region" || region.Code != "440" {
		t.Fatalf("hierarchy: %+v %+v", pref, region)
	}
	var unreceived, added, station *model.ObservedArea
	for i := range region.Children {
		c := &region.Children[i]
		switch c.Name {
		case "未入電町":
			unreceived = c
		case "追加町":
			added = c
		}
		if station == nil && len(c.Children) > 0 {
			station = &c.Children[0]
		}
	}
	if unreceived == nil || unreceived.Intensity != "5弱以上未入電" || unreceived.Condition != "震度５弱以上未入電" || unreceived.Code != "2299901" {
		t.Fatalf("unreceived city: %+v", unreceived)
	}
	if added == nil || added.Revise != "追加" || added.Intensity != "4" {
		t.Fatalf("revised city: %+v", added)
	}
	if station == nil || station.Level != "station" || station.Code == "" || station.Intensity == "" {
		t.Fatalf("station: %+v", station)
	}
	rows := map[string]string{}
	for _, s := range r.Bulletin.Sections {
		for _, row := range s.Rows {
			rows[s.Title+"|"+row.Label] = row.Value
		}
	}
	if rows["観測 / 静岡県 / 静岡県伊豆 / 未入電町|最大震度"] != "5弱以上未入電" || rows["観測 / 静岡県 / 静岡県伊豆 / 追加町|修正"] != "追加" {
		t.Fatal("bulletin rows lost the condition or revision")
	}
	found := false
	for _, a := range r.AffectedAreas {
		found = found || a.Name == "未入電町" && a.MaxIntensity == "5弱以上未入電"
	}
	if !found {
		t.Fatal("unreceived municipality missing from affected areas")
	}
	if h := r.Hypocenter; h.Code != "485" || h.ArrivalTime == nil || h.CoordinateText == "" || h.MagnitudeType != "Mj" {
		t.Fatalf("hypocenter details: %+v", h)
	}
}

func TestLongPeriodBandsAndHypocenterSource(t *testing.T) {
	r := normalizeFixture(t, "VXSE62", fixture(t, "78_01_01_240613_VXSE62.xml"))
	o := r.IntensityObservation
	if o == nil || o.MaxLongPeriodClass != "3" || o.LongPeriodCategory != "4" {
		t.Fatalf("long period summary: %+v", o)
	}
	station := o.Areas[0].Children[0].Children[0]
	if station.Level != "station" || station.Code != "0421200" || station.LongPeriodClass != "3" || station.SVA == nil || *station.SVA.Value != 20.5 ||
		len(station.Periods) != 7 {
		t.Fatalf("station: %+v", station)
	}
	if p := station.Periods[1]; p.Band != "2" || p.PeriodUnit != "秒台" || p.LongPeriodClass != "3" || p.SVA == nil || *p.SVA != 50.3 || p.SVAUnit != "cm/s" {
		t.Fatalf("period band: %+v", p)
	}

	data := fixture(t, "32-35_01_02_240613_VXSE52.xml")
	data = bytes.Replace(data, []byte("</jmx_eb:Coordinate>"), []byte("</jmx_eb:Coordinate><DetailedName>詳細地名</DetailedName><DetailedCode>9001</DetailedCode><LandOrSea>海域</LandOrSea>"), 1)
	data = bytes.Replace(data, []byte("</Area>\n      </Hypocenter>"), []byte("</Area><Source>ＵＳＧＳ</Source></Hypocenter>"), 1)
	r = normalizeFixture(t, "VXSE52", data)
	if h := r.Hypocenter; h.DetailedName != "詳細地名" || h.Source != "ＵＳＧＳ" || h.LandOrSea != "海域" {
		t.Fatalf("detailed hypocenter: %+v", h)
	}
}

func TestRelatedEarthquakesOfTsunamiBulletins(t *testing.T) {
	r := normalizeFixture(t, "VTSE52", fixture(t, "32-39_12_05_250206_VTSE52.xml"))
	if len(r.RelatedEarthquakes) != 1 {
		t.Fatalf("related earthquakes: %+v", r.RelatedEarthquakes)
	}
	h := r.RelatedEarthquakes[0]
	if h.Code != "689" || h.Latitude == nil || *h.Latitude != 33.2 || h.Longitude == nil || *h.Longitude != 136 || h.DepthKM == nil || *h.DepthKM != 10 ||
		h.Magnitude != nil || h.MagnitudeCondition != "不明" || h.MagnitudeText != "Ｍ８を超える巨大地震" || h.NameFromMark == "" {
		t.Fatalf("tsunami earthquake: %+v", h)
	}
	r = normalizeFixture(t, "VTSE41", fixture(t, "38-39_02_01_250206_VTSE41.xml"))
	if len(r.RelatedEarthquakes) != 1 || r.RelatedEarthquakes[0].Magnitude == nil || *r.RelatedEarthquakes[0].Magnitude != 7.0 || r.Hypocenter != nil {
		t.Fatalf("VTSE41 earthquake: %+v", r.RelatedEarthquakes)
	}
}
