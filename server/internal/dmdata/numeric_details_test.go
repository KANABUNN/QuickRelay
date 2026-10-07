package dmdata

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/model"
)

func TestHypocenterNumbersRetainQualifications(t *testing.T) {
	now := time.Date(2026, 10, 5, 3, 0, 10, 0, time.UTC)
	for _, code := range []string{"VXSE45", "VXSE43"} {
		for _, mode := range []string{"plum", "level", "single_station", "normal"} {
			t.Run(code+"/"+mode, func(t *testing.T) {
				accuracy := map[string]any{"epicenters": []string{"4", "9"}, "magnitudeCalculation": "4"}
				eq := map[string]any{"originTime": "2026-10-05T03:00:00Z", "magnitude": map[string]any{"value": "1.0"},
					"hypocenter": map[string]any{"name": "合成震源", "depth": map[string]string{"value": "10"},
						"coordinate": map[string]any{"latitude": map[string]string{"value": "31.8"}, "longitude": map[string]string{"value": "131.7"}},
						"accuracy":   accuracy}}
				body := map[string]any{"earthquake": eq, "intensity": map[string]any{"forecastMaxInt": map[string]string{"from": "5-", "to": "over"}},
					"prefectures": []map[string]string{{"name": "合成対象地域"}}}
				wantStatus := "estimated"
				switch mode {
				case "plum":
					eq["condition"] = "仮定震源要素"
					wantStatus = "assumed"
				case "level":
					accuracy["magnitudeCalculation"] = "8"
					eq["magnitude"] = map[string]any{"value": nil}
					eq["originTime"] = nil
					delete(body, "intensity")
					wantStatus = "assumed"
				case "single_station":
					accuracy["epicenters"] = []string{"1", "1"}
					delete(body, "intensity")
					wantStatus = "low_accuracy"
				}
				r, err := Normalize(frame(t, code, body), now)
				if err != nil {
					t.Fatal(err)
				}
				h := r.Hypocenter
				if h == nil || h.Status != wantStatus || h.Epicenter == nil || *h.Epicenter != "合成震源" ||
					h.Latitude == nil || *h.Latitude != 31.8 || h.Longitude == nil || *h.Longitude != 131.7 {
					t.Fatalf("hypocenter missing or changed: %+v", h)
				}
				if wantStatus != "estimated" && (h.Note == "" || !strings.Contains(r.Body, h.Note)) {
					t.Fatal("qualification missing from notification")
				}
				if wantStatus == "assumed" && (r.Epicenter != nil || r.Magnitude != nil || r.DepthKM != nil || r.OriginTime != nil) {
					t.Fatal("legacy clients could display unqualified assumed values")
				}
				if code == "VXSE43" {
					if h.Magnitude != nil || h.DepthKM != nil || h.DepthCondition != "" || strings.Contains(r.Body, "M1.0") || strings.Contains(r.Body, "10km") {
						t.Fatal("warning exposed magnitude or depth")
					}
					if !strings.Contains(r.Body, "合成対象地域") {
						t.Fatal("warning targets lost")
					}
				} else {
					if h.DepthKM == nil || *h.DepthKM != 10 || !strings.Contains(r.Body, "深さ10km") {
						t.Fatal("forecast depth lost")
					}
					if mode != "level" && (h.Magnitude == nil || *h.Magnitude != 1 || !strings.Contains(r.Body, "M1.0")) {
						t.Fatal("forecast magnitude lost")
					}
				}
				if mode == "level" && (h.OriginTime != nil || h.Magnitude != nil || r.MaxIntensity != nil) {
					t.Fatal("missing data was invented")
				}
				if mode == "plum" && (!strings.Contains(r.Body, "仮定値") || !strings.Contains(r.Body, "予想最大震度5弱以上")) {
					t.Fatal("assumption/range missing")
				}
				encoded, err := json.Marshal(r.Event())
				if err != nil {
					t.Fatal(err)
				}
				var event model.Event
				if err = json.Unmarshal(encoded, &event); err != nil || event.Hypocenter == nil || event.Hypocenter.Status != wantStatus {
					t.Fatal("qualification lost on API roundtrip", err)
				}
			})
		}
	}
}

func TestDepthConditionsAndUnknownValues(t *testing.T) {
	now := time.Date(2026, 10, 5, 3, 0, 10, 0, time.UTC)
	for _, tc := range []struct{ value, condition, want string }{
		{"700", "７００ｋｍ以上", "深さ700km以上"},
		{"0", "ごく浅い", "ごく浅い（数値0km）"},
		{"10.5", "", ""}, {"NaN", "", ""}, {"-1", "", ""},
	} {
		t.Run(tc.value, func(t *testing.T) {
			r, err := Normalize(frame(t, "VXSE45", map[string]any{"earthquake": map[string]any{
				"hypocenter": map[string]any{"depth": map[string]string{"value": tc.value, "condition": tc.condition}},
				"magnitude":  map[string]any{"value": nil}}, "intensity": map[string]any{"forecastMaxInt": map[string]string{"to": "over"}}}), now)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && !strings.Contains(r.Body, tc.want) {
				t.Fatal("depth qualifier lost", r.Body)
			}
			if tc.want == "" && r.Hypocenter.DepthKM != nil {
				t.Fatal("invalid depth converted into a number")
			}
			if r.Magnitude != nil || r.Hypocenter.Magnitude != nil || *r.MaxIntensity != "不明" {
				t.Fatal("unknown values were invented")
			}
		})
	}
}

func TestCancellationContainsNoNumericDetails(t *testing.T) {
	now := time.Date(2026, 10, 5, 3, 0, 10, 0, time.UTC)
	for _, code := range []string{"VXSE43", "VXSE45"} {
		r, err := Normalize(frame(t, code, map[string]any{"isCanceled": true, "earthquake": map[string]any{
			"condition": "仮定震源要素", "magnitude": map[string]string{"value": "1.0"}}}), now)
		if err != nil || r.Hypocenter != nil || r.Magnitude != nil || r.Body != "この情報は取り消されました。" {
			t.Fatal(r, err)
		}
	}
}
