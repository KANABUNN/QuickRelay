// Package eew is the EEW bridge's view of JMA earthquake early warnings:
// VXSE45 (forecast) and VXSE43 (warning) as two independent series of one
// event, and the state machine that applies reports to them. It does no I/O.
//
// JSON tags are the WSS contract (schemaVersion 1); revisions are decimal
// strings and times are UTC RFC 3339.
package eew

import (
	"strconv"
	"time"
)

const (
	SeriesForecast = "forecast"
	SeriesWarning  = "warning"

	StatusActive   = "active"
	StatusFinal    = "final" // forecast only
	StatusCanceled = "canceled"

	InfoIssue      = "issue"
	InfoCorrection = "correction"
	InfoCancel     = "cancel"

	OriginLive       = "live"
	OriginRecovered  = "recovered"
	OriginHistorical = "historical"

	CoverageComplete = "complete"
	CoveragePartial  = "partial"

	EnvironmentProduction = "production"

	LevelZone       = "zone"       // 地方予報区
	LevelPrefecture = "prefecture" // 府県予報区
	LevelRegion     = "region"     // 細分区域
)

// Speech may start only until these limits after the report's issue time.
const (
	ForecastSpeechLimit = 10 * time.Second
	WarningSpeechLimit  = 15 * time.Second
	CancelSpeechLimit   = 30 * time.Second
)

// Intensity is a forecast seismic intensity or long-period ground motion class.
// Values are JMA codes ("0"–"4", "5-", "5+", "6-", "6+", "7").
type Intensity struct {
	Kind  string `json:"kind"` // exact | range | lowerBound | unknown
	Value string `json:"value,omitempty"`
	Min   string `json:"min,omitempty"`
	Max   string `json:"max,omitempty"`
}

var (
	IntensityCodes  = map[string]int{"0": 0, "1": 1, "2": 2, "3": 3, "4": 4, "5-": 5, "5+": 6, "6-": 7, "6+": 8, "7": 9}
	LongPeriodCodes = map[string]int{"0": 0, "1": 1, "2": 2, "3": 3, "4": 4}
)

// RangeOf maps a JMA From/To pair. "over" in To means From or more; 不明 or an
// unrecognized code is unknown, never zero.
func RangeOf(from, to string, codes map[string]int) Intensity {
	_, fromOK := codes[from]
	_, toOK := codes[to]
	switch {
	case !fromOK:
		return Intensity{Kind: "unknown"}
	case to == "over":
		return Intensity{Kind: "lowerBound", Min: from}
	case to == "" || to == from:
		return Intensity{Kind: "exact", Value: from}
	case !toOK:
		return Intensity{Kind: "unknown"}
	default:
		return Intensity{Kind: "range", Min: from, Max: to}
	}
}

// Telegram is one parsed VXSE45 or VXSE43 report.
type Telegram struct {
	Code       string // VXSE45 or VXSE43
	Series     string
	TelegramID string // DMDATA message id
	EventID    string // Head/EventID
	Serial     *int
	InfoType   string // issue | correction | cancel
	IssuedAt   time.Time
	ControlAt  time.Time
	// Final is set by the VXSE45 final-report advisory.
	Final    bool
	Forecast *ForecastData  // VXSE45, not for cancellations
	Warning  *WarningReport // VXSE43, not for cancellations
	// ContentHash identifies the telegram body; equal IDs with other bodies conflict.
	ContentHash string
}

type HypocenterQuality struct {
	// estimated, assumed (仮定震源要素・レベル法) or lowAccuracy (1点観測など).
	Kind            string `json:"kind"`
	EpicenterRank   string `json:"epicenterRank,omitempty"`
	EpicenterRank2  string `json:"epicenterRank2,omitempty"`
	DepthRank       string `json:"depthRank,omitempty"`
	MagnitudeRank   string `json:"magnitudeRank,omitempty"`
	MagnitudePoints *int   `json:"magnitudePoints,omitempty"`
}

type Hypocenter struct {
	Code      string            `json:"code"`
	Name      string            `json:"name"`
	Latitude  *float64          `json:"latitude"`
	Longitude *float64          `json:"longitude"`
	Quality   HypocenterQuality `json:"quality"`
}

// Arrival keeps the published time apart from its meaning; no countdown is derived.
type Arrival struct {
	Time *string `json:"time"`
	// predicted, arrived (既に主要動到達と推測), noPrediction (PLUM法) or unknown.
	Condition string `json:"condition"`
	Text      string `json:"text,omitempty"`
}

type RegionForecast struct {
	CodeType           string     `json:"codeType"`
	Code               string     `json:"code"`
	Name               string     `json:"name"`
	PrefectureCode     string     `json:"prefectureCode,omitempty"`
	PrefectureName     string     `json:"prefectureName,omitempty"`
	KindCode           string     `json:"kindCode,omitempty"`
	KindName           string     `json:"kindName,omitempty"`
	IsWarning          bool       `json:"isWarning"`
	MaxIntensity       Intensity  `json:"maxIntensity"`
	MaxLongPeriodClass *Intensity `json:"maxLongPeriodClass"`
	Arrival            Arrival    `json:"arrival"`
}

// ForecastData is the forecast series value. For an assumed hypocenter the
// origin time, magnitude and depth are placeholders and are left null.
type ForecastData struct {
	OriginTime         *string          `json:"originTime"`
	Hypocenter         *Hypocenter      `json:"hypocenter"`
	Magnitude          *float64         `json:"magnitude"`
	DepthKm            *int             `json:"depthKm"`
	MaxIntensity       Intensity        `json:"maxIntensity"`
	MaxLongPeriodClass *Intensity       `json:"maxLongPeriodClass"`
	RegionForecasts    []RegionForecast `json:"regionForecasts"`
}

// Region is a warning target area, keyed by (codeType, code).
type Region struct {
	CodeType string `json:"codeType"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Level    string `json:"level"`
}

func (r Region) key() string { return r.CodeType + "\x00" + r.Code }

// ReportRegion is a region listed in one VXSE43 report with its previous kind:
// LastKind "00" marks a region added by this report, "31" one already warned.
type ReportRegion struct {
	Region
	LastKind string
}

func (r ReportRegion) Added() bool { return r.LastKind == "00" }

type WarningReport struct {
	Regions []ReportRegion
}

// ActiveRegion is a region warned so far in the event. SourceRef names the
// telegram that added it; both fields are null when that report was not seen.
type ActiveRegion struct {
	Region
	SourceRef   *string `json:"sourceRef"`
	SpeechUntil *string `json:"speechUntil"`
}

type WarningData struct {
	ReportRegions []Region       `json:"reportRegions"`
	AddedRegions  []Region       `json:"addedRegions"`
	ActiveRegions []ActiveRegion `json:"activeRegions"`
	// Sources of earlier reports that ActiveRegions refer to.
	RegionSources []Source `json:"regionSources"`
	Coverage      string   `json:"coverage"`
}

type Source struct {
	Issuer     string  `json:"issuer"`
	Provider   string  `json:"provider"`
	Code       string  `json:"code"`
	TelegramID string  `json:"telegramId"`
	Serial     *string `json:"serial"`
	InfoType   string  `json:"infoType"`
	IssuedAt   *string `json:"issuedAt"`
	// Null until a transmission time with a confirmed meaning is available.
	SentAt     *string `json:"sentAt"`
	ReceivedAt *string `json:"receivedAt"`
	ControlAt  *string `json:"controlAt"`
	Origin     string  `json:"origin"`
}

type ForecastSeries struct {
	Status      string        `json:"status"`
	Revision    int64         `json:"revision,string"`
	Source      Source        `json:"source"`
	Data        *ForecastData `json:"data"`
	SpeechUntil *string       `json:"speechUntil"`
}

type WarningSeries struct {
	Status      string       `json:"status"`
	Revision    int64        `json:"revision,string"`
	Source      Source       `json:"source"`
	Data        *WarningData `json:"data"`
	SpeechUntil *string      `json:"speechUntil"`
}

// Event is the full state delivered for one JMA EventID. Series not yet
// received are null.
type Event struct {
	EventID     string          `json:"eventId"`
	Environment string          `json:"environment"`
	Revision    int64           `json:"revision,string"`
	Forecast    *ForecastSeries `json:"forecast"`
	Warning     *WarningSeries  `json:"warning"`
}

// Terminal reports whether every received series has ended. Warnings end only
// by cancellation.
func (e Event) Terminal() bool {
	if e.Forecast == nil && e.Warning == nil {
		return false
	}
	return (e.Forecast == nil || e.Forecast.Status != StatusActive) && (e.Warning == nil || e.Warning.Status == StatusCanceled)
}

// SeriesMeta is bookkeeping kept beside the delivered state.
type SeriesMeta struct {
	// Serial and ContentHash of the last applied issue report.
	Serial      *int   `json:"serial,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
	// Upstream connection generation of the last applied report.
	Upstream string `json:"upstream,omitempty"`
}

type Meta struct {
	Forecast SeriesMeta `json:"forecast"`
	Warning  SeriesMeta `json:"warning"`
}

type State struct {
	Event Event
	Meta  Meta
}

func stamp(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	v := t.UTC().Format(time.RFC3339Nano)
	return &v
}

func serialText(n *int) *string {
	if n == nil {
		return nil
	}
	v := strconv.Itoa(*n)
	return &v
}
