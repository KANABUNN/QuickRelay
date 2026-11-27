package model

import (
	"encoding/json"
	"testing"
)

func TestStoredMagnitudePointsOutsideTheCodesAreDropped(t *testing.T) {
	for stored, want := range map[string]int{`1`: 1, `4`: 4, `5`: 5, `0`: 0, `6`: 0, `-1`: 0, `null`: 0} {
		var r Report
		payload := `{"hypocenter":{"status":"estimated","accuracy":{"depth":"4","magnitude_points":` + stored + `}}}`
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			t.Fatal(stored, err)
		}
		a := r.Hypocenter.Accuracy
		if a == nil || a.Depth != "4" || want == 0 && a.MagnitudePoints != nil || want != 0 && (a.MagnitudePoints == nil || *a.MagnitudePoints != want) {
			t.Fatalf("stored %s: %+v", stored, a)
		}
		wire, _ := json.Marshal(r.Hypocenter.Accuracy)
		if want == 0 && string(wire) != `{"depth":"4"}` {
			t.Fatalf("stored %s served as %s", stored, wire)
		}
	}
	var a HypocenterAccuracy
	if err := json.Unmarshal([]byte(`{"magnitude_points":"5"}`), &a); err == nil {
		t.Fatal("a string code was accepted")
	}
}
