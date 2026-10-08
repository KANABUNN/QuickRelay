package dmdata

import (
	"quakerelay/server/internal/model"
	"testing"
)

func TestAffectedAreasStayInTheirSourceScope(t *testing.T) {
	data := []byte(`{"body":{"prefectures":[{"name":"石川県"}],"intensity":{"regions":[
        {"name":"石川県能登","forecastMaxInt":{"from":"5-","to":"over"}},
        {"name":"東京都２３区","forecastMaxInt":{"from":"4","to":"4"}}]},
        "tsunami":{"forecasts":[{"name":"宮崎県"}]}}}`)
	forecast := affectedAreas(data, "VXSE45")
	if len(forecast) != 4 || model.IntensityRank(forecast[1].MaxIntensity) != 9 {
		t.Fatal(forecast)
	}
	warning := affectedAreas(data, "VXSE43")
	if len(warning) != 1 || warning[0].Name != "石川県" {
		t.Fatal("warning included forecast-only target", warning)
	}
}
