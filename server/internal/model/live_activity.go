package model

func (r Report) SupportsLiveActivity() bool {
	return r.TelegramType == "VXSE45" || r.TelegramType == "VTSE41"
}
func (r Report) StartsLiveActivity() bool {
	return r.TelegramType == "VXSE45" && !r.Final && !r.Cancelled ||
		r.TelegramType == "VTSE41" && r.Warning && !r.Cancelled
}
