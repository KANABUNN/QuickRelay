package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/model"
	"strings"
	"time"
)

type LiveActivity struct {
	DeviceID, EventID, TelegramType, ActivityID, Token, State string
	StartSequence, LastSequence, LastTimestamp                int64
}

func (s *Store) LiveActivity(ctx context.Context, device, event, telegram string) (LiveActivity, error) {
	out := LiveActivity{DeviceID: device, EventID: event, TelegramType: telegram}
	err := s.DB.QueryRowContext(ctx, `SELECT activity_id,token,state,start_sequence,last_sequence,last_timestamp
        FROM live_activities WHERE device_id=? AND event_id=? AND telegram_type=?`, device, event, telegram).
		Scan(&out.ActivityID, &out.Token, &out.State, &out.StartSequence, &out.LastSequence, &out.LastTimestamp)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}
func (s *Store) Stream(ctx context.Context, event, telegram string) (model.Report, error) {
	var r model.Report
	var data string
	err := s.DB.QueryRowContext(ctx, "SELECT payload FROM streams WHERE event_id=? AND telegram_type=?", event, telegram).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(data), &r)
	}
	return r, err
}
func (s *Store) RegisterLiveStartToken(ctx context.Context, device, token string, now time.Time) error {
	if !apns.ValidToken(token) {
		return ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM live_activity_start_tokens WHERE token=? AND device_id<>?", strings.ToLower(token), device); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO live_activity_start_tokens(device_id,token,updated_ms) VALUES(?,?,?)
        ON CONFLICT(device_id) DO UPDATE SET token=excluded.token,updated_ms=excluded.updated_ms`, device, strings.ToLower(token), now.UnixMilli())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReserveLiveStart(ctx context.Context, device string, r model.Report, now time.Time) (string, error) {
	if !r.StartsLiveActivity() || now.Sub(r.ReportedAt) >= r.TTL() {
		return "", nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var currentID string
	if err = tx.QueryRowContext(ctx, "SELECT json_extract(payload,'$.id') FROM streams WHERE event_id=? AND telegram_type=?", r.EventID, r.TelegramType).Scan(&currentID); err != nil {
		return "", err
	}
	if currentID != r.ID {
		return "", nil
	}
	var token string
	err = tx.QueryRowContext(ctx, "SELECT token FROM live_activity_start_tokens WHERE device_id=?", device).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var seq, lastSequence int64
	var state string
	err = tx.QueryRowContext(ctx, "SELECT start_sequence,last_sequence,state FROM live_activities WHERE device_id=? AND event_id=? AND telegram_type=?", device, r.EventID, r.TelegramType).Scan(&seq, &lastSequence, &state)
	if err == nil {
		if seq == r.ServerSequence && state == "starting" {
			return token, tx.Commit()
		}
		if state == "ended" && r.TelegramType == "VTSE41" && r.ServerSequence > lastSequence {
			_, err = tx.ExecContext(ctx, `UPDATE live_activities SET activity_id='',token='',state='starting',
                start_sequence=?,last_sequence=0,last_timestamp=0,updated_ms=? WHERE device_id=? AND event_id=? AND telegram_type=?`,
				r.ServerSequence, now.UnixMilli(), device, r.EventID, r.TelegramType)
			if err != nil {
				return "", err
			}
			return token, tx.Commit()
		}
		return "", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO live_activities(device_id,event_id,telegram_type,start_sequence,created_ms,updated_ms)
        VALUES(?,?,?,?,?,?)`, device, r.EventID, r.TelegramType, r.ServerSequence, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return "", err
	}
	return token, tx.Commit()
}
func (s *Store) FinishLiveStart(ctx context.Context, device string, r model.Report, token string, accepted, invalid bool, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if accepted {
		_, err = tx.ExecContext(ctx, `UPDATE live_activities SET last_sequence=max(last_sequence,?),last_timestamp=max(last_timestamp,?),updated_ms=?
            WHERE device_id=? AND event_id=? AND telegram_type=? AND start_sequence=?`, r.ServerSequence, now.Unix(), now.UnixMilli(), device, r.EventID, r.TelegramType, r.ServerSequence)
	} else {
		_, err = tx.ExecContext(ctx, "DELETE FROM live_activities WHERE device_id=? AND event_id=? AND telegram_type=? AND start_sequence=? AND token='' AND state='starting'",
			device, r.EventID, r.TelegramType, r.ServerSequence)
		if err == nil && invalid {
			_, err = tx.ExecContext(ctx, "DELETE FROM live_activity_start_tokens WHERE device_id=? AND token=?", device, token)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RegisterLiveActivityToken(ctx context.Context, device, event, telegram, activity, token string, startSequence int64, now time.Time) error {
	if !ValidID(event) || !ValidID(activity) || !apns.ValidToken(token) {
		return ErrInvalid
	}
	deviceState, err := s.Device(ctx, device)
	if err != nil {
		return err
	}
	report, err := s.Stream(ctx, event, telegram)
	if err != nil {
		return err
	}
	if !report.SupportsLiveActivity() {
		return ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A finished or dismissed activity is not resurrected by a late token.
	var state, oldActivity string
	var start int64
	err = tx.QueryRowContext(ctx, "SELECT state,activity_id,start_sequence FROM live_activities WHERE device_id=? AND event_id=? AND telegram_type=?", device, event, telegram).Scan(&state, &oldActivity, &start)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalid
	}
	if err == nil && (startSequence <= 0 || start != startSequence || state == "ended" || state == "dismissed" || oldActivity != "" && oldActivity != activity) {
		return ErrInvalid
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	forceEnd := state == "ending" || !deviceState.Active || !deviceState.Preferences.NotificationsEnabled || !deviceState.Preferences.LiveActivitiesEnabled
	newState := "active"
	if forceEnd {
		newState = "ending"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO live_activities(device_id,event_id,telegram_type,activity_id,token,token_updated_ms,state,created_ms,updated_ms)
        VALUES(?,?,?,?,?,?,?,?,?)
        ON CONFLICT(device_id,event_id,telegram_type) DO UPDATE SET activity_id=excluded.activity_id,token=excluded.token,
        token_updated_ms=excluded.token_updated_ms,state=excluded.state,updated_ms=excluded.updated_ms`,
		device, event, telegram, activity, strings.ToLower(token), now.UnixMilli(), newState, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	// Recover the latest publication after the start-token/update-token gap.
	_, err = tx.ExecContext(ctx, `INSERT INTO live_activity_jobs(report_sequence,device_id,force_end,next_attempt_ms,expires_ms,updated_ms)
        VALUES(?,?,?,?,?,?) ON CONFLICT(report_sequence,device_id) DO UPDATE SET status='pending',force_end=excluded.force_end,
        next_attempt_ms=excluded.next_attempt_ms,expires_ms=excluded.expires_ms WHERE live_activity_jobs.status IN ('pending','retry','skipped','expired','accepted')`,
		report.ServerSequence, device, forceEnd, now.UnixMilli(), now.Add(time.Minute).UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) EndLiveActivity(ctx context.Context, device, event, telegram, activity string, dismissed bool) error {
	state := "ended"
	if dismissed {
		state = "dismissed"
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE live_activities SET state=CASE WHEN state='ended' THEN state ELSE ? END,token='' WHERE device_id=? AND event_id=? AND telegram_type=? AND activity_id=?",
		state, device, event, telegram, activity)
	return err
}

// Explicit opt-out schedules only silent end pushes, even after revocation.
func (s *Store) StopLiveActivities(ctx context.Context, device string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE live_activities SET state='ending' WHERE device_id=? AND state NOT IN ('ended','dismissed')", device); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO live_activity_jobs(report_sequence,device_id,force_end,next_attempt_ms,expires_ms,updated_ms)
        SELECT json_extract(s.payload,'$.server_sequence'),a.device_id,1,?,?,? FROM live_activities a
        JOIN streams s ON s.event_id=a.event_id AND s.telegram_type=a.telegram_type
        WHERE a.device_id=? AND a.state='ending'
        ON CONFLICT(report_sequence,device_id) DO UPDATE SET force_end=1,status='pending',
        next_attempt_ms=excluded.next_attempt_ms,expires_ms=excluded.expires_ms`, now.UnixMilli(), now.Add(time.Minute).UnixMilli(), now.UnixMilli(), device)
	if err != nil {
		return err
	}
	return tx.Commit()
}

type LiveJob struct {
	ID                int64
	DeviceID          string
	ActivityID, Token string
	Report            model.Report
	ForceEnd          bool
	Attempts          int
	Expires           time.Time
}

func (s *Store) ClaimLiveJob(ctx context.Context, now time.Time) (*LiveJob, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE live_activity_jobs SET status='expired',reason='ttl_expired' WHERE status IN ('pending','retry','sending')
        AND expires_ms<=? AND lease_until_ms<=?`, now.UnixMilli(), now.UnixMilli()); err != nil {
		return nil, err
	}
	var out LiveJob
	var data string
	var ms int64
	err = tx.QueryRowContext(ctx, `SELECT j.id,j.device_id,r.payload,j.force_end,j.attempts,j.expires_ms FROM live_activity_jobs j
        JOIN reports r ON r.sequence=j.report_sequence WHERE
        ((j.status IN ('pending','retry') AND j.next_attempt_ms<=?) OR (j.status='sending' AND j.lease_until_ms<=?)) AND j.expires_ms>?
        AND NOT EXISTS(SELECT 1 FROM live_activity_jobs busy WHERE busy.device_id=j.device_id AND busy.status='sending' AND busy.lease_until_ms>?)
        ORDER BY j.report_sequence,j.id LIMIT 1`, now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.UnixMilli()).
		Scan(&out.ID, &out.DeviceID, &data, &out.ForceEnd, &out.Attempts, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(data), &out.Report); err != nil {
		return nil, err
	}
	out.Expires = time.UnixMilli(ms)
	out.Attempts++
	_, err = tx.ExecContext(ctx, "UPDATE live_activity_jobs SET status='sending',attempts=?,lease_until_ms=? WHERE id=?",
		out.Attempts, now.Add(30*time.Second).UnixMilli(), out.ID)
	if err != nil {
		return nil, err
	}
	return &out, tx.Commit()
}
func (s *Store) FinishLiveJob(ctx context.Context, j LiveJob, status, reason string, next time.Time, ended bool, timestamp int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if j.Token != "" {
		var currentToken string
		if err = tx.QueryRowContext(ctx, "SELECT token FROM live_activities WHERE device_id=? AND event_id=? AND telegram_type=?", j.DeviceID, j.Report.EventID, j.Report.TelegramType).Scan(&currentToken); err != nil {
			return err
		}
		if currentToken != "" && currentToken != j.Token {
			status = "retry"
			reason = "token_rotated"
			ended = false
			timestamp = 0
		}
	}
	res, err := tx.ExecContext(ctx, "UPDATE live_activity_jobs SET status=?,reason=?,next_attempt_ms=?,lease_until_ms=0,updated_ms=? WHERE id=? AND status='sending' AND attempts=?",
		status, reason, next.UnixMilli(), time.Now().UnixMilli(), j.ID, j.Attempts)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 && (status == "accepted" || ended) {
		state := "active"
		if ended {
			state = "ended"
		}
		_, err = tx.ExecContext(ctx, `UPDATE live_activities SET last_sequence=max(last_sequence,?),last_timestamp=max(last_timestamp,?),state=?,
            token=CASE WHEN ? THEN '' ELSE token END,updated_ms=? WHERE device_id=? AND event_id=? AND telegram_type=? AND activity_id=? AND token=?`,
			j.Report.ServerSequence, timestamp, state, ended, time.Now().UnixMilli(), j.DeviceID, j.Report.EventID, j.Report.TelegramType, j.ActivityID, j.Token)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
