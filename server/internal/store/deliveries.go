package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"quakerelay/server/internal/model"
)

type Delivery struct {
	ID       int64
	DeviceID string
	Report   model.Report
	Attempts int
	APNsID   string
	Expires  time.Time
}

func (s *Store) Claim(ctx context.Context, now time.Time) (*Delivery, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE deliveries SET status='expired',reason='ttl_expired',updated_ms=?
 WHERE status IN ('pending','retry','sending') AND expires_ms<=? AND lease_until_ms<=?`, now.UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
		return nil, err
	}
	var d Delivery
	var raw string
	var expires int64
	err = tx.QueryRowContext(ctx, `SELECT d.id,d.device_id,d.attempts,d.expires_ms,r.payload
 FROM deliveries d JOIN reports r ON r.sequence=d.report_sequence
 WHERE ((d.status IN ('pending','retry') AND d.next_attempt_ms<=?) OR (d.status='sending' AND d.lease_until_ms<=?))
 AND d.expires_ms>? AND NOT EXISTS (
  SELECT 1 FROM deliveries busy WHERE busy.device_id=d.device_id AND busy.status='sending' AND busy.lease_until_ms>?)
 ORDER BY d.report_sequence,d.id LIMIT 1`, now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.UnixMilli()).Scan(&d.ID, &d.DeviceID, &d.Attempts, &expires, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(raw), &d.Report); err != nil {
		return nil, err
	}
	d.Attempts++
	d.Expires = time.UnixMilli(expires)
	d.APNsID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(d.Report.ID+":"+d.DeviceID)).String()
	if _, err = tx.ExecContext(ctx, `UPDATE deliveries SET status='sending',attempts=?,lease_until_ms=?,apns_id=?,updated_ms=? WHERE id=?`,
		d.Attempts, now.Add(30*time.Second).UnixMilli(), d.APNsID, now.UnixMilli(), d.ID); err != nil {
		return nil, err
	}
	return &d, tx.Commit()
}
func (s *Store) Finish(ctx context.Context, d Delivery, status, reason string, next time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE deliveries SET status=?,reason=?,next_attempt_ms=?,lease_until_ms=0,updated_ms=?
 WHERE id=? AND status='sending' AND attempts=?`, status, reason, next.UnixMilli(), time.Now().UnixMilli(), d.ID, d.Attempts)
	return err
}
func (s *Store) Superseded(ctx context.Context, r model.Report, retry bool) (bool, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, "SELECT payload FROM streams WHERE event_id=? AND telegram_type=?", r.EventID, r.TelegramType).Scan(&raw)
	if err != nil {
		return false, err
	}
	var latest model.Report
	if err = json.Unmarshal([]byte(raw), &latest); err != nil {
		return false, err
	}
	return latest.ID != r.ID && (latest.Cancelled || retry && latest.NewerThan(r)), nil
}
func (s *Store) DeliveryCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT status,count(*) FROM deliveries GROUP BY status")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var status string
		var n int64
		if err = rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

// A forecast cancellation never cancels an independently followed warning.
func (s *Store) Followed(ctx context.Context, device string, r model.Report) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM deliveries d JOIN reports p ON p.sequence=d.report_sequence
    WHERE d.device_id=? AND d.status IN ('accepted','sending') AND p.event_id=? AND p.telegram_type=? AND p.sequence<?`, device, r.EventID, r.TelegramType, r.ServerSequence).Scan(&n)
	return n > 0, err
}
