package model

import (
	"slices"
	"strconv"
	"strings"
)

const observationSection = "津波観測："
const estimationSection = "沿岸の推定："

func (r Report) IsTsunamiObservation() bool {
	return r.TelegramType == "VTSE52" ||
		r.TelegramType == "VTSE51" && r.Bulletin != nil &&
			slices.ContainsFunc(r.Bulletin.Sections, func(s BulletinSection) bool {
				return strings.HasPrefix(s.Title, observationSection)
			})
}

type tsunamiWaveSummary struct {
	name, area, first, maximum string
}

func (r Report) tsunamiWaveSummaries() []tsunamiWaveSummary {
	return r.tsunamiWaves(observationSection, "第1波", "最大波")
}

func (r Report) tsunamiWaves(prefix, firstLabel, maxLabel string) []tsunamiWaveSummary {
	if r.Bulletin == nil {
		return nil
	}
	var summaries []tsunamiWaveSummary
	for _, section := range r.Bulletin.Sections {
		name, ok := strings.CutPrefix(section.Title, prefix)
		if !ok || name == "" {
			continue
		}
		s := tsunamiWaveSummary{name: name, area: name}
		if prefix == observationSection {
			s.area = ""
		}
		for _, row := range section.Rows {
			switch row.Label {
			case "地域":
				s.area = row.Value
			case firstLabel:
				s.first = row.Value
			case maxLabel:
				s.maximum = row.Value
			}
		}
		if s.first != "" || s.maximum != "" {
			summaries = append(summaries, s)
		}
	}
	return summaries
}

func (s tsunamiWaveSummary) priority() (int, float64) {
	maximum := strings.Split(s.maximum, " / ")
	first := strings.Split(s.first, " / ")
	// Heights are already qualified and formatted by the bulletin parser.
	// Read the numeric portion only to choose which station to feature.
	var height float64
	for _, part := range maximum {
		value, unit, ok := strings.Cut(part, "m")
		if ok && (unit == "" || unit == "以上") {
			height, _ = strconv.ParseFloat(value, 64)
		}
	}
	switch {
	case strings.Contains(s.maximum, "上昇中"):
		return 5, height
	case strings.Contains(s.maximum, "重要") || tsunamiWaveUpdated(s.maximum):
		return 4, height
	case tsunamiWaveUpdated(s.first) || strings.Contains(s.first, " JST") || strings.Contains(s.first, "到達を確認") ||
		slices.Contains(first, "押し") || slices.Contains(first, "引き"):
		return 3, height
	case height > 0:
		return 2, height
	case s.maximum != "" && !strings.Contains(s.maximum, "欠測"), s.first != "" && !strings.Contains(s.first, "欠測"):
		return 1, 0
	default:
		return 0, 0
	}
}

func tsunamiWaveUpdated(wave string) bool {
	flags := strings.Split(wave, " / ")
	flag := flags[len(flags)-1]
	return flag == "更新" || flag == "追加"
}

func bestTsunamiWave(summaries []tsunamiWaveSummary, regions []string) string {
	var best tsunamiWaveSummary
	bestRank := -1
	var bestHeight float64
	for _, s := range summaries {
		if len(regions) > 0 && !matchesRegion(s.area, regions) {
			continue
		}
		rank, height := s.priority()
		if rank > bestRank || rank == bestRank && height > bestHeight {
			best, bestRank, bestHeight = s, rank, height
		}
	}
	if bestRank < 0 {
		return ""
	}
	first, maximum := best.first, best.maximum
	if first != "" {
		first = "第1波 " + first
	}
	if maximum != "" {
		maximum = "最大波 " + maximum
	}
	if first != "" && maximum != "" {
		return best.name + " " + first + "・" + maximum
	}
	return best.name + " " + first + maximum
}

func (r Report) TsunamiObservationSummary(regions []string) string {
	stations := r.tsunamiWaveSummaries()
	if r.TelegramType == "VTSE51" {
		if len(regions) > 0 && !slices.ContainsFunc(stations, func(s tsunamiWaveSummary) bool { return s.area != "" }) {
			regions = nil
		}
		return bestTsunamiWave(stations, regions)
	}

	// Offshore stations have no forecast-zone assignment. Coastal estimates
	// are published separately and can be matched to the selected zones.
	observed := bestTsunamiWave(stations, nil)
	if observed != "" && len(regions) == 0 {
		return observed
	}
	estimate := bestTsunamiWave(r.tsunamiWaves(estimationSection, "第1波の推定", "最大波の推定"), regions)
	if estimate == "" {
		return observed
	}
	estimate = "沿岸の推定 " + estimate
	if observed != "" {
		return observed + "\n" + estimate
	}
	return estimate
}
