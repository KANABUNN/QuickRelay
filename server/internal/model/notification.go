package model

import (
	"slices"
	"strings"
)

type AffectedArea struct {
	Name         string `json:"name"`
	MaxIntensity string `json:"max_intensity,omitempty"`
}

// Use the upper bound of a range; missing or open bounds cannot be treated
// as a fixed low intensity that would silently discard a relevant alert.
func IntensityRank(s string) int {
	s = strings.NewReplacer("5弱", "5-", "5強", "5+", "6弱", "6-", "6強", "6+").Replace(s)
	if strings.Contains(s, "以上") || strings.Contains(s, "over") {
		return 9
	}
	ranks := map[string]int{"0": 0, "1": 1, "2": 2, "3": 3, "4": 4, "5-": 5, "5+": 6, "6-": 7, "6+": 8, "7": 9}
	best := -1
	for _, v := range strings.FieldsFunc(s, func(r rune) bool { return r == '〜' || r == '～' || r == '~' || r == '/' }) {
		if rank, ok := ranks[v]; ok && rank > best {
			best = rank
		}
	}
	return best
}

func (r Report) EndsNotificationLifecycle() bool {
	return r.Cancelled || r.IsEEW() && r.Final ||
		r.TelegramType == "VTSE41" && !r.Warning
}

// Empty selectors retain all regions. Missing regional or intensity data
// is kept rather than silently discarded. Advisories have separate switches.
func (p Preferences) Allows(r Report, following bool) bool {
	if !p.NotificationsEnabled {
		return false
	}
	if following && r.EndsNotificationLifecycle() {
		return true
	}
	if !slices.Contains(p.EventTypes, r.EventType) {
		return false
	}
	selected := p.EarthquakeRegions
	if r.CategoryName() == "tsunami" {
		selected = p.TsunamiRegions
	}
	applies := r.CategoryName() == "earthquake" || r.CategoryName() == "tsunami"
	intensity := ""
	if r.MaxIntensity != nil {
		intensity = *r.MaxIntensity
	}
	if applies && len(selected) > 0 && len(r.AffectedAreas) > 0 {
		matches := []AffectedArea{}
		for _, area := range r.AffectedAreas {
			for _, name := range selected {
				if area.Name == name || strings.HasPrefix(area.Name, name) {
					matches = append(matches, area)
					break
				}
			}
		}
		if len(matches) == 0 {
			// Forecast XML omits regions below intensity 4. Their absence
			// cannot prove that a watched low-threshold region is unaffected.
			if r.TelegramType == "VXSE45" && (p.MinimumIntensity == "" || IntensityRank(p.MinimumIntensity) < 4) {
				return true
			}
			return false
		}
		rank := -1
		unknown := false
		for _, area := range matches {
			n := IntensityRank(area.MaxIntensity)
			if n < 0 {
				unknown = true
			}
			if n > rank {
				rank = n
				intensity = area.MaxIntensity
			}
		}
		if unknown {
			intensity = ""
		}
	}
	if r.CategoryName() == "earthquake" && r.EventType != "eew_warning" && p.MinimumIntensity != "" {
		n, min := IntensityRank(intensity), IntensityRank(p.MinimumIntensity)
		if n >= 0 && min >= 0 && n < min {
			return false
		}
	}
	return true
}
