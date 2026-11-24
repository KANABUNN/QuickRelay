package relay

import (
	"encoding/json"
	"quakerelay/server/internal/model"
	"strings"
	"testing"
)

func TestBulletinNotificationPriorityAndCategory(t *testing.T) {
	for _, tc := range []struct {
		event, category string
		warning, urgent bool
	}{
		{"tsunami_warning", "tsunami", true, true},
		{"tsunami_info", "tsunami", false, false},
		{"nankai_info", "advisory", true, true},
		{"nankai_info", "advisory", false, false},
		{"seismic_advisory", "advisory", false, false},
	} {
		r := model.Report{EventType: tc.event, Category: tc.category, Warning: tc.warning, Title: "合成試験", Body: "実際の情報ではありません。"}
		b, err := Payload(r, model.DefaultPreferences())
		if err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		_ = json.Unmarshal(b, &p)
		level := p["aps"].(map[string]any)["interruption-level"]
		if p["category"] != tc.category || (level == "time-sensitive") != tc.urgent {
			t.Fatal(string(b))
		}
		prefs := model.DefaultPreferences()
		prefs.TimeSensitiveEnabled = false
		prefs.CustomSoundEnabled = false
		b, _ = Payload(r, prefs)
		_ = json.Unmarshal(b, &p)
		aps := p["aps"].(map[string]any)
		if aps["interruption-level"] != "active" || aps["sound"] != "default" {
			t.Fatal("preference override ignored")
		}
	}
}

func TestTsunamiObservationPushUsesSelectedRegionAndWaveDetails(t *testing.T) {
	r := model.Report{
		TelegramType: "VTSE51", EventType: "tsunami_info", Category: "tsunami",
		Title: "津波情報", Body: "現在の観測値をお知らせします。",
		AffectedAreas: []model.AffectedArea{{Name: "岩手県"}, {Name: "宮城県"}, {Name: "青森県"}},
		Bulletin: &model.Bulletin{Sections: []model.BulletinSection{
			{Title: "津波予報区：青森県"},
			{Title: "津波観測：大船渡", Rows: []model.BulletinRow{
				{Label: "地域", Value: "岩手県"},
				{Label: "第1波", Value: "03/11 14:46 JST / 引き / 追加"},
				{Label: "最大波", Value: "観測中"},
			}},
			{Title: "津波観測：石巻市鮎川", Rows: []model.BulletinRow{
				{Label: "地域", Value: "宮城県"},
				{Label: "最大波", Value: "03/11 15:26 JST / 1.2m以上 / 上昇中 / 更新"},
			}},
		}},
	}

	check := func(regions []string, want ...string) {
		t.Helper()
		prefs := model.DefaultPreferences()
		prefs.TsunamiRegions = regions
		b, err := Payload(r, prefs)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range want {
			if !strings.Contains(string(b), value) {
				t.Fatalf("want %q in %s", value, b)
			}
		}
		if strings.Contains(string(b), "time-sensitive") || strings.Contains(string(b), r.Body) {
			t.Fatal(string(b))
		}
	}
	check(nil, "石巻市鮎川", "15:26 JST", "1.2m以上", "上昇中", "更新")
	check([]string{"岩手県"}, "沿岸の津波観測に関する情報", "大船渡", "14:46 JST", "引き", "観測中")

	prefs := model.DefaultPreferences()
	prefs.TsunamiRegions = []string{"青森県"}
	if prefs.Allows(r, false) {
		t.Fatal("a forecast alone matched the observation preference")
	}

	forecast := r
	forecast.Bulletin = &model.Bulletin{Sections: r.Bulletin.Sections[:1]}
	if !prefs.Allows(forecast, false) {
		t.Fatal("forecast-only VTSE51 lost its notification")
	}
	prefs.TsunamiRegions = nil
	enabled := false
	prefs.TsunamiObservationsEnabled = &enabled
	if prefs.Allows(r, false) || !prefs.Allows(forecast, false) {
		t.Fatal("forecast and observation could not be selected independently")
	}
	enabled = true
	prefs.EventTypes = []string{"tsunami_warning"}
	if !prefs.Allows(r, false) || prefs.Allows(forecast, false) {
		t.Fatal("observation-only preference ignored")
	}
	prefs.TsunamiObservationsEnabled = nil
	if prefs.Allows(r, false) {
		t.Fatal("legacy opt-out was lost")
	}
}
