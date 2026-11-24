package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"quakerelay/server/internal/model"
)

// Only use press times to order simultaneous publications when each has one.
// Older cached reports without press time still participate in cancellation
// and correction precedence.
const tsunamiPublicationOrder = `julianday(json_extract(payload,'$.occurred_at')) DESC,
    CASE WHEN MIN(json_extract(payload,'$.press_time') IS NOT NULL) OVER (
        PARTITION BY julianday(json_extract(payload,'$.occurred_at'))
    ) THEN julianday(json_extract(payload,'$.press_time')) END DESC,
    json_extract(payload,'$.is_cancelled') DESC,
    CASE WHEN json_extract(payload,'$.info_type')='訂正' THEN 1 ELSE 0 END DESC,
    CAST(json_extract(payload,'$.server_sequence') AS INTEGER) DESC`

type reportQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func latestTsunamiReport(ctx context.Context, q reportQuerier) (model.Report, error) {
	var report model.Report
	var data string
	err := q.QueryRowContext(ctx, `SELECT payload FROM streams WHERE telegram_type='VTSE41'
        AND julianday(json_extract(payload,'$.occurred_at'))=(SELECT max(julianday(json_extract(payload,'$.occurred_at'))) FROM streams WHERE telegram_type='VTSE41')
        ORDER BY `+tsunamiPublicationOrder+` LIMIT 1`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return report, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(data), &report)
	}
	return report, err
}

func (s *Store) LatestTsunamiReport(ctx context.Context) (model.Report, error) {
	return latestTsunamiReport(ctx, s.DB)
}

// Walk the national publications back only through the current warning for
// each released zone. A delivery from an earlier tsunami cannot establish
// that this installation received this warning.
func (s *Store) FollowedTsunamiReleases(ctx context.Context, device string, r model.Report, regions []string) ([]model.TsunamiArea, error) {
	var releases []model.TsunamiArea
	for _, area := range r.TsunamiForecastAreas() {
		if area.Released() {
			releases = append(releases, area)
		}
	}
	if len(releases) == 0 {
		return nil, nil
	}
	latest, err := s.LatestTsunamiReport(ctx)
	if err != nil {
		return nil, err
	}
	if latest.ID != r.ID {
		if latest.Cancelled {
			return nil, nil
		}
		active := latest.TsunamiWarningAreas(nil)
		releases = slices.DeleteFunc(releases, func(release model.TsunamiArea) bool {
			return slices.ContainsFunc(active, release.Matches)
		})
		if len(releases) == 0 {
			return nil, nil
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.payload,d.id IS NOT NULL,d.tsunami_warning_areas FROM reports r
        LEFT JOIN deliveries d ON d.report_sequence=r.sequence AND d.device_id=? AND d.status='accepted'
        WHERE r.telegram_type='VTSE41' AND r.sequence<?
        AND julianday(json_extract(payload,'$.occurred_at'))<=julianday(?)
        ORDER BY `+tsunamiPublicationOrder, device, r.ServerSequence, Time(r.ReportedAt))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var followed []model.TsunamiArea
	for rows.Next() {
		var previous model.Report
		var data string
		var accepted bool
		var scopeJSON sql.NullString
		if err = rows.Scan(&data, &accepted, &scopeJSON); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(data), &previous); err != nil {
			return nil, err
		}
		if previous.Cancelled || len(previous.TsunamiForecastAreas()) == 0 || previous.NewerThan(r) {
			continue
		}
		active := previous.TsunamiWarningAreas(nil)
		var delivered []model.TsunamiArea
		if scopeJSON.Valid {
			if err = json.Unmarshal([]byte(scopeJSON.String), &delivered); err != nil {
				return nil, err
			}
		} else if accepted {
			// Older rows retain the bulletin but have no saved device scope.
			delivered = previous.TsunamiWarningAreas(regions)
		}
		releases = slices.DeleteFunc(releases, func(release model.TsunamiArea) bool {
			if !slices.ContainsFunc(active, release.Matches) {
				return true
			}
			matched := slices.ContainsFunc(delivered, release.Matches)
			if matched {
				followed = append(followed, release)
			}
			return matched
		})
		if len(releases) == 0 {
			break
		}
	}
	return followed, rows.Err()
}
