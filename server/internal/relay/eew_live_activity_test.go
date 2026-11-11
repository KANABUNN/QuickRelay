package relay

import (
	"testing"
	"time"

	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/model"
	"quakerelay/server/internal/store"
)

type eewLiveFixture struct {
	t        *testing.T
	store    *store.Store
	worker   *Worker
	sender   *fakeSender
	forecast model.Report
	now      time.Time
}

func newEEWLiveFixture(t *testing.T) *eewLiveFixture {
	t.Helper()
	st, worker, sender, forecast := setup(t)
	enableLive(t, st, forecast)
	f := &eewLiveFixture{t: t, store: st, worker: worker, sender: sender, forecast: forecast, now: forecast.ReportedAt}
	worker.Now = func() time.Time { return f.now }
	return f
}

func (f *eewLiveFixture) publish(report model.Report, id string, serial int) model.Report {
	f.t.Helper()
	report.ID, report.MessageID = id, id
	report.Serial = &serial
	report.ReportedAt, report.ReceivedAt = f.now, f.now
	return ingestLive(f.t, f.store, report)
}

func (f *eewLiveFixture) deliver() []apns.Request {
	f.t.Helper()
	start := len(f.sender.requests)
	if worked, err := f.worker.Step(f.t.Context()); err != nil || !worked {
		f.t.Fatalf("ordinary queue step: worked=%v, error=%v", worked, err)
	}
	return f.sender.requests[start:]
}

func (f *eewLiveFixture) drainLive() []apns.Request {
	f.t.Helper()
	start := len(f.sender.requests)
	for range 10 {
		worked, err := f.worker.LiveStep(f.t.Context())
		if err != nil {
			f.t.Fatal(err)
		}
		if !worked {
			return f.sender.requests[start:]
		}
	}
	f.t.Fatal("live queue did not drain")
	return nil
}

func (f *eewLiveFixture) registerUpdateToken(start model.Report) {
	f.t.Helper()
	if err := f.store.RegisterLiveActivityToken(f.t.Context(), "phone", start.EventID, "VXSE45", "activity1", "eeff", start.ServerSequence, f.now); err != nil {
		f.t.Fatal(err)
	}
}

func eewWarningReport(forecast model.Report) model.Report {
	forecast.TelegramType = "VXSE43"
	forecast.Classification = "eew.warning"
	forecast.EventType = "eew_warning"
	forecast.Title = "緊急地震速報（警報）"
	forecast.Warning = true
	return forecast
}

func assertEEWLivePush(t *testing.T, requests []apns.Request, event string, warning bool) map[string]any {
	t.Helper()
	if len(requests) != 1 || requests[0].PushType != "liveactivity" {
		t.Fatalf("want one Live Activity %s, got %v", event, requests)
	}
	aps := liveAPS(t, requests[0])
	if aps["event"] != event || (aps["alert"] != nil) != (event == "start") || aps["sound"] != nil {
		t.Fatalf("unexpected %s APS: %v", event, aps)
	}
	state := aps["content-state"].(map[string]any)
	if (state["eewWarning"] == true) != warning || (state["ended"] == true) != (event == "end") {
		t.Fatalf("unexpected %s state: %v", event, state)
	}
	return aps
}

func TestEEWLiveWarningStartsCanonicalActivityAndLaterForecastKeepsWarning(t *testing.T) {
	f := newEEWLiveFixture(t)
	warning := f.publish(eewWarningReport(f.forecast), "warning", 1)
	aps := assertEEWLivePush(t, f.deliver(), "start", true)
	attributes := aps["attributes"].(map[string]any)
	if attributes["eventID"] != warning.EventID || attributes["telegramType"] != "VXSE45" || attributes["startSequence"] != float64(warning.ServerSequence) {
		t.Fatalf("warning start did not use the shared activity identity: %v", attributes)
	}
	f.registerUpdateToken(warning)

	f.now = f.now.Add(2 * time.Second)
	f.publish(f.forecast, "forecast", 5)
	if requests := f.deliver(); len(requests) != 1 || requests[0].PushType == "liveactivity" {
		t.Fatalf("want one ordinary forecast alert after warning starts the activity: %v", requests)
	}
	assertEEWLivePush(t, f.drainLive(), "update", true)

	f.now = warning.ReportedAt.Add(warning.TTL() + time.Second)
	f.publish(f.forecast, "forecast-fresh", 6)
	assertEEWLivePush(t, f.drainLive(), "update", false)
}

func TestEEWLiveWarningDuringTokenGapUpdatesExistingForecastActivity(t *testing.T) {
	f := newEEWLiveFixture(t)
	forecast := f.publish(f.forecast, "forecast", 8)
	assertEEWLivePush(t, f.deliver(), "start", false)

	f.now = f.now.Add(2 * time.Second)
	f.publish(eewWarningReport(f.forecast), "warning", 1)
	if requests := f.deliver(); len(requests) != 1 || requests[0].PushType == "liveactivity" {
		t.Fatalf("want one ordinary warning alert while the forecast activity starts: %v", requests)
	}
	if requests := f.drainLive(); len(requests) != 0 {
		t.Fatalf("sent updates before registering a token: %v", requests)
	}

	f.registerUpdateToken(forecast)
	f.now = f.now.Add(2 * time.Second)
	assertEEWLivePush(t, f.drainLive(), "update", true)
}

func TestEEWLiveTerminationIsScopedToReportSeriesExceptForecastFinal(t *testing.T) {
	tests := []struct {
		name          string
		warningOnly   bool
		warningCancel bool
		forecastFinal bool
		wantEvent     string
		wantWarning   bool
	}{
		{name: "warning cancellation retains forecast", warningCancel: true, wantEvent: "update"},
		{name: "forecast cancellation retains warning", wantEvent: "update", wantWarning: true},
		{name: "warning cancellation ends warning-only activity", warningOnly: true, warningCancel: true, wantEvent: "end"},
		{name: "forecast final ends shared activity", forecastFinal: true, wantEvent: "end"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newEEWLiveFixture(t)
			warning := eewWarningReport(f.forecast)
			startReport := f.forecast
			if tc.warningOnly {
				startReport = warning
			}
			start := f.publish(startReport, "start", 1)
			assertEEWLivePush(t, f.deliver(), "start", tc.warningOnly)
			f.registerUpdateToken(start)
			f.now = f.now.Add(2 * time.Second)
			if !tc.warningOnly {
				f.publish(warning, "warning", 1)
				assertEEWLivePush(t, f.drainLive(), "update", true)
				f.now = f.now.Add(2 * time.Second)
			}

			terminal := f.forecast
			if tc.warningCancel {
				terminal = warning
			}
			terminal.Final = true
			if !tc.forecastFinal {
				terminal.Cancelled, terminal.Warning = true, false
				terminal.EventType = "eew_cancel"
			}
			f.publish(terminal, "terminal", 2)
			assertEEWLivePush(t, f.drainLive(), tc.wantEvent, tc.wantWarning)
		})
	}
}

func TestEEWLiveFilteredWarningDoesNotColorOrEndEligibleForecast(t *testing.T) {
	for _, name := range []string{"warning preference", "warning outside selected regions"} {
		t.Run(name, func(t *testing.T) {
			f := newEEWLiveFixture(t)
			preferences := model.DefaultPreferences()
			preferences.LiveActivitiesEnabled = true
			if name == "warning preference" {
				preferences.EventTypes = []string{"eew_forecast", "eew_cancel"}
			} else {
				preferences.EarthquakeRegions = []string{"福岡県"}
			}
			if err := f.store.Preferences(t.Context(), "phone", preferences); err != nil {
				t.Fatal(err)
			}
			forecast := f.forecast
			forecast.Warning = true // A forecast may carry warning areas without a VXSE43 alert.
			forecast.AffectedAreas = []model.AffectedArea{{Name: "福岡県福岡"}}
			start := f.publish(forecast, "forecast", 1)
			assertEEWLivePush(t, f.deliver(), "start", false)
			f.registerUpdateToken(start)

			f.now = f.now.Add(2 * time.Second)
			warning := eewWarningReport(forecast)
			warning.AffectedAreas = []model.AffectedArea{{Name: "宮城県北部"}}
			f.publish(warning, "warning", 1)
			if requests := f.deliver(); len(requests) != 0 {
				t.Fatalf("filtered warning generated an alert: %v", requests)
			}
			if requests := f.drainLive(); len(requests) > 0 {
				assertEEWLivePush(t, requests, "update", false)
			}

			f.now = f.now.Add(2 * time.Second)
			f.publish(forecast, "forecast-next", 2)
			assertEEWLivePush(t, f.drainLive(), "update", false)
		})
	}
}
