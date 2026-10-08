package dmdata

import (
	"encoding/json"
	"quakerelay/server/internal/model"
)

func affectedAreas(data []byte, telegram string) []model.AffectedArea {
	var top object
	if json.Unmarshal(data, &top) != nil {
		return nil
	}
	body := obj(top["body"])
	out := []model.AffectedArea{}
	add := func(name, value string) {
		if name == "" {
			return
		}
		for i, a := range out {
			if a.Name == name {
				if model.IntensityRank(value) > model.IntensityRank(a.MaxIntensity) {
					out[i].MaxIntensity = value
				}
				return
			}
		}
		out = append(out, model.AffectedArea{Name: name, MaxIntensity: value})
	}
	var walk func([]any)
	walk = func(values []any) {
		for _, v := range values {
			a := obj(v)
			value := str(a["maxInt"])
			if value == "" {
				value = str(a["int"])
			}
			if f := obj(a["forecastMaxInt"]); len(f) > 0 {
				from, to := str(f["from"]), str(f["to"])
				if to == "over" {
					value = from + "以上"
				} else if to != "" {
					value = to
				} else {
					value = from
				}
			}
			add(str(a["name"]), intensity(value))
			for _, key := range []string{"prefectures", "regions", "areas", "cities"} {
				walk(items(a[key]))
			}
		}
	}
	walk(items(body["prefectures"]))
	walk(items(body["regions"]))
	if telegram == "VXSE43" {
		return out
	}
	in := obj(body["intensity"])
	for _, key := range []string{"prefectures", "regions", "areas"} {
		walk(items(in[key]))
	}
	tsunami := obj(body["tsunami"])
	for _, key := range []string{"forecasts", "observations", "estimations"} {
		walk(items(tsunami[key]))
	}
	return out
}
