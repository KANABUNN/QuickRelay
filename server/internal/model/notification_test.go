package model

import "testing"

func TestPreferenceFiltersAndLifecycle(t *testing.T) {
	p := DefaultPreferences()
	p.EarthquakeRegions = []string{"石川県"}
	p.TsunamiRegions = []string{"宮崎県"}
	p.MinimumIntensity = "5-"
	max := "6強"
	r := Report{TelegramType: "VXSE45", Classification: "eew.forecast", EventType: "eew_forecast", MaxIntensity: &max,
		AffectedAreas: []AffectedArea{{Name: "石川県能登", MaxIntensity: "4"}, {Name: "宮崎県南部", MaxIntensity: "6強"}}}
	if p.Allows(r, false) {
		t.Fatal("global intensity replaced watched region intensity")
	}
	r.AffectedAreas[0].MaxIntensity = "5弱〜5強"
	if !p.Allows(r, false) {
		t.Fatal("matching intensity range suppressed")
	}
	r.AffectedAreas = []AffectedArea{{Name: "東京都", MaxIntensity: "7"}}
	if p.Allows(r, false) {
		t.Fatal("different known region sent")
	}
	r.AffectedAreas = nil
	r.MaxIntensity = nil
	if !p.Allows(r, false) {
		t.Fatal("unknown metadata suppressed")
	}
	r.Cancelled = true
	r.EventType = "eew_cancel"
	p.EventTypes = nil
	if !p.Allows(r, true) || p.Allows(r, false) {
		t.Fatal("followed cancellation lost or unrelated cancellation sent")
	}
	p.NotificationsEnabled = false
	if p.Allows(r, true) {
		t.Fatal("global opt-out ignored")
	}
	p = DefaultPreferences()
	p.TsunamiRegions = []string{"宮崎県"}
	r = Report{Category: "tsunami", TelegramType: "VTSE41", EventType: "tsunami_warning", Warning: true,
		AffectedAreas: []AffectedArea{{Name: "宮崎県"}}}
	if !p.Allows(r, false) {
		t.Fatal("tsunami filter used earthquake intensity")
	}
	r.Warning = false
	r.EventType = "tsunami_info"
	r.AffectedAreas = []AffectedArea{{Name: "沖縄県"}}
	p.EventTypes = []string{"tsunami_warning"}
	if !p.Allows(r, true) {
		t.Fatal("followed withdrawal lost after scope changed")
	}
}

func TestIntensityRankUpperBoundsAndUnknown(t *testing.T) {
	for input, want := range map[string]int{"5弱": 5, "5強": 6, "4〜5強": 6, "6-": 7, "6+": 8, "7": 9, "5弱以上": 9, "不明": -1, "": -1} {
		if got := IntensityRank(input); got != want {
			t.Errorf("%s got %d want %d", input, got, want)
		}
	}
}
