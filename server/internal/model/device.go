package model

type Preferences struct {
	NotificationsEnabled  bool     `json:"notifications_enabled"`
	TimeSensitiveEnabled  bool     `json:"time_sensitive_enabled"`
	CustomSoundEnabled    bool     `json:"custom_sound_enabled"`
	EventTypes            []string `json:"event_types"`
	EarthquakeRegions     []string `json:"earthquake_regions,omitempty"`
	TsunamiRegions        []string `json:"tsunami_regions,omitempty"`
	MinimumIntensity      string   `json:"minimum_intensity,omitempty"`
	LiveActivitiesEnabled bool     `json:"live_activities_enabled"`
}

func DefaultPreferences() Preferences {
	return Preferences{NotificationsEnabled: true, TimeSensitiveEnabled: true, CustomSoundEnabled: true, EventTypes: []string{"eew_forecast", "eew_warning", "eew_cancel", "earthquake_info", "earthquake_update", "tsunami_warning", "tsunami_info", "nankai_info", "seismic_advisory", "earthquake_data", "system_test"}}
}

type Device struct {
	InstallationID string      `json:"installation_id"`
	DeviceName     string      `json:"device_name"`
	Environment    string      `json:"environment"`
	AppVersion     string      `json:"app_version"`
	OSVersion      string      `json:"os_version"`
	Active         bool        `json:"active"`
	Preferences    Preferences `json:"preferences"`
	LastSeenAt     string      `json:"last_seen_at"`
	Token          string      `json:"-"`
	TokenUpdatedMS int64       `json:"-"`
	PushActive     bool        `json:"-"`
}
type Registration struct {
	InstallationID string       `json:"installation_id"`
	DeviceToken    string       `json:"device_token"`
	Environment    string       `json:"environment"`
	AppVersion     string       `json:"app_version"`
	OSVersion      string       `json:"os_version"`
	DeviceName     string       `json:"device_name"`
	Preferences    *Preferences `json:"preferences"`
}
