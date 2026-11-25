package eew

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC)

func at(seconds int) time.Time { return t0.Add(time.Duration(seconds) * time.Second) }

func forecast(serial, issued int) Telegram {
	n := serial
	return Telegram{Code: "VXSE45", Series: SeriesForecast, TelegramID: fmt.Sprintf("f%d", serial), EventID: "E1", Serial: &n,
		InfoType: InfoIssue, IssuedAt: at(issued), ControlAt: at(issued), ContentHash: fmt.Sprintf("hf%d", serial),
		Forecast: &ForecastData{MaxIntensity: Intensity{Kind: "exact", Value: "4"}, RegionForecasts: []RegionForecast{}}}
}

func region(code, last string) ReportRegion {
	return ReportRegion{Region: Region{CodeType: "地震情報／細分区域", Code: code, Name: "地域" + code, Level: LevelRegion}, LastKind: last}
}

func warning(serial, issued int, regions ...ReportRegion) Telegram {
	n := serial
	return Telegram{Code: "VXSE43", Series: SeriesWarning, TelegramID: fmt.Sprintf("w%d", serial), EventID: "E1", Serial: &n,
		InfoType: InfoIssue, IssuedAt: at(issued), ControlAt: at(issued), ContentHash: fmt.Sprintf("hw%d", serial),
		Warning: &WarningReport{Regions: regions}}
}

func cancel(series string, serial, issued int) Telegram {
	t := forecast(serial, issued)
	if series == SeriesWarning {
		t = warning(serial, issued)
	}
	t.TelegramID, t.InfoType, t.Forecast, t.Warning, t.ContentHash = "c"+t.TelegramID, InfoCancel, nil, nil, "hc"+t.TelegramID
	return t
}

// committed is the context of a report QuickRelay made current.
func committed(issued int, t Telegram) Context {
	return Context{ReceivedAt: at(issued).Add(60 * time.Millisecond), Origin: OriginLive, Upstream: "u1",
		Relay: &RelayState{Serial: t.Serial, Cancelled: t.InfoType == InfoCancel, Final: t.Final || t.InfoType == InfoCancel}}
}

type step struct {
	t       Telegram
	c       Context
	outcome string
	reason  string
}

func run(t *testing.T, s *State, steps ...step) *State {
	t.Helper()
	for i, st := range steps {
		before, _ := json.Marshal(s)
		next, r := Apply(s, st.t, st.c)
		if r.Outcome != st.outcome || r.Reason != st.reason {
			t.Fatalf("step %d (%s %s): got %s/%s, want %s/%s", i, st.t.TelegramID, st.t.InfoType, r.Outcome, r.Reason, st.outcome, st.reason)
		}
		if r.Outcome != Applied {
			if after, _ := json.Marshal(s); string(after) != string(before) {
				t.Fatalf("step %d changed the previous state", i)
			}
			if s != nil && !reflect.DeepEqual(next, *s) {
				t.Fatalf("step %d returned a changed state without applying", i)
			}
			continue
		}
		if r.ChangedSeries != st.t.Series {
			t.Fatalf("step %d changed %s", i, r.ChangedSeries)
		}
		s = &next
	}
	return s
}

func ok(t Telegram, issued int) step { return step{t: t, c: committed(issued, t), outcome: Applied} }

func TestSeriesAreIndependent(t *testing.T) {
	// Design example: 101 warning first, 102 forecast final, 103 forecast cancel.
	w := warning(1, 0, region("622", "00"))
	final := forecast(4, 5)
	final.Final = true
	c := cancel(SeriesForecast, 5, 8)
	s := run(t, nil, ok(w, 0))
	warned := *s.Event.Warning
	if s.Event.Revision != 1 || warned.Revision != 1 || s.Event.Forecast != nil || *warned.SpeechUntil != "2026-11-25T03:00:15Z" {
		t.Fatal("warning first", s.Event)
	}
	s = run(t, s, ok(final, 5))
	if s.Event.Revision != 2 || s.Event.Forecast.Status != StatusFinal || s.Event.Forecast.SpeechUntil != nil || s.Event.Forecast.Data == nil {
		t.Fatal("final forecast", s.Event.Forecast)
	}
	s = run(t, s, ok(c, 8))
	f := s.Event.Forecast
	if s.Event.Revision != 3 || f.Status != StatusCanceled || f.Revision != 2 || f.Data != nil || f.Source.TelegramID != "cf5" ||
		f.Source.InfoType != InfoCancel || *f.SpeechUntil != "2026-11-25T03:00:38Z" {
		t.Fatal("forecast cancel", f)
	}
	if !reflect.DeepEqual(*s.Event.Warning, warned) || s.Event.Terminal() {
		t.Fatal("forecast updates touched the warning", s.Event.Warning)
	}
}

func TestCancellationFollowsRelayWhateverTheSerial(t *testing.T) {
	low := cancel(SeriesForecast, 4, 6)
	s := run(t, nil, ok(forecast(5, 0), 0),
		step{t: low, c: Context{ReceivedAt: at(6), Origin: OriginLive, Upstream: "u1"}, outcome: NotApplied, reason: "cancel_not_committed_by_relay"},
		step{t: low, c: Context{ReceivedAt: at(6), Origin: OriginLive, Upstream: "u1", Relay: &RelayState{}}, outcome: NotApplied, reason: "cancel_not_committed_by_relay"},
		ok(low, 6),
		step{t: cancel(SeriesForecast, 6, 7), c: committed(7, cancel(SeriesForecast, 6, 7)), outcome: Duplicate, reason: "already_canceled"},
		step{t: forecast(3, 1), c: committed(1, forecast(3, 1)), outcome: Stale, reason: "older_serial"},
		step{t: forecast(6, 7), c: committed(7, forecast(6, 7)), outcome: Quarantined, reason: "after_cancel"},
	)
	if s.Event.Forecast.Status != StatusCanceled || *s.Event.Forecast.Source.Serial != "4" {
		t.Fatal(s.Event.Forecast)
	}
	// A cancellation that arrives first is recorded; earlier reports stay history.
	first := cancel(SeriesWarning, 3, 10)
	s = run(t, nil, ok(first, 10),
		step{t: warning(2, 5, region("1", "00")), c: committed(5, warning(2, 5)), outcome: Stale, reason: "before_cancel"},
		step{t: warning(4, 11, region("1", "00")), c: committed(11, warning(4, 11)), outcome: Quarantined, reason: "after_cancel"},
	)
	if s.Event.Warning.Status != StatusCanceled || s.Event.Warning.Data != nil || s.Event.Forecast != nil || !s.Event.Terminal() {
		t.Fatal("cancellation first", s.Event)
	}
}

func TestFinalForecastStillTakesCancellation(t *testing.T) {
	final := forecast(10, 0)
	final.Final = true
	run(t, nil, ok(final, 0),
		step{t: forecast(11, 1), c: committed(1, forecast(11, 1)), outcome: Quarantined, reason: "after_final"},
		step{t: forecast(9, 1), c: committed(1, forecast(9, 1)), outcome: Stale, reason: "older_serial"},
		ok(cancel(SeriesForecast, 11, 2), 2))
}

func TestIssueOrderingAndQuarantine(t *testing.T) {
	conflict := forecast(2, 1)
	conflict.TelegramID, conflict.ContentHash = "other", "other"
	missing := forecast(3, 2)
	missing.Serial = nil
	correction := forecast(3, 2)
	correction.InfoType = InfoCorrection
	noData := forecast(3, 2)
	noData.Forecast = nil
	s := run(t, nil, ok(forecast(2, 1), 1),
		step{t: forecast(1, 0), c: committed(0, forecast(1, 0)), outcome: Stale, reason: "older_serial"},
		step{t: forecast(2, 1), c: committed(1, forecast(2, 1)), outcome: Duplicate, reason: "same_serial"},
		step{t: conflict, c: committed(1, conflict), outcome: Quarantined, reason: "same_serial_conflict"},
		step{t: missing, c: committed(2, missing), outcome: Quarantined, reason: "missing_serial"},
		step{t: correction, c: committed(2, correction), outcome: Quarantined, reason: "correction_unsupported"},
		step{t: noData, c: committed(2, noData), outcome: Quarantined, reason: "missing_data"},
		ok(forecast(5, 3), 3), // A gap in serials is not waited for.
	)
	if s.Event.Revision != 2 || s.Event.Forecast.Revision != 2 || *s.Event.Forecast.Source.Serial != "5" || *s.Event.Forecast.SpeechUntil != "2026-11-25T03:00:13Z" {
		t.Fatal(s.Event)
	}
}

func TestWarningRegionsAccumulateWithSources(t *testing.T) {
	s := run(t, nil, ok(warning(1, 0, region("A", "00"), region("B", "00")), 0))
	d := s.Event.Warning.Data
	if len(d.ActiveRegions) != 2 || len(d.AddedRegions) != 2 || d.Coverage != CoverageComplete || len(d.RegionSources) != 0 ||
		*d.ActiveRegions[0].SourceRef != "w1" || *d.ActiveRegions[0].SpeechUntil != "2026-11-25T03:00:15Z" {
		t.Fatal("first", d)
	}
	s = run(t, s, ok(warning(3, 4, region("A", "31"), region("B", "31"), region("C", "00")), 4))
	d = s.Event.Warning.Data
	a, c := d.ActiveRegions[0], d.ActiveRegions[2]
	if len(d.ReportRegions) != 3 || len(d.AddedRegions) != 1 || d.AddedRegions[0].Code != "C" || *a.SourceRef != "w1" ||
		*a.SpeechUntil != "2026-11-25T03:00:15Z" || *c.SourceRef != "w3" || *c.SpeechUntil != "2026-11-25T03:00:19Z" {
		t.Fatal("continuation must not extend earlier deadlines", d)
	}
	if len(d.RegionSources) != 1 || d.RegionSources[0].TelegramID != "w1" || d.Coverage != CoverageComplete {
		t.Fatal("region sources", d.RegionSources, d.Coverage)
	}
	// Regions missing from a later report are kept: absence is not cancellation.
	s = run(t, s, ok(warning(4, 6, region("A", "31")), 6))
	d = s.Event.Warning.Data
	if len(d.ActiveRegions) != 3 || len(d.ReportRegions) != 1 || len(d.AddedRegions) != 0 || len(d.RegionSources) != 2 || s.Event.Warning.Revision != 3 {
		t.Fatal("regions removed", d)
	}
}

func TestWarningCoverageBecomesPartial(t *testing.T) {
	// First seen report lists a region warned by a report this bridge missed.
	s := run(t, nil, ok(warning(2, 0, region("A", "31"), region("C", "00")), 0))
	d := s.Event.Warning.Data
	if d.Coverage != CoveragePartial || d.ActiveRegions[0].SourceRef != nil || d.ActiveRegions[0].SpeechUntil != nil || *d.ActiveRegions[1].SourceRef != "w2" {
		t.Fatal("unseen earlier report", d)
	}
	s = run(t, s, ok(warning(3, 2, region("A", "31"), region("C", "31")), 2))
	if s.Event.Warning.Data.Coverage != CoveragePartial {
		t.Fatal("partial reverted")
	}
	// A new upstream connection between reports may hide a report.
	s = run(t, nil, ok(warning(1, 0, region("A", "00")), 0))
	next := warning(2, 2, region("A", "31"))
	c := committed(2, next)
	c.Upstream = "u2"
	s = run(t, s, step{t: next, c: c, outcome: Applied})
	if s.Event.Warning.Data.Coverage != CoveragePartial {
		t.Fatal("generation change kept complete coverage")
	}
}

func TestSpeechDeadlinesAndAge(t *testing.T) {
	f := forecast(1, 0)
	c := committed(0, f)
	c.ReceivedAt = at(-120) // issue time two minutes ahead of the VPS clock
	s := run(t, nil, step{t: f, c: c, outcome: Applied})
	if s.Event.Forecast.SpeechUntil != nil {
		t.Fatal("deadline from an implausible issue time")
	}
	old := forecast(1, 0)
	late := committed(0, old)
	late.ReceivedAt = at(int(MaxInputAge/time.Second) + 1)
	run(t, nil, step{t: old, c: late, outcome: Ignored, reason: "too_old"})
	late.Origin = OriginHistorical
	run(t, nil, step{t: old, c: late, outcome: Applied})
}

func TestWireShape(t *testing.T) {
	s := run(t, nil, ok(warning(1, 0, region("A", "00")), 0), ok(cancel(SeriesWarning, 2, 3), 3))
	data, err := json.Marshal(s.Event)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`"eventId":"E1"`, `"environment":"production"`, `"revision":"2"`, `"forecast":null`, `"status":"canceled"`,
		`"data":null`, `"serial":"2"`, `"sentAt":null`, `"infoType":"cancel"`, `"origin":"live"`, `"speechUntil":"2026-11-25T03:00:33Z"`} {
		if !strings.Contains(text, want) {
			t.Fatal("missing", want, text)
		}
	}
	var back Event
	if err = json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(back, s.Event) {
		t.Fatal("round trip", err)
	}
}
