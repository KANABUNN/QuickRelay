package model

import "encoding/json"

// Structured values taken from the JMA telegram. They are optional additions to
// the existing Report fields and bulletin sections, which remain unchanged for
// older clients. Values are copied as published; nothing is estimated here.

// HypocenterAccuracy holds the JMA accuracy ranks (Hypocenter/Accuracy) as codes.
type HypocenterAccuracy struct {
	Epicenter          string `json:"epicenter,omitempty"`
	EpicenterSecondary string `json:"epicenter_secondary,omitempty"`
	Depth              string `json:"depth,omitempty"`
	Magnitude          string `json:"magnitude,omitempty"`
	// MagnitudePoints is the NumberOfMagnitudeCalculation code 1–5 (5 is five
	// or more), not a count; it is absent when unknown.
	MagnitudePoints *int `json:"magnitude_points,omitempty"`
}

// UnmarshalJSON drops magnitude point codes outside 1–5. Reports stored before
// the unknown code 0 was left out carry it, and the API must not serve it.
func (a *HypocenterAccuracy) UnmarshalJSON(data []byte) error {
	type plain HypocenterAccuracy
	if err := json.Unmarshal(data, (*plain)(a)); err != nil {
		return err
	}
	if p := a.MagnitudePoints; p != nil && (*p < 1 || *p > 5) {
		a.MagnitudePoints = nil
	}
	return nil
}

// IntensityRange is a forecast range (ForecastInt / ForecastLgInt). To may be
// "over" (From or more); Text is the display form used by the summary.
type IntensityRange struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Text string `json:"text,omitempty"`
}

// EEWChange is Forecast/Appendix: the change of the forecast maximum and its reason.
type EEWChange struct {
	MaxIntensity       string `json:"max_intensity,omitempty"`
	MaxLongPeriodClass string `json:"max_long_period_class,omitempty"`
	Reason             string `json:"reason,omitempty"`
	Description        string `json:"description,omitempty"`
}

// EEWZone is one target area of a warning listed in Head/Headline.
type EEWZone struct {
	// zone (地方予報区), prefecture (府県予報区) or region (細分区域).
	Level string `json:"level"`
	Name  string `json:"name"`
	Code  string `json:"code,omitempty"`
}

// EEWArea is one forecast area (Intensity/Forecast/Pref/Area).
type EEWArea struct {
	Prefecture              string          `json:"prefecture,omitempty"`
	PrefectureCode          string          `json:"prefecture_code,omitempty"`
	Name                    string          `json:"name"`
	Code                    string          `json:"code,omitempty"`
	Kind                    string          `json:"kind,omitempty"`
	KindCode                string          `json:"kind_code,omitempty"`
	Warning                 bool            `json:"is_warning"`
	ForecastIntensity       *IntensityRange `json:"forecast_intensity,omitempty"`
	ForecastLongPeriodClass *IntensityRange `json:"forecast_long_period_class,omitempty"`
	// Published expected arrival time of the principal motion; not a countdown.
	ArrivalTime *string `json:"arrival_time,omitempty"`
	Condition   string  `json:"condition,omitempty"`
}

// EEWDetail carries the EEW fields that the summary fields cannot represent.
// Warnings (VXSE43) never carry magnitude or depth here either.
type EEWDetail struct {
	Headline              string          `json:"headline,omitempty"`
	ForecastMaxIntensity  *IntensityRange `json:"forecast_max_intensity,omitempty"`
	ForecastMaxLongPeriod *IntensityRange `json:"forecast_max_long_period_class,omitempty"`
	Change                *EEWChange      `json:"change,omitempty"`
	WarningAreas          []EEWZone       `json:"warning_areas,omitempty"`
	Areas                 []EEWArea       `json:"areas,omitempty"`
	Comments              []string        `json:"comments,omitempty"`
}

// Measurement is a numeric value with its unit; Value is nil when unknown.
type Measurement struct {
	Value     *float64 `json:"value"`
	Unit      string   `json:"unit,omitempty"`
	Condition string   `json:"condition,omitempty"`
}

// LongPeriodBand is one period band of a long-period ground motion station (VXSE62).
type LongPeriodBand struct {
	Band            string   `json:"band"`
	PeriodUnit      string   `json:"period_unit,omitempty"`
	LongPeriodClass string   `json:"long_period_class,omitempty"`
	SVA             *float64 `json:"sva,omitempty"`
	SVAUnit         string   `json:"sva_unit,omitempty"`
}

// ObservedArea is one node of the observed intensity hierarchy.
type ObservedArea struct {
	// prefecture, region, city or station.
	Level           string `json:"level"`
	Name            string `json:"name"`
	Code            string `json:"code,omitempty"`
	Intensity       string `json:"intensity,omitempty"`
	LongPeriodClass string `json:"long_period_class,omitempty"`
	// For example 「震度５弱以上未入電」.
	Condition string `json:"condition,omitempty"`
	// 「上方修正」 or 「追加」.
	Revise   string           `json:"revise,omitempty"`
	SVA      *Measurement     `json:"sva,omitempty"`
	Periods  []LongPeriodBand `json:"periods,omitempty"`
	Children []ObservedArea   `json:"children,omitempty"`
}

// IntensityObservation is Intensity/Observation as a tree with codes.
type IntensityObservation struct {
	MaxIntensity       string         `json:"max_intensity,omitempty"`
	MaxLongPeriodClass string         `json:"max_long_period_class,omitempty"`
	LongPeriodCategory string         `json:"long_period_category,omitempty"`
	Areas              []ObservedArea `json:"areas,omitempty"`
}

// WithoutStructuredDetails returns the report as older versions serialized it.
// Duplicate detection hashes this form, so fingerprints of reports stored by
// earlier installations stay comparable after structured fields were added.
func (r Report) WithoutStructuredDetails() Report {
	r.TsunamiAreas = nil
	r.EEW = nil
	r.IntensityObservation = nil
	r.RelatedEarthquakes = nil
	if r.Hypocenter != nil {
		h := *r.Hypocenter
		h.Code, h.DetailedName, h.NameFromMark, h.Source, h.LandOrSea = "", "", "", "", ""
		h.CoordinateText, h.MagnitudeType, h.MagnitudeCondition, h.MagnitudeText = "", "", "", ""
		h.ArrivalTime, h.Accuracy = nil, nil
		r.Hypocenter = &h
	}
	return r
}
