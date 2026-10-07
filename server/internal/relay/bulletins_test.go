package relay

import (
	"encoding/json"
	"quakerelay/server/internal/model"
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
