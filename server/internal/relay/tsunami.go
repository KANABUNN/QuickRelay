package relay

import (
	"cmp"
	"slices"
	"strings"

	"quakerelay/server/internal/model"
)

func tsunamiWarningMatches(report model.Report, p model.Preferences) bool {
	return report.TelegramType != "VTSE41" || !report.Warning || len(report.TsunamiForecastAreas()) == 0 ||
		len(report.TsunamiWarningAreas(p.TsunamiRegions)) > 0
}

func tsunamiReleaseAlert(report model.Report, releases []model.TsunamiArea) model.Report {
	report.EventType = "tsunami_warning"
	report.Warning = false
	report.Title = "津波警報・注意報の解除"
	report.Body = tsunamiReleaseText(releases)
	return report
}

func tsunamiReleaseText(releases []model.TsunamiArea) string {
	names := make([]string, 0, len(releases))
	for _, area := range releases {
		names = append(names, area.Name)
	}
	return strings.Join(names, "、") + "の津波警報・注意報が解除されました。"
}

func tsunamiLiveDisplay(report model.Report, p model.Preferences) (model.Report, bool) {
	if report.Cancelled || len(report.TsunamiForecastAreas()) == 0 {
		return report, report.EndsNotificationLifecycle() || !p.Allows(report, true)
	}
	active := report.TsunamiWarningAreas(p.TsunamiRegions)
	if len(active) == 0 {
		if report.Warning {
			report.Title = "津波警報・注意報"
			report.Body = "受信した最新発表では、設定した地域に津波警報・注意報は出ていません。"
		}
		report.Warning = false
		return report, true
	}
	slices.SortStableFunc(active, func(a, b model.TsunamiArea) int { return cmp.Compare(b.WarningRank(), a.WarningRank()) })
	labels := []string{"", "津波注意報", "津波警報", "大津波警報"}
	report.Title = labels[active[0].WarningRank()]
	areas := make([]string, 0, len(active))
	for _, area := range active {
		areas = append(areas, area.Name+"（"+labels[area.WarningRank()]+"）")
	}
	report.Body = strings.Join(areas, "、")
	return report, !p.Allows(report, false)
}
