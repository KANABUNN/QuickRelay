package model

import "strings"

type TsunamiArea struct {
	Code     string `json:"code,omitempty"`
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`
	KindName string `json:"kind_name,omitempty"`
	LastKind string `json:"last_kind,omitempty"`
}

func (a TsunamiArea) Warning() bool {
	return a.WarningRank() > 0
}

func (a TsunamiArea) WarningRank() int {
	switch a.Kind {
	case "52", "53":
		return 3
	case "51":
		return 2
	case "62":
		return 1
	case "":
		if strings.Contains(a.KindName, "解除") {
			return 0
		}
		switch {
		case strings.Contains(a.KindName, "大津波警報"):
			return 3
		case strings.Contains(a.KindName, "津波警報"):
			return 2
		case strings.Contains(a.KindName, "津波注意報"):
			return 1
		}
	}
	return 0
}

func (a TsunamiArea) Released() bool {
	return a.Kind == "50" || a.Kind == "60" ||
		a.Kind == "" && strings.Contains(a.KindName, "解除") ||
		(a.Kind == "71" || a.Kind == "72" || a.Kind == "73") && (TsunamiArea{Kind: a.LastKind}).Warning()
}

func (a TsunamiArea) Matches(other TsunamiArea) bool {
	if a.Code != "" && other.Code != "" {
		return a.Code == other.Code
	}
	return a.Name == other.Name
}

func (a TsunamiArea) InRegions(regions []string) bool {
	if len(regions) == 0 {
		return true
	}
	for _, region := range regions {
		if strings.HasPrefix(a.Name, region) {
			return true
		}
	}
	return false
}

// Bulletin rows retain the forecast-zone name and status for reports stored
// before forecast-zone codes were recorded.
func (r Report) TsunamiForecastAreas() []TsunamiArea {
	if len(r.TsunamiAreas) > 0 || r.Bulletin == nil || r.TelegramType != "VTSE41" {
		return r.TsunamiAreas
	}
	var areas []TsunamiArea
	for _, section := range r.Bulletin.Sections {
		name, ok := strings.CutPrefix(section.Title, "津波予報区：")
		if !ok || name == "" {
			continue
		}
		for _, row := range section.Rows {
			if row.Label == "発表" {
				areas = append(areas, TsunamiArea{Name: name, KindName: row.Value})
				break
			}
		}
	}
	return areas
}

func (r Report) TsunamiWarningAreas(regions []string) []TsunamiArea {
	var active []TsunamiArea
	for _, area := range r.TsunamiForecastAreas() {
		if area.Warning() && area.InRegions(regions) {
			active = append(active, area)
		}
	}
	return active
}
