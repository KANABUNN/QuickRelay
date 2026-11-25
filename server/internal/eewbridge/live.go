package eewbridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"quakerelay/server/internal/eew"
)

// signal wakes waiters on the next fire; take the channel before checking state.
type signal struct {
	mu sync.Mutex
	ch chan struct{}
}

func (s *signal) wait() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch == nil {
		s.ch = make(chan struct{})
	}
	return s.ch
}
func (s *signal) fire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch != nil {
		close(s.ch)
	}
	s.ch = make(chan struct{})
}

// Changed is closed by the next log append or epoch change.
func (s *Store) Changed() <-chan struct{} { return s.changed.wait() }

// HandoffCursor is the last relay hand-off position applied.
type HandoffCursor struct {
	Seq        int64
	ReceiptID  string
	Generation string
}

func (s *Store) Cursor(ctx context.Context) (HandoffCursor, error) {
	var c HandoffCursor
	err := s.DB.QueryRowContext(ctx, "SELECT seq,receipt_id,upstream_generation FROM handoff_cursor WHERE id=1").Scan(&c.Seq, &c.ReceiptID, &c.Generation)
	return c, err
}

// SetCursor moves the position without an input, for example past a removed
// range or back to the start after the relay's history changed.
func (s *Store) SetCursor(ctx context.Context, seq int64, receipt string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE handoff_cursor SET seq=?,receipt_id=? WHERE id=1", seq, receipt)
	return err
}

func (s *Store) SetGeneration(ctx context.Context, generation string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE handoff_cursor SET upstream_generation=? WHERE id=1", generation)
	return err
}

// Reconcile marks the accumulated regions of every active warning as partial
// after reports may have been missed, through the log with cause=reconcile.
// With rotate, the delivery log also starts a new epoch, so clients replace
// their state from a snapshot. It returns the events changed.
func (s *Store) Reconcile(ctx context.Context, now time.Time, rotate bool) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT state,meta FROM events WHERE terminal=0")
	if err != nil {
		return 0, err
	}
	var states []eew.State
	for rows.Next() {
		var state, meta string
		var st eew.State
		if err = rows.Scan(&state, &meta); err == nil {
			err = json.Unmarshal([]byte(state), &st.Event)
		}
		if err == nil {
			err = json.Unmarshal([]byte(meta), &st.Meta)
		}
		if err != nil {
			rows.Close()
			return 0, err
		}
		states = append(states, st)
	}
	rows.Close()
	changed := 0
	for _, st := range states {
		w := st.Event.Warning
		if w == nil || w.Status != eew.StatusActive || w.Data == nil || w.Data.Coverage == eew.CoveragePartial {
			continue
		}
		data := *w.Data
		data.Coverage = eew.CoveragePartial
		next := *w
		next.Data, next.Revision = &data, w.Revision+1
		st.Event.Warning = &next
		st.Event.Revision++
		if err = saveState(ctx, tx, st, now); err != nil {
			return 0, err
		}
		if _, err = appendLog(ctx, tx, StreamProduction, st.Event, "reconcile", eew.SeriesWarning, now); err != nil {
			return 0, err
		}
		changed++
	}
	if rotate {
		epoch, err := newEpoch()
		if err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM stream_log"); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE streams SET epoch=?,purged_through=last_seq", epoch); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	if changed > 0 || rotate {
		s.changed.fire()
	}
	return changed, nil
}

// Resume is a client's delivery position.
type Resume struct {
	StreamID, Epoch string
	Seq             int64
}

// SyncView is a consistent starting point for one client: either the log
// entries after its position, or the recent states as a snapshot.
type SyncView struct {
	Stream  Stream
	Mode    string // resume or snapshot
	Records []LogRecord
	Events  []eew.Event
}

// ErrResync means a client's live position can no longer continue.
var ErrResync = errors.New("delivery position is no longer continuous")

// SyncView reads the stream position and the data to reach it in one read
// transaction. A resume is used when the position belongs to the current epoch
// and at most maxRecords entries are needed; otherwise the snapshot holds the
// events updated since the given time.
func (s *Store) SyncView(ctx context.Context, stream string, resume *Resume, since time.Time, maxRecords int) (SyncView, error) {
	var out SyncView
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Stream.ID = stream
	if err = tx.QueryRowContext(ctx, "SELECT epoch,last_seq,purged_through FROM streams WHERE stream_id=?", stream).
		Scan(&out.Stream.Epoch, &out.Stream.LastSeq, &out.Stream.PurgedThrough); err != nil {
		return out, err
	}
	st := out.Stream
	if resume != nil && resume.StreamID == stream && resume.Epoch == st.Epoch && resume.Seq >= st.PurgedThrough &&
		resume.Seq <= st.LastSeq && st.LastSeq-resume.Seq <= int64(maxRecords) {
		out.Mode = "resume"
		out.Records, err = readLog(ctx, tx, stream, resume.Seq, st.LastSeq)
		return out, err
	}
	out.Mode, out.Events = "snapshot", []eew.Event{}
	rows, err := tx.QueryContext(ctx, "SELECT state FROM events WHERE environment=? AND updated_ms>=? ORDER BY updated_ms,event_id",
		eew.EnvironmentProduction, since.UnixMilli())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var e eew.Event
		if err = rows.Scan(&state); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(state), &e); err != nil {
			return out, err
		}
		out.Events = append(out.Events, e)
	}
	return out, rows.Err()
}

// LiveBatch returns entries after a live position of the given epoch.
func (s *Store) LiveBatch(ctx context.Context, stream, epoch string, after int64, limit int) ([]LogRecord, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var current string
	var last, purged int64
	if err = tx.QueryRowContext(ctx, "SELECT epoch,last_seq,purged_through FROM streams WHERE stream_id=?", stream).Scan(&current, &last, &purged); err != nil {
		return nil, err
	}
	if current != epoch || after < purged || after > last {
		return nil, ErrResync
	}
	return readLog(ctx, tx, stream, after, min(last, after+int64(limit)))
}

func readLog(ctx context.Context, tx *sql.Tx, stream string, after, through int64) ([]LogRecord, error) {
	rows, err := tx.QueryContext(ctx, "SELECT seq,cause,changed_series,committed_ms,event FROM stream_log WHERE stream_id=? AND seq>? AND seq<=? ORDER BY seq",
		stream, after, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogRecord
	expected := after + 1
	for rows.Next() {
		var r LogRecord
		var committed int64
		var event string
		if err = rows.Scan(&r.Seq, &r.Cause, &r.ChangedSeries, &committed, &event); err != nil {
			return nil, err
		}
		if r.Seq != expected {
			return nil, ErrResync
		}
		expected++
		r.CommittedAt = time.UnixMilli(committed).UTC()
		if err = json.Unmarshal([]byte(event), &r.Event); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if expected != through+1 {
		return nil, ErrResync
	}
	return out, nil
}
