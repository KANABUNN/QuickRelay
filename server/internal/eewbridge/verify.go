package eewbridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"quakerelay/server/internal/eew"
	"quakerelay/server/internal/model"
)

// VerifyReport compares the bridge with the relay over the EEW reports of a
// copied relay database, without audio or delivery.
type VerifyReport struct {
	Reports  int            `json:"reports"`
	Outcomes map[string]int `json:"outcomes"`
	Series   int            `json:"series"`
	// Final series state that differs between the bridge and the relay's ordering.
	Mismatches []Mismatch `json:"mismatches"`
	// Reports the relay made current but the bridge did not apply, or the reverse.
	Disagreements []Disagreement `json:"disagreements"`
	// Streams stored by the relay that today's relay ordering would decide
	// differently, for example cancellations stored before the lower-serial fix.
	StoredDrift     []Mismatch    `json:"stored_drift"`
	Quarantined     []Quarantined `json:"quarantined"`
	PartialWarnings int           `json:"partial_warnings"`
}

type Mismatch struct {
	EventID string `json:"event_id"`
	Series  string `json:"series"`
	Relay   string `json:"relay"`
	Bridge  string `json:"bridge"`
}

type Disagreement struct {
	ReportSequence int64  `json:"report_sequence"`
	TelegramID     string `json:"telegram_id"`
	EventID        string `json:"event_id"`
	RelayCurrent   bool   `json:"relay_current"`
	Outcome        string `json:"outcome"`
	Reason         string `json:"reason,omitempty"`
}

type Quarantined struct {
	TelegramID string `json:"telegram_id"`
	Code       string `json:"code"`
	EventID    string `json:"event_id"`
	Serial     *int   `json:"serial"`
	Reason     string `json:"reason"`
}

// Clean reports whether the bridge and the relay agree.
func (r VerifyReport) Clean() bool { return len(r.Mismatches) == 0 && len(r.Disagreements) == 0 }

const verifyListLimit = 100

// OpenRelayCopy opens a copied relay database read-only and immutable. Use a
// backup copy: an immutable reader must not share a database being written.
func OpenRelayCopy(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: "mode=ro&immutable=1"}
	if filepath.VolumeName(abs) != "" {
		u.Path = "/" + u.Path
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	var n int
	if err = db.QueryRow("SELECT count(*) FROM reports").Scan(&n); err != nil {
		db.Close()
		return nil, fmt.Errorf("not a relay database: %w", err)
	}
	return db, nil
}

func relayStatus(r model.Report) string {
	switch {
	case r.Cancelled:
		return eew.StatusCanceled
	case r.Final && r.TelegramType == "VXSE45":
		return eew.StatusFinal
	}
	return eew.StatusActive
}

func describe(status string, serial *int) string {
	if serial == nil {
		return status + "#-"
	}
	return status + "#" + strconv.Itoa(*serial)
}

func sourceEventID(r model.Report) string {
	if r.SourceEventID != "" {
		return r.SourceEventID
	}
	return r.EventID
}

// Verify replays the VXSE43/VXSE45 reports of relay, in commit order, into an
// empty bridge store as historical input. The relay's decision for each report
// is recomputed with today's ordering (Report.NewerThan), as at ingest.
func Verify(ctx context.Context, relay *sql.DB, bridge *Store) (VerifyReport, error) {
	out := VerifyReport{Outcomes: map[string]int{}, Mismatches: []Mismatch{}, Disagreements: []Disagreement{}, StoredDrift: []Mismatch{}, Quarantined: []Quarantined{}}
	var used int
	if err := bridge.DB.QueryRowContext(ctx, "SELECT count(*) FROM inputs").Scan(&used); err != nil || used != 0 {
		return out, errors.New("verification needs an empty bridge database")
	}
	rows, err := relay.QueryContext(ctx, "SELECT sequence,payload,raw,received_ms FROM reports WHERE telegram_type IN ('VXSE43','VXSE45') ORDER BY sequence")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	latest := map[string]model.Report{}
	var order []string
	for rows.Next() {
		var sequence, received int64
		var payload string
		var raw []byte
		if err = rows.Scan(&sequence, &payload, &raw, &received); err != nil {
			return out, err
		}
		var r model.Report
		if err = json.Unmarshal([]byte(payload), &r); err != nil {
			return out, fmt.Errorf("report %d payload: %w", sequence, err)
		}
		key := r.EventID + "\x00" + r.TelegramType
		old, seen := latest[key]
		current := !seen || r.NewerThan(old)
		var state *eew.RelayState
		if current {
			if !seen {
				order = append(order, key)
			}
			latest[key] = r
			state = &eew.RelayState{Serial: r.Serial, Cancelled: r.Cancelled, Final: r.Final}
		}
		result, err := bridge.Process(ctx, Input{Frame: raw, ReceivedAt: time.UnixMilli(received).UTC(), Origin: eew.OriginHistorical, Relay: state}, time.Now())
		if err != nil {
			return out, fmt.Errorf("report %d: %w", sequence, err)
		}
		out.Reports++
		label := result.Outcome
		if result.Reason != "" {
			label += ":" + result.Reason
		}
		out.Outcomes[label]++
		if (result.Outcome == eew.Applied) != current && len(out.Disagreements) < verifyListLimit {
			out.Disagreements = append(out.Disagreements, Disagreement{ReportSequence: sequence, TelegramID: result.TelegramID,
				EventID: sourceEventID(r), RelayCurrent: current, Outcome: result.Outcome, Reason: result.Reason})
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	for _, key := range order {
		r := latest[key]
		series := eew.SeriesForecast
		if r.TelegramType == "VXSE43" {
			series = eew.SeriesWarning
		}
		out.Series++
		want := describe(relayStatus(r), r.Serial)
		got := "missing"
		state, err := bridge.State(ctx, eew.EnvironmentProduction, sourceEventID(r))
		if err != nil {
			return out, err
		}
		if state != nil {
			if series == eew.SeriesForecast && state.Event.Forecast != nil {
				got = describe(state.Event.Forecast.Status, parseSerial(state.Event.Forecast.Source.Serial))
			}
			if series == eew.SeriesWarning && state.Event.Warning != nil {
				got = describe(state.Event.Warning.Status, parseSerial(state.Event.Warning.Source.Serial))
				if state.Event.Warning.Data != nil && state.Event.Warning.Data.Coverage == eew.CoveragePartial {
					out.PartialWarnings++
				}
			}
		}
		if got != want && len(out.Mismatches) < verifyListLimit {
			out.Mismatches = append(out.Mismatches, Mismatch{EventID: sourceEventID(r), Series: series, Relay: want, Bridge: got})
		}
	}
	if out.StoredDrift, err = storedDrift(ctx, relay, latest); err != nil {
		return out, err
	}
	out.Quarantined, err = bridge.quarantined(ctx, verifyListLimit)
	return out, err
}

func parseSerial(s *string) *int {
	if s == nil {
		return nil
	}
	n, err := strconv.Atoi(*s)
	if err != nil {
		return nil
	}
	return &n
}

func storedDrift(ctx context.Context, relay *sql.DB, latest map[string]model.Report) ([]Mismatch, error) {
	out := []Mismatch{}
	rows, err := relay.QueryContext(ctx, "SELECT event_id,telegram_type,payload FROM streams WHERE telegram_type IN ('VXSE43','VXSE45')")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var eventID, code, payload string
		if err = rows.Scan(&eventID, &code, &payload); err != nil {
			return out, err
		}
		var stored model.Report
		if err = json.Unmarshal([]byte(payload), &stored); err != nil {
			return out, err
		}
		want, ok := latest[eventID+"\x00"+code]
		if !ok {
			continue
		}
		a, b := describe(relayStatus(stored), stored.Serial), describe(relayStatus(want), want.Serial)
		if a != b && len(out) < verifyListLimit {
			out = append(out, Mismatch{EventID: sourceEventID(want), Series: code, Relay: a, Bridge: b})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EventID < out[j].EventID })
	return out, rows.Err()
}

func (s *Store) quarantined(ctx context.Context, limit int) ([]Quarantined, error) {
	out := []Quarantined{}
	rows, err := s.DB.QueryContext(ctx, "SELECT telegram_id,code,event_id,serial,reason FROM inputs WHERE outcome=? ORDER BY processed_ms,rowid LIMIT ?", eew.Quarantined, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var q Quarantined
		var serial sql.NullInt64
		if err = rows.Scan(&q.TelegramID, &q.Code, &q.EventID, &serial, &q.Reason); err != nil {
			return out, err
		}
		if serial.Valid {
			n := int(serial.Int64)
			q.Serial = &n
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
