package eew

import (
	"strconv"
	"time"
)

// Outcomes of applying one telegram.
const (
	Applied     = "applied"
	Duplicate   = "duplicate"
	Stale       = "stale"
	NotApplied  = "not_applied"
	Ignored     = "ignored"
	Quarantined = "quarantined"
)

// MaxInputAge bounds how long after issue a live report may still change state;
// older inputs are history only. Historical verification is exempt.
const MaxInputAge = time.Hour

// issuedAt further ahead of reception than this is treated as a clock anomaly.
const maxClockAhead = time.Minute

// RelayState is the stream state QuickRelay committed for this report.
type RelayState struct {
	Serial    *int `json:"serial"`
	Cancelled bool `json:"is_cancelled"`
	Final     bool `json:"is_final"`
}

// Context describes how the telegram reached the bridge.
type Context struct {
	ReceivedAt time.Time
	Origin     string
	// Upstream DMDATA connection generation; a change may hide missed reports.
	Upstream string
	// Relay is nil when QuickRelay did not make this report current.
	Relay *RelayState
}

type Result struct {
	Outcome       string
	Reason        string
	ChangedSeries string
}

// Apply applies one telegram to an event. prev is nil for an unseen event.
// When the outcome is not Applied the returned state equals prev.
//
// Rules (VPS integration design §3): series are independent; issue reports are
// ordered by serial within their series; explicit cancellation is adopted only
// when QuickRelay committed it, whatever its serial; terminal series never
// reopen; regions are never removed and cancellation is never inferred.
func Apply(prev *State, t Telegram, c Context) (State, Result) {
	var s State
	if prev != nil {
		s = *prev
	} else {
		s = State{Event: Event{EventID: t.EventID, Environment: EnvironmentProduction}}
	}
	unchanged := s
	keep := func(outcome, reason string) (State, Result) {
		return unchanged, Result{Outcome: outcome, Reason: reason}
	}
	if t.Series != SeriesForecast && t.Series != SeriesWarning {
		return keep(Quarantined, "unknown_series")
	}
	if c.Origin != OriginHistorical && !t.IssuedAt.IsZero() && c.ReceivedAt.Sub(t.IssuedAt) > MaxInputAge {
		return keep(Ignored, "too_old")
	}
	status, revision := seriesStatus(s.Event, t.Series)
	meta := s.Meta.of(t.Series)
	switch t.InfoType {
	case InfoCancel:
		if status == StatusCanceled {
			return keep(Duplicate, "already_canceled")
		}
		if c.Relay == nil || !c.Relay.Cancelled {
			return keep(NotApplied, "cancel_not_committed_by_relay")
		}
		src := source(t, c)
		until := deadline(t, c, CancelSpeechLimit)
		if t.Series == SeriesForecast {
			s.Event.Forecast = &ForecastSeries{Status: StatusCanceled, Revision: revision + 1, Source: src, SpeechUntil: until}
		} else {
			s.Event.Warning = &WarningSeries{Status: StatusCanceled, Revision: revision + 1, Source: src, SpeechUntil: until}
		}
		meta.Upstream = c.Upstream
	case InfoCorrection:
		return keep(Quarantined, "correction_unsupported")
	case InfoIssue:
		if t.Serial == nil {
			return keep(Quarantined, "missing_serial")
		}
		if meta.Serial != nil {
			switch {
			case *t.Serial < *meta.Serial:
				return keep(Stale, "older_serial")
			case *t.Serial == *meta.Serial && t.ContentHash == meta.ContentHash:
				return keep(Duplicate, "same_serial")
			case *t.Serial == *meta.Serial:
				return keep(Quarantined, "same_serial_conflict")
			}
		}
		switch status {
		case StatusCanceled:
			// A late report numbered at or below the cancellation is history, not a contradiction.
			if cs := canceledSerial(s.Event, t.Series); cs != nil && *t.Serial <= *cs {
				return keep(Stale, "before_cancel")
			}
			return keep(Quarantined, "after_cancel")
		case StatusFinal:
			return keep(Quarantined, "after_final")
		}
		if t.Series == SeriesForecast {
			if t.Forecast == nil {
				return keep(Quarantined, "missing_data")
			}
			next := &ForecastSeries{Status: StatusActive, Revision: revision + 1, Source: source(t, c), Data: t.Forecast,
				SpeechUntil: deadline(t, c, ForecastSpeechLimit)}
			if t.Final {
				// A final forecast offers no speech; it never ends the warning or the event.
				next.Status, next.SpeechUntil = StatusFinal, nil
			}
			s.Event.Forecast = next
		} else {
			if t.Warning == nil {
				return keep(Quarantined, "missing_data")
			}
			s.Event.Warning = applyWarning(s.Event.Warning, meta, t, c, revision+1)
		}
		meta = SeriesMeta{Serial: t.Serial, ContentHash: t.ContentHash, Upstream: c.Upstream}
	default:
		return keep(Quarantined, "unsupported_info_type")
	}
	s.Event.Revision++
	s.Meta.set(t.Series, meta)
	return s, Result{Outcome: Applied, ChangedSeries: t.Series}
}

// applyWarning accumulates target regions. A region keeps the source and speech
// deadline of the report that added it. A region already warned in a report
// that was not seen makes the accumulation partial, as does a change of the
// upstream connection between reports; partial never reverts.
func applyWarning(previous *WarningSeries, meta SeriesMeta, t Telegram, c Context, revision int64) *WarningSeries {
	src := source(t, c)
	regionUntil := deadline(t, c, WarningSpeechLimit)
	coverage := CoverageComplete
	var active []ActiveRegion
	sources := map[string]Source{}
	if previous != nil && previous.Data != nil {
		prev := previous.Data
		if prev.Coverage == CoveragePartial {
			coverage = CoveragePartial
		}
		active = append(active, prev.ActiveRegions...)
		for _, s := range prev.RegionSources {
			sources[s.TelegramID] = s
		}
		sources[previous.Source.TelegramID] = previous.Source
	}
	if previous != nil && meta.Upstream != c.Upstream {
		coverage = CoveragePartial
	}
	known := map[string]bool{}
	for _, r := range active {
		known[r.key()] = true
	}
	data := &WarningData{ReportRegions: []Region{}, AddedRegions: []Region{}, RegionSources: []Source{}}
	for _, r := range t.Warning.Regions {
		data.ReportRegions = append(data.ReportRegions, r.Region)
		if known[r.key()] {
			continue
		}
		known[r.key()] = true
		if r.Added() {
			ref := t.TelegramID
			active = append(active, ActiveRegion{Region: r.Region, SourceRef: &ref, SpeechUntil: regionUntil})
			data.AddedRegions = append(data.AddedRegions, r.Region)
			continue
		}
		// Warned by a report this bridge did not apply.
		active = append(active, ActiveRegion{Region: r.Region})
		coverage = CoveragePartial
	}
	listed := map[string]bool{t.TelegramID: true}
	for _, r := range active {
		if r.SourceRef == nil || listed[*r.SourceRef] {
			continue
		}
		listed[*r.SourceRef] = true
		if s, ok := sources[*r.SourceRef]; ok {
			data.RegionSources = append(data.RegionSources, s)
		}
	}
	data.ActiveRegions = active
	if data.ActiveRegions == nil {
		data.ActiveRegions = []ActiveRegion{}
	}
	data.Coverage = coverage
	return &WarningSeries{Status: StatusActive, Revision: revision, Source: src, Data: data, SpeechUntil: deadline(t, c, WarningSpeechLimit)}
}

func seriesStatus(e Event, series string) (string, int64) {
	if series == SeriesForecast && e.Forecast != nil {
		return e.Forecast.Status, e.Forecast.Revision
	}
	if series == SeriesWarning && e.Warning != nil {
		return e.Warning.Status, e.Warning.Revision
	}
	return "", 0
}

func canceledSerial(e Event, series string) *int {
	var text *string
	if series == SeriesForecast && e.Forecast != nil {
		text = e.Forecast.Source.Serial
	}
	if series == SeriesWarning && e.Warning != nil {
		text = e.Warning.Source.Serial
	}
	if text == nil {
		return nil
	}
	n, err := strconv.Atoi(*text)
	if err != nil {
		return nil
	}
	return &n
}

func (m Meta) of(series string) SeriesMeta {
	if series == SeriesForecast {
		return m.Forecast
	}
	return m.Warning
}
func (m *Meta) set(series string, v SeriesMeta) {
	if series == SeriesForecast {
		m.Forecast = v
	} else {
		m.Warning = v
	}
}

func source(t Telegram, c Context) Source {
	return Source{Issuer: "JMA", Provider: "DMDATA", Code: t.Code, TelegramID: t.TelegramID, Serial: serialText(t.Serial),
		InfoType: t.InfoType, IssuedAt: stamp(t.IssuedAt), ReceivedAt: stamp(c.ReceivedAt), ControlAt: stamp(t.ControlAt), Origin: c.Origin}
}

// deadline is the speech start limit from the issue time; null when the issue
// time is missing or implausibly ahead of reception.
func deadline(t Telegram, c Context, limit time.Duration) *string {
	if t.IssuedAt.IsZero() || !c.ReceivedAt.IsZero() && t.IssuedAt.Sub(c.ReceivedAt) > maxClockAhead {
		return nil
	}
	return stamp(t.IssuedAt.Add(limit))
}
