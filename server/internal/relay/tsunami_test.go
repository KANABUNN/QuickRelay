package relay

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/model"
)

func TestTsunamiReleaseWithChangedEarthquakeIDsAndLateActivityToken(t *testing.T) {
	st, worker, sender, seed := setup(t)
	ctx := context.Background()
	enableLive(t, st, seed)
	prefs := model.DefaultPreferences()
	prefs.LiveActivitiesEnabled = true
	prefs.EventTypes = []string{"tsunami_warning"}
	if err := st.Preferences(ctx, "phone", prefs); err != nil {
		t.Fatal(err)
	}
	var now time.Time
	worker.Now = func() time.Time { return now }
	replay := func(id, name string) model.Report {
		t.Helper()
		data, err := os.ReadFile("../dmdata/testdata/jma/" + name)
		if err != nil {
			t.Fatal(err)
		}
		e := dmdata.Envelope{Type: "data", ID: id, Classification: "telegram.earthquake", Format: "xml", Encoding: "utf-8", Body: string(data)}
		e.Head.Type = "VTSE41"
		frame, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		report, err := dmdata.Normalize(frame, seed.ReportedAt)
		if err != nil {
			t.Fatal(err)
		}
		now = report.ReportedAt.Add(time.Second)
		report.ReceivedAt = now
		return ingestLive(t, st, report)
	}
	first := replay("official-warning", "38-39_02_01_250206_VTSE41.xml")
	if worked, err := worker.Step(ctx); err != nil || !worked || len(sender.requests) != 1 || liveAPS(t, sender.requests[0])["event"] != "start" {
		t.Fatal("advisory start missing", worked, err)
	}
	clear := replay("official-release", "38-39_02_04_250206_VTSE41.xml")
	if clear.EventID == first.EventID || !clear.TsunamiAreas[0].Released() || clear.TsunamiAreas[0].Code != "771" {
		t.Fatal("release fixture did not exercise changed earthquake IDs and zone codes")
	}
	if worked, err := worker.Step(ctx); err != nil || !worked || len(sender.requests) != 2 {
		t.Fatal("warning-only installation did not receive release", worked, err)
	}
	alert := liveAPS(t, sender.requests[1])["alert"].(map[string]any)
	if alert["title"] != "津波警報・注意報の解除" || !strings.Contains(alert["body"].(string), "種子島・屋久島地方") {
		t.Fatal(alert)
	}
	if err := st.RegisterLiveActivityToken(ctx, "phone", first.EventID, "VTSE41", "original-activity", "eeff", first.ServerSequence, now); err != nil {
		t.Fatal(err)
	}
	if worked, err := worker.LiveStep(ctx); err != nil || !worked || len(sender.requests) != 3 || sender.requests[2].Token != "eeff" {
		t.Fatal("original activity did not end using its late token", worked, err)
	}
	end := liveAPS(t, sender.requests[2])
	if end["event"] != "end" || end["alert"] != nil {
		t.Fatal(end)
	}
	if end["content-state"].(map[string]any)["detailEventID"] != clear.EventID {
		t.Fatal("activity detail still opens the original event", end)
	}
	live, err := st.LiveActivity(ctx, "phone", first.EventID, "VTSE41")
	if err != nil || live.State != "ended" || live.LastSequence != clear.ServerSequence {
		t.Fatal(live, err)
	}
}

func TestTsunamiPartialReleaseUsesInstallationRegion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		regions   []string
		liveEvent string
	}{
		{"released zone only", []string{"岩手県"}, "end"},
		{"both zones", nil, "update"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, worker, sender, first := setup(t)
			ctx := context.Background()
			enableLive(t, st, first)
			prefs := model.DefaultPreferences()
			prefs.LiveActivitiesEnabled = true
			prefs.EventTypes = []string{"tsunami_warning"}
			prefs.TsunamiRegions = tc.regions
			if err := st.Preferences(ctx, "phone", prefs); err != nil {
				t.Fatal(err)
			}
			now := first.ReportedAt
			worker.Now = func() time.Time { return now }
			first.EventID, first.Category, first.TelegramType = "original-event", "tsunami", "VTSE41"
			first.Classification, first.EventType, first.Warning = "telegram.earthquake", "tsunami_warning", true
			first.AffectedAreas = []model.AffectedArea{{Name: "岩手県"}, {Name: "宮城県"}}
			first.TsunamiAreas = []model.TsunamiArea{{Name: "岩手県", Code: "202", Kind: "62"}, {Name: "宮城県", Code: "210", Kind: "51"}}
			first = ingestLive(t, st, first)
			if _, err := worker.Step(ctx); err != nil {
				t.Fatal(err)
			}
			if err := st.RegisterLiveActivityToken(ctx, "phone", first.EventID, "VTSE41", "activity", "eeff", first.ServerSequence, now); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Second)
			partial := first
			partial.ID, partial.MessageID, partial.EventID = "partial", "partial", "changed-event"
			partial.ReportedAt, partial.ReceivedAt = now, now
			partial.TsunamiAreas = []model.TsunamiArea{{Name: "岩手県", Code: "202", Kind: "60", LastKind: "62"}, {Name: "宮城県", Code: "210", Kind: "51"}}
			partial = ingestLive(t, st, partial)
			if _, err := worker.Step(ctx); err != nil || len(sender.requests) != 2 || sender.requests[1].PushType != "" {
				t.Fatal("ordinary follow-up missing or duplicate activity started", err, len(sender.requests))
			}
			alert := liveAPS(t, sender.requests[1])["alert"].(map[string]any)
			if !strings.Contains(alert["body"].(string), "岩手県の津波警報・注意報が解除されました。") {
				t.Fatal("partial release was omitted from the notification", alert)
			}
			for i := 0; i < 2; i++ {
				if _, err := worker.LiveStep(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if len(sender.requests) != 3 || sender.requests[2].Token != "eeff" {
				t.Fatal("original activity was not updated exactly once", len(sender.requests))
			}
			aps := liveAPS(t, sender.requests[2])
			if aps["event"] != tc.liveEvent {
				t.Fatal(aps)
			}
			content := aps["content-state"].(map[string]any)
			if tc.liveEvent == "update" && (content["title"] != "津波警報" || content["summary"] != "宮城県（津波警報）") {
				t.Fatal(content)
			}
		})
	}
}

func TestTsunamiReleaseRetainsDeliveredRegionAfterPreferencesChange(t *testing.T) {
	st, worker, sender, first := setup(t)
	ctx := context.Background()
	prefs := model.DefaultPreferences()
	prefs.EventTypes = []string{"tsunami_warning"}
	prefs.TsunamiRegions = []string{"岩手県"}
	if err := st.Preferences(ctx, "phone", prefs); err != nil {
		t.Fatal(err)
	}
	first.EventID, first.Category, first.TelegramType = "first-event", "tsunami", "VTSE41"
	first.Classification, first.EventType, first.Warning = "telegram.earthquake", "tsunami_warning", true
	first.TsunamiAreas = []model.TsunamiArea{{Name: "岩手県", Code: "202", Kind: "62"}, {Name: "宮城県", Code: "210", Kind: "51"}}
	first = ingestLive(t, st, first)
	if _, err := worker.Step(ctx); err != nil || len(sender.requests) != 1 {
		t.Fatal(err, len(sender.requests))
	}
	prefs.TsunamiRegions = []string{"宮城県"}
	if err := st.Preferences(ctx, "phone", prefs); err != nil {
		t.Fatal(err)
	}
	clear := first
	clear.ID, clear.MessageID, clear.EventID = "clear", "clear", "changed-event"
	clear.ReportedAt = first.ReportedAt.Add(time.Second)
	clear.ReceivedAt = clear.ReportedAt
	clear.EventType, clear.Warning = "tsunami_info", false
	clear.TsunamiAreas = []model.TsunamiArea{{Name: "岩手県", Code: "202", Kind: "60"}, {Name: "宮城県", Code: "210", Kind: "50"}}
	clear = ingestLive(t, st, clear)
	worker.Now = func() time.Time { return clear.ReportedAt }
	if _, err := worker.Step(ctx); err != nil || len(sender.requests) != 2 {
		t.Fatal("release of the originally followed area was missed", err, len(sender.requests))
	}
	alert := liveAPS(t, sender.requests[1])["alert"].(map[string]any)
	if alert["body"] != "岩手県の津波警報・注意報が解除されました。" {
		t.Fatal(alert)
	}
}

func TestTsunamiNationalOrderAllowsSameTimeCancellationWithMissingPressTime(t *testing.T) {
	st, _, _, first := setup(t)
	ctx := context.Background()
	first.EventID, first.TelegramType, first.Classification = "first-event", "VTSE41", "telegram.earthquake"
	press := first.ReportedAt.Add(time.Second)
	first.PressedAt = &press
	first = ingestLive(t, st, first)
	cancel := first
	cancel.ID, cancel.MessageID, cancel.EventID = "cancel", "cancel", "changed-event"
	cancel.PressedAt, cancel.Cancelled = nil, true
	cancel = ingestLive(t, st, cancel)
	latest, err := st.LatestTsunamiReport(ctx)
	if err != nil || latest.ID != cancel.ID {
		t.Fatal("missing press time displaced the simultaneous cancellation", latest.ID, err)
	}
}
