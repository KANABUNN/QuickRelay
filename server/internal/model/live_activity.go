package model

import "time"

// Preserve the activity key already used by forecast-started activities.
// The actual telegram types still identify independent report streams.
func (r Report) LiveActivityKey() string {
	if r.TelegramType == "VXSE43" {
		return "VXSE45"
	}
	return r.TelegramType
}

func (r Report) SupportsLiveActivity() bool {
	return r.LiveActivityKey() == "VXSE45" || r.TelegramType == "VTSE41"
}
func (r Report) StartsLiveActivity() bool {
	return r.LiveActivityKey() == "VXSE45" && !r.Final && !r.Cancelled ||
		r.TelegramType == "VTSE41" && r.Warning && !r.Cancelled
}

func (r Report) LiveActivityDeadline() time.Time {
	deadline := r.ReportedAt.Add(r.TTL())
	if r.ValidUntil != nil && r.ValidUntil.Before(deadline) {
		return *r.ValidUntil
	}
	return deadline
}
