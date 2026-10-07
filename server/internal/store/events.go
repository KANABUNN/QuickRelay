package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"quakerelay/server/internal/model"
)

type IngestResult struct {
	Inserted, Current bool
	Sequence          int64
}

func (s *Store) Ingest(ctx context.Context, r model.Report) (IngestResult, error) {
	return s.ingest(ctx, r, true)
}
func (s *Store) IngestHistorical(ctx context.Context, r model.Report) (IngestResult, error) {
	return s.ingest(ctx, r, false)
}
func (s *Store) ingest(ctx context.Context, r model.Report, notify bool) (IngestResult, error) {
	out := IngestResult{}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	// Semantic duplicate protection across transport IDs; preserve corrections and terminal changes.
	fp := r
	fp.ID = ""
	fp.MessageID = ""
	fp.ReceivedAt = time.Time{}
	fp.Raw = nil
	fp.ServerSequence = 0
	fpBytes, _ := json.Marshal(fp)
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO reports(id,message_id,fingerprint,event_id,telegram_type,payload,raw,received_ms)
 VALUES(?,?,?,?,?,'{}',?,?)`, r.ID, r.MessageID, Hash(string(fpBytes)), r.EventID, r.TelegramType, []byte(r.Raw), r.ReceivedAt.UnixMilli())
	if err != nil {
		return out, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return out, err
	}
	if n == 0 {
		return out, tx.Commit()
	}
	r.ServerSequence, err = res.LastInsertId()
	if err != nil {
		return out, err
	}
	out.Inserted = true
	out.Sequence = r.ServerSequence
	payload, err := json.Marshal(r)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE reports SET payload=? WHERE sequence=?", string(payload), r.ServerSequence); err != nil {
		return out, err
	}
	var oldJSON string
	err = tx.QueryRowContext(ctx, "SELECT payload FROM streams WHERE event_id=? AND telegram_type=?", r.EventID, r.TelegramType).Scan(&oldJSON)
	current := errors.Is(err, sql.ErrNoRows)
	if err == nil {
		var old model.Report
		if err = json.Unmarshal([]byte(oldJSON), &old); err != nil {
			return out, err
		}
		current = r.NewerThan(old)
	} else if !current {
		return out, err
	}
	out.Current = current
	if current {
		if _, err = tx.ExecContext(ctx, `INSERT INTO streams(event_id,telegram_type,payload) VALUES(?,?,?)
   ON CONFLICT(event_id,telegram_type) DO UPDATE SET payload=excluded.payload`, r.EventID, r.TelegramType, string(payload)); err != nil {
			return out, err
		}
		var oldEventJSON string
		err = tx.QueryRowContext(ctx, "SELECT payload FROM events WHERE event_id=?", r.EventID).Scan(&oldEventJSON)
		visible := errors.Is(err, sql.ErrNoRows)
		if err == nil {
			var old model.Event
			if err = json.Unmarshal([]byte(oldEventJSON), &old); err != nil {
				return out, err
			}
			visible = old.TelegramType == r.TelegramType || r.ReportedAt.After(old.LatestReportAt) ||
				r.ReportedAt.Equal(old.LatestReportAt) && (r.Cancelled || r.Warning && !old.Warning)
		} else if !visible {
			return out, err
		}
		if visible {
			eventBytes, _ := json.Marshal(r.Event())
			if _, err = tx.ExecContext(ctx, `INSERT INTO events(event_id,sequence,payload) VALUES(?,?,?)
    ON CONFLICT(event_id) DO UPDATE SET sequence=excluded.sequence,payload=excluded.payload`, r.EventID, r.ServerSequence, string(eventBytes)); err != nil {
				return out, err
			}
		}
	}
	if current && notify && r.PushEligible() {
		_, err = tx.ExecContext(ctx, `INSERT INTO deliveries(report_sequence,device_id,next_attempt_ms,expires_ms,updated_ms)
          SELECT ?,installation_id,?,?,? FROM devices WHERE revoked=0 AND push_active=1`,
			r.ServerSequence, r.ReceivedAt.UnixMilli(), r.ReportedAt.Add(r.TTL()).UnixMilli(), r.ReceivedAt.UnixMilli())
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}
func (s *Store) Sync(ctx context.Context, after int64, limit int) (model.SyncPage, error) {
	out := model.SyncPage{OK: true, Items: []model.SyncItem{}, Next: after, ServerTime: Time(time.Now())}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0) FROM reports").Scan(&out.Latest); err != nil {
		return out, err
	}
	if after < 0 || after > out.Latest {
		return out, ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.payload,e.payload FROM reports r JOIN events e ON e.event_id=r.event_id
 WHERE r.sequence>? AND r.sequence<=? ORDER BY r.sequence LIMIT ?`, after, out.Latest, limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a, b string
		if err = rows.Scan(&a, &b); err != nil {
			break
		}
		var item model.SyncItem
		if err = json.Unmarshal([]byte(a), &item.Report); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(b), &item.Event); err != nil {
			break
		}
		if len(out.Items) == limit {
			out.HasMore = true
			break
		}
		out.Items = append(out.Items, item)
		out.Next = item.Report.ServerSequence
	}
	rowsErr := rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if rowsErr != nil {
		return out, rowsErr
	}
	return out, tx.Commit()
}
func (s *Store) Events(ctx context.Context, before int64, limit int) ([]model.Event, error) {
	if before == 0 {
		before = 1<<63 - 1
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT payload FROM events WHERE sequence<? ORDER BY sequence DESC LIMIT ?", before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Event{}
	for rows.Next() {
		var b string
		var e model.Event
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &e); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}
func (s *Store) Event(ctx context.Context, id string) (model.Event, []model.Report, error) {
	var event model.Event
	var raw string
	reports := []model.Report{}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return event, nil, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, "SELECT payload FROM events WHERE event_id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return event, nil, ErrNotFound
	}
	if err != nil {
		return event, nil, err
	}
	if err = json.Unmarshal([]byte(raw), &event); err != nil {
		return event, nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT payload FROM reports WHERE event_id=? ORDER BY sequence", id)
	if err != nil {
		return event, nil, err
	}
	for rows.Next() {
		var b string
		var r model.Report
		if err = rows.Scan(&b); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(b), &r); err != nil {
			break
		}
		reports = append(reports, r)
	}
	rowsErr := rows.Err()
	rows.Close()
	if err != nil {
		return event, nil, err
	}
	if rowsErr != nil {
		return event, nil, rowsErr
	}
	return event, reports, tx.Commit()
}

// SourceReport is available only through an authenticated API handler.
func (s *Store) SourceReport(ctx context.Context, id string) (model.Report, []byte, error) {
	var r model.Report
	var payload string
	var raw []byte
	err := s.DB.QueryRowContext(ctx, "SELECT payload,raw FROM reports WHERE id=?", id).Scan(&payload, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil, ErrNotFound
	}
	if err != nil {
		return r, nil, err
	}
	if err = json.Unmarshal([]byte(payload), &r); err != nil {
		return r, nil, err
	}
	if r.IsEEW() || r.Bulletin == nil || r.Bulletin.Document == nil {
		return r, nil, ErrNotFound
	}
	return r, raw, nil
}
