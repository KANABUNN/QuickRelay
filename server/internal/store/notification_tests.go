package store

import (
	"context"
	"database/sql"
	"errors"
	"quakerelay/server/internal/model"
	"time"
)

func (s *Store) NotificationTest(ctx context.Context, device, id string) (model.NotificationTest, error) {
	var out model.NotificationTest
	var ms int64
	err := s.DB.QueryRowContext(ctx, "SELECT id,style,status,requested_ms FROM notification_tests WHERE id=? AND device_id=?", id, device).
		Scan(&out.ID, &out.Style, &out.Status, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	out.RequestedAt = Time(time.UnixMilli(ms))
	return out, err
}

// Reserve before making the provider request. Retries with the same request ID
// inspect its result and never send again, even after a timeout or restart.
func (s *Store) ReserveNotificationTest(ctx context.Context, device, id, style string, now time.Time) (model.NotificationTest, bool, error) {
	if !ValidID(id) || style != "normal" && style != "warning" {
		return model.NotificationTest{}, false, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.NotificationTest{}, false, err
	}
	defer tx.Rollback()
	var owner, existingStyle, status string
	var ms int64
	err = tx.QueryRowContext(ctx, "SELECT device_id,style,status,requested_ms FROM notification_tests WHERE id=?", id).Scan(&owner, &existingStyle, &status, &ms)
	if err == nil {
		if owner != device || existingStyle != style {
			return model.NotificationTest{}, false, ErrInvalid
		}
		return model.NotificationTest{ID: id, Style: style, Status: status, RequestedAt: Time(time.UnixMilli(ms))}, false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.NotificationTest{}, false, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM notification_tests WHERE device_id=? AND requested_ms>?", device, now.Add(-time.Minute).UnixMilli()).Scan(&count); err != nil {
		return model.NotificationTest{}, false, err
	}
	if count > 0 {
		return model.NotificationTest{}, false, ErrRateLimited
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO notification_tests(id,device_id,style,status,requested_ms,updated_ms) VALUES(?,?,?,'pending',?,?)", id, device, style, now.UnixMilli(), now.UnixMilli()); err != nil {
		return model.NotificationTest{}, false, err
	}
	out := model.NotificationTest{ID: id, Style: style, Status: "pending", RequestedAt: Time(now)}
	return out, true, tx.Commit()
}

func (s *Store) FinishNotificationTest(ctx context.Context, device, id, status string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE notification_tests SET status=?,updated_ms=? WHERE id=? AND device_id=? AND status='pending'", status, now.UnixMilli(), id, device)
	return err
}
