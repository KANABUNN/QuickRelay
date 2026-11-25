package dmdata

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"quakerelay/server/internal/eew"
)

func TestParseEEWForecastSample(t *testing.T) {
	got, err := ParseEEW(transport(t, "VXSE45", "xml", fixture(t, firstEEW)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != "VXSE45" || got.Series != eew.SeriesForecast || got.EventID != "20240417231454" || got.TelegramID != "xml-test-VXSE45" ||
		got.Serial == nil || *got.Serial != 1 || got.InfoType != eew.InfoIssue || got.Final || got.Warning != nil || len(got.ContentHash) != 64 {
		t.Fatal("identity", got)
	}
	if !got.IssuedAt.Equal(time.Date(2024, 4, 17, 14, 14, 57, 0, time.UTC)) || !got.ControlAt.Equal(got.IssuedAt) {
		t.Fatal("times", got.IssuedAt, got.ControlAt)
	}
	d := got.Forecast
	if d == nil || d.Hypocenter == nil || d.Hypocenter.Name != "豊後水道" || d.Hypocenter.Code != "681" || d.Hypocenter.Quality.Kind != "estimated" ||
		*d.Hypocenter.Latitude != 33.1 || *d.Hypocenter.Longitude != 132.4 || d.Hypocenter.Quality.MagnitudeRank != "2" || *d.Hypocenter.Quality.MagnitudePoints != 0 {
		t.Fatal("hypocenter", d)
	}
	if *d.Magnitude != 4.2 || *d.DepthKm != 40 || *d.OriginTime != "2024-04-17T14:14:47Z" {
		t.Fatal("values", *d.Magnitude, *d.DepthKm, *d.OriginTime)
	}
	if d.MaxIntensity != (eew.Intensity{Kind: "exact", Value: "3"}) || d.MaxLongPeriodClass == nil || d.MaxLongPeriodClass.Kind != "exact" ||
		d.RegionForecasts == nil || len(d.RegionForecasts) != 0 {
		t.Fatal("intensity", d.MaxIntensity, d.MaxLongPeriodClass, d.RegionForecasts)
	}
}

func TestParseEEWRegionForecasts(t *testing.T) {
	// The warning sample carries per-area forecasts in the same Body layout as VXSE45.
	got, err := ParseEEW(transport(t, "VXSE45", "xml", fixture(t, "37_01_01_240613_VXSE43.xml")))
	if err != nil {
		t.Fatal(err)
	}
	regions := got.Forecast.RegionForecasts
	if len(regions) != 9 || got.Forecast.MaxIntensity != (eew.Intensity{Kind: "exact", Value: "5-"}) {
		t.Fatal(len(regions), got.Forecast.MaxIntensity)
	}
	first, second, last := regions[0], regions[1], regions[8]
	if first.Code != "622" || first.Name != "愛媛県南予" || first.PrefectureCode != "9380" || first.CodeType != "地震情報／細分区域" ||
		first.KindCode != "11" || !first.IsWarning || first.Arrival.Condition != "arrived" || first.Arrival.Time != nil ||
		first.MaxIntensity != (eew.Intensity{Kind: "exact", Value: "5-"}) {
		t.Fatal("arrived area", first)
	}
	if second.Arrival.Condition != "predicted" || *second.Arrival.Time != "2024-04-17T14:15:02Z" {
		t.Fatal("predicted area", second.Arrival)
	}
	if last.MaxIntensity != (eew.Intensity{Kind: "range", Min: "3", Max: "4"}) {
		t.Fatal("range", last.MaxIntensity)
	}
}

func TestParseEEWWarningRegions(t *testing.T) {
	data := fixture(t, "37_01_01_240613_VXSE43.xml")
	got, err := ParseEEW(transport(t, "VXSE43", "xml", data))
	if err != nil {
		t.Fatal(err)
	}
	if got.Series != eew.SeriesWarning || got.Forecast != nil || got.Warning == nil || len(got.Warning.Regions) != 17 {
		t.Fatal(got)
	}
	levels := map[string]int{}
	for _, r := range got.Warning.Regions {
		if !r.Added() {
			t.Fatal("first warning region not marked as added", r)
		}
		levels[r.Level]++
	}
	if levels[eew.LevelZone] != 3 || levels[eew.LevelPrefecture] != 5 || levels[eew.LevelRegion] != 9 {
		t.Fatal(levels)
	}
	r := got.Warning.Regions[8]
	if r.CodeType != "地震情報／細分区域" || r.Code != "622" || r.Name != "愛媛県南予" || r.Level != eew.LevelRegion {
		t.Fatal(r)
	}
	// A continuation lists earlier regions with LastKind 31.
	data = bytes.ReplaceAll(data, []byte("<LastKind><Name>なし</Name><Code>00</Code></LastKind>"), []byte("<LastKind><Name>緊急地震速報（警報）</Name><Code>31</Code></LastKind>"))
	got, err = ParseEEW(transport(t, "VXSE43", "xml", data))
	if err != nil || got.Warning.Regions[0].Added() || got.Warning.Regions[0].LastKind != "31" {
		t.Fatal("continuation", err)
	}
}

func TestParseEEWCancelFinalAndQualification(t *testing.T) {
	got, err := ParseEEW(transport(t, "VXSE45", "xml", fixture(t, "77_01_33_240613_VXSE45.xml")))
	if err != nil || got.InfoType != eew.InfoCancel || *got.Serial != 32 || got.Forecast != nil || got.Final {
		t.Fatal("cancel", got, err)
	}
	source := fixture(t, firstEEW)
	data := bytes.Replace(source, []byte("</Body>"), []byte("<NextAdvisory>"+eewFinalAdvisory+"</NextAdvisory></Body>"), 1)
	if got, err = ParseEEW(transport(t, "VXSE45", "xml", data)); err != nil || !got.Final || got.Forecast == nil {
		t.Fatal("final", got, err)
	}
	for _, tc := range []struct{ name, old, new, kind string }{
		{"plum", "<Earthquake>", "<Earthquake><Condition>仮定震源要素</Condition>", "assumed"},
		{"level", `<MagnitudeCalculation rank="2">`, `<MagnitudeCalculation rank="8">`, "assumed"},
		{"single", `<Epicenter rank="4" rank2="4">`, `<Epicenter rank="1" rank2="1">`, "lowAccuracy"},
	} {
		got, err := ParseEEW(transport(t, "VXSE45", "xml", bytes.Replace(source, []byte(tc.old), []byte(tc.new), 1)))
		if err != nil || got.Forecast.Hypocenter.Quality.Kind != tc.kind {
			t.Fatal(tc.name, got.Forecast.Hypocenter, err)
		}
		if tc.kind == "assumed" && (got.Forecast.Magnitude != nil || got.Forecast.DepthKm != nil || got.Forecast.OriginTime != nil) {
			t.Fatal("assumed placeholders kept as values", tc.name)
		}
	}
	data = bytes.Replace(source, []byte("<ForecastInt><From>3</From><To>3</To></ForecastInt>"), []byte("<ForecastInt><From>5-</From><To>over</To></ForecastInt>"), 1)
	if got, _ = ParseEEW(transport(t, "VXSE45", "xml", data)); got.Forecast.MaxIntensity != (eew.Intensity{Kind: "lowerBound", Min: "5-"}) {
		t.Fatal("lower bound", got.Forecast.MaxIntensity)
	}
	data = bytes.Replace(source, []byte("<ForecastInt><From>3</From><To>3</To></ForecastInt>"), []byte("<ForecastInt><From>不明</From><To>不明</To></ForecastInt>"), 1)
	if got, _ = ParseEEW(transport(t, "VXSE45", "xml", data)); got.Forecast.MaxIntensity.Kind != "unknown" {
		t.Fatal("unknown intensity", got.Forecast.MaxIntensity)
	}
	data = bytes.Replace(source, []byte(">4.2</jmx_eb:Magnitude>"), []byte(">NaN</jmx_eb:Magnitude>"), 1)
	if got, _ = ParseEEW(transport(t, "VXSE45", "xml", data)); got.Forecast.Magnitude != nil {
		t.Fatal("unknown magnitude invented")
	}
}

func TestParseEEWRejectsOutOfScopeAndMalformed(t *testing.T) {
	forecast, warning := fixture(t, firstEEW), fixture(t, "37_01_01_240613_VXSE43.xml")
	ignored := map[string][]byte{
		"training": transport(t, "VXSE45", "xml", bytes.Replace(forecast, []byte("<Status>通常</Status>"), []byte("<Status>訓練</Status>"), 1)),
		"vxse44":   transport(t, "VXSE44", "xml", forecast),
		"quake":    transport(t, "VXSE53", "xml", operational(fixture(t, "32-35_01_03_240613_VXSE53.xml"))),
		"test":     mutateEnvelope(t, transport(t, "VXSE45", "xml", forecast), func(e *Envelope) { e.Head.Test = true }),
	}
	for name, frame := range ignored {
		if _, err := ParseEEW(frame); !errors.Is(err, ErrIgnored) {
			t.Fatal(name, err)
		}
	}
	if _, err := ParseEEW(transport(t, "VXSE45", "json", forecast)); !errors.Is(err, ErrEEWFormat) {
		t.Fatal("converted JSON accepted", err)
	}
	for name, data := range map[string][]byte{
		"no_event":  bytes.Replace(forecast, []byte("<EventID>20240417231454</EventID>"), []byte("<EventID></EventID>"), 1),
		"info_type": bytes.Replace(forecast, []byte("<InfoType>発表</InfoType>"), []byte("<InfoType>遅延</InfoType>"), 1),
		"serial":    bytes.Replace(forecast, []byte("<Serial>1</Serial>"), []byte("<Serial>x</Serial>"), 1),
		"version":   bytes.Replace(forecast, []byte("<InfoKindVersion>1.2_0</InfoKindVersion>"), []byte("<InfoKindVersion>9.9_9</InfoKindVersion>"), 1),
		"advisory":  bytes.Replace(forecast, []byte("</Body>"), []byte("<NextAdvisory>別の文言</NextAdvisory></Body>"), 1),
		"directive": append([]byte("<!DOCTYPE Report>"), forecast...),
		"no_quake":  bytes.Replace(forecast, []byte("<Earthquake>"), []byte("<Unused>"), 1),
	} {
		if _, err := ParseEEW(transport(t, "VXSE45", "xml", data)); err == nil || errors.Is(err, ErrIgnored) {
			t.Fatal(name, "accepted", err)
		}
	}
	noRegions := bytes.ReplaceAll(warning, []byte("<Code>31</Code></Kind>"), []byte("<Code>99</Code></Kind>"))
	if _, err := ParseEEW(transport(t, "VXSE43", "xml", noRegions)); err == nil {
		t.Fatal("warning without regions accepted")
	}
	final := bytes.Replace(warning, []byte("</Body>"), []byte("<NextAdvisory>"+eewFinalAdvisory+"</NextAdvisory></Body>"), 1)
	if _, err := ParseEEW(transport(t, "VXSE43", "xml", final)); err == nil {
		t.Fatal("final warning accepted")
	}
}
