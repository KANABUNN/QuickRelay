package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"quakerelay/server/internal/model"
)

// EEW bridge hand-off. Live VXSE43/VXSE45 reports are recorded in the ingest
// transaction, so the bridge reads exactly what QuickRelay committed and in
// commit order. Delivery decisions (APNs, Live Activity) never depend on it.

// Handoff identifies the live reception a report came from.
type Handoff struct {
	// ReceiptID is the received_telegrams key of the frame (SHA-256 of its bytes).
	ReceiptID string
	// Upstream identifies the DMDATA connection that delivered the frame.
	Upstream string
}

// HandoffApplied is the stream state QuickRelay committed for a handed-off report.
type HandoffApplied struct {
	EventID       string `json:"event_id"`
	SourceEventID string `json:"source_event_id"`
	TelegramType  string `json:"telegram_type"`
	Serial        *int   `json:"serial"`
	Cancelled     bool   `json:"is_cancelled"`
	Final         bool   `json:"is_final"`
}

type HandoffItem struct {
	Seq            int64
	ReceiptID      string
	ReportSequence int64
	ReportID       string
	TelegramType   string
	ReceivedAt     time.Time
	CommittedAt    time.Time
	Upstream       string
	// Nil when QuickRelay did not make this report current for its stream.
	Applied *HandoffApplied
	Frame   []byte
}

type HandoffBatch struct {
	Items []HandoffItem
	// Latest is the highest position ever assigned; PurgedThrough the highest removed.
	Latest, PurgedThrough int64
	// Gap reports that positions after the cursor were removed before being read.
	Gap bool
	// More is set when further rows exist beyond this batch.
	More bool
}

// ErrHandoffCursor means the cursor does not belong to this database's history,
// for example after a restore. The reader must rebuild from a snapshot.
var ErrHandoffCursor = errors.New("hand-off cursor does not match this database")

func HandoffTelegram(code string) bool { return code == "VXSE43" || code == "VXSE45" }

// IngestWithHandoff is Ingest that also records live VXSE43/VXSE45 reports for the
// EEW bridge in the same transaction. A hand-off write failure fails the ingest.
func (s *Store) IngestWithHandoff(ctx context.Context, r model.Report, h Handoff) (IngestResult, error) {
	if h.ReceiptID == "" {
		return IngestResult{}, ErrInvalid
	}
	if h.Upstream == "" {
		// A missing label must not stop reception; the bridge treats it as its own generation.
		h.Upstream = "unknown"
	}
	return s.ingest(ctx, r, true, &h)
}

func writeHandoff(ctx context.Context, tx *sql.Tx, r model.Report, h Handoff, current bool, now time.Time) error {
	var applied any
	if current {
		data, err := json.Marshal(HandoffApplied{EventID: r.EventID, SourceEventID: r.SourceEventID, TelegramType: r.TelegramType,
			Serial: r.Serial, Cancelled: r.Cancelled, Final: r.Final})
		if err != nil {
			return err
		}
		applied = string(data)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO eew_handoff(receipt_id,report_sequence,report_id,telegram_type,received_ms,committed_ms,upstream_generation,applied)
 VALUES(?,?,?,?,?,?,?,?)`, h.ReceiptID, r.ServerSequence, r.ID, r.TelegramType, r.ReceivedAt.UnixMilli(), now.UnixMilli(), h.Upstream, applied)
	return err
}

// handoffSignal wakes readers waiting for rows committed after their query.
type handoffSignal struct {
	mu sync.Mutex
	ch chan struct{}
}

func (s *handoffSignal) wait() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch == nil {
		s.ch = make(chan struct{})
	}
	return s.ch
}
func (s *handoffSignal) fire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch != nil {
		close(s.ch)
	}
	s.ch = make(chan struct{})
}

// HandoffChanged returns a channel closed by the next hand-off commit or prune.
// Take it before querying so a commit in between is not missed.
func (s *Store) HandoffChanged() <-chan struct{} { return s.handoff.wait() }

func handoffBounds(ctx context.Context, tx *sql.Tx) (latest, purged int64, err error) {
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name='eew_handoff'),0)").Scan(&latest); err != nil {
		return
	}
	err = tx.QueryRowContext(ctx, "SELECT purged_through FROM eew_handoff_meta WHERE id=1").Scan(&purged)
	return
}

// HandoffBatch returns rows after the cursor, bounded by count and frame bytes.
// A single frame larger than maxBytes is returned alone. afterReceipt, when given,
// must match the row at the cursor while that row is retained.
func (s *Store) HandoffBatch(ctx context.Context, after int64, afterReceipt string, limit, maxBytes int) (HandoffBatch, error) {
	var out HandoffBatch
	if after < 0 || limit < 1 || maxBytes < 1 {
		return out, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if out.Latest, out.PurgedThrough, err = handoffBounds(ctx, tx); err != nil {
		return out, err
	}
	if after > out.Latest {
		return out, ErrHandoffCursor
	}
	if after > out.PurgedThrough && afterReceipt != "" {
		var receipt string
		err = tx.QueryRowContext(ctx, "SELECT receipt_id FROM eew_handoff WHERE ingress_seq=?", after).Scan(&receipt)
		if errors.Is(err, sql.ErrNoRows) || err == nil && receipt != afterReceipt {
			return out, ErrHandoffCursor
		}
		if err != nil {
			return out, err
		}
	}
	out.Gap = after < out.PurgedThrough
	// The receipt journal keeps the exact frame; reports.raw holds the same bytes.
	rows, err := tx.QueryContext(ctx, `SELECT h.ingress_seq,h.receipt_id,h.report_sequence,h.report_id,h.telegram_type,h.received_ms,h.committed_ms,
 h.upstream_generation,h.applied,COALESCE(t.frame,r.raw)
 FROM eew_handoff h LEFT JOIN received_telegrams t ON t.frame_sha256=h.receipt_id LEFT JOIN reports r ON r.sequence=h.report_sequence
 WHERE h.ingress_seq>? ORDER BY h.ingress_seq LIMIT ?`, after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	used := 0
	for rows.Next() {
		var item HandoffItem
		var received, committed int64
		var applied sql.NullString
		if err = rows.Scan(&item.Seq, &item.ReceiptID, &item.ReportSequence, &item.ReportID, &item.TelegramType, &received, &committed,
			&item.Upstream, &applied, &item.Frame); err != nil {
			return out, err
		}
		if len(out.Items) == limit || len(out.Items) > 0 && used+len(item.Frame) > maxBytes {
			out.More = true
			break
		}
		if item.Frame == nil {
			return out, errors.New("hand-off frame missing")
		}
		item.ReceivedAt, item.CommittedAt = time.UnixMilli(received).UTC(), time.UnixMilli(committed).UTC()
		if applied.Valid {
			item.Applied = &HandoffApplied{}
			if err = json.Unmarshal([]byte(applied.String), item.Applied); err != nil {
				return out, err
			}
		}
		used += len(item.Frame)
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// PruneHandoff removes rows older than maxAge or beyond the newest maxRows, and
// records the removed range in the same transaction. It returns the rows removed.
func (s *Store) PruneHandoff(ctx context.Context, now time.Time, maxAge time.Duration, maxRows int64) (int64, error) {
	if maxAge <= 0 || maxRows < 1 {
		return 0, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	latest, purged, err := handoffBounds(ctx, tx)
	if err != nil {
		return 0, err
	}
	var aged int64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(ingress_seq),0) FROM eew_handoff WHERE committed_ms<?", now.Add(-maxAge).UnixMilli()).Scan(&aged); err != nil {
		return 0, err
	}
	through := max(aged, latest-maxRows)
	if through <= purged {
		return 0, nil
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM eew_handoff WHERE ingress_seq<=?", through)
	if err != nil {
		return 0, err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE eew_handoff_meta SET purged_through=? WHERE id=1", through); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	s.handoff.fire()
	return removed, nil
}
