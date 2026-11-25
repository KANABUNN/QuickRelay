// Package eewbridge persists the EEW bridge state in its own SQLite database
// (bridge.db), separate from the relay database. Each input frame is applied,
// recorded and, when the state changes, appended to the delivery log in one
// transaction.
package eewbridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/eew"
)

//go:embed migrations/*.sql
var migrations embed.FS

// StreamProduction is the v1 delivery stream: production EEW, full scope.
const StreamProduction = "eew-production"

var ErrInvalid = errors.New("invalid bridge input")

type Store struct {
	DB *sql.DB
	// changed wakes delivery sessions after a log append or an epoch change.
	changed signal
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*Store, error) { db.Close(); return nil, err }
	for _, q := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL",
		"CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY)"} {
		if _, err = db.Exec(q); err != nil {
			return fail(err)
		}
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return fail(err)
	}
	for _, entry := range entries {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE name=?", entry.Name()).Scan(&n); err != nil {
			return fail(err)
		}
		if n > 0 {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fail(err)
		}
		tx, err := db.Begin()
		if err != nil {
			return fail(err)
		}
		if _, err = tx.Exec(string(body)); err == nil {
			_, err = tx.Exec("INSERT INTO schema_migrations(name) VALUES(?)", entry.Name())
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return fail(err)
		}
	}
	epoch, err := newEpoch()
	if err != nil {
		return fail(err)
	}
	if _, err = db.Exec("INSERT OR IGNORE INTO streams(stream_id,epoch) VALUES(?,?)", StreamProduction, epoch); err != nil {
		return fail(err)
	}
	return &Store{DB: db}, nil
}
func (s *Store) Close() error { return s.DB.Close() }

func newEpoch() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "g" + hex.EncodeToString(b), nil
}

// Input is one frame handed to the bridge.
type Input struct {
	// Seq is the relay hand-off position; 0 outside live hand-off.
	Seq int64
	// ReceiptID is the SHA-256 of Frame; computed when empty.
	ReceiptID  string
	Frame      []byte
	ReceivedAt time.Time
	Upstream   string
	Origin     string
	// Relay is the stream state QuickRelay committed for this frame; nil when not applied.
	Relay *eew.RelayState
}

type Outcome struct {
	eew.Result
	ReceiptID, TelegramID, EventID string
	// LogSeq is the delivery log position appended for this input, or 0.
	LogSeq int64
}

func receiptOf(frame []byte) string {
	sum := sha256.Sum256(frame)
	return hex.EncodeToString(sum[:])
}

// peek reads the envelope identity of a frame that could not be parsed.
func peek(frame []byte) (id, code string) {
	var e struct {
		ID   string `json:"id"`
		Head struct {
			Type string `json:"type"`
		} `json:"head"`
	}
	if json.Unmarshal(frame, &e) == nil && len(e.ID) <= 128 && len(e.Head.Type) <= 16 {
		return e.ID, e.Head.Type
	}
	return "", ""
}

// Process applies one input. Inputs are idempotent by receipt: a frame already
// recorded returns duplicate without changes.
func (s *Store) Process(ctx context.Context, in Input, now time.Time) (Outcome, error) {
	if in.Origin != eew.OriginLive && in.Origin != eew.OriginRecovered && in.Origin != eew.OriginHistorical {
		return Outcome{}, ErrInvalid
	}
	out := Outcome{ReceiptID: in.ReceiptID}
	if out.ReceiptID == "" {
		out.ReceiptID = receiptOf(in.Frame)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var seen string
	err = tx.QueryRowContext(ctx, "SELECT outcome FROM inputs WHERE receipt_id=?", out.ReceiptID).Scan(&seen)
	if err == nil {
		out.Result = eew.Result{Outcome: eew.Duplicate, Reason: "receipt_seen"}
		if err = advanceCursor(ctx, tx, in); err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	row := inputRow{origin: in.Origin, upstream: in.Upstream, seq: in.Seq, received: in.ReceivedAt, relayApplied: in.Relay != nil}
	t, err := dmdata.ParseEEW(in.Frame)
	switch {
	case errors.Is(err, dmdata.ErrIgnored):
		out.TelegramID, row.code = peek(in.Frame)
		out.Result = eew.Result{Outcome: eew.Ignored, Reason: "not_operational_eew"}
	case err != nil:
		out.TelegramID, row.code = peek(in.Frame)
		out.Result = eew.Result{Outcome: eew.Quarantined, Reason: "parse: " + err.Error()}
	default:
		out.TelegramID, out.EventID = t.TelegramID, t.EventID
		row.code, row.serial, row.infoType, row.contentHash = t.Code, t.Serial, t.InfoType, t.ContentHash
		out.Result, out.LogSeq, err = s.apply(ctx, tx, t, in, now)
		if err != nil {
			return out, err
		}
	}
	row.telegramID, row.eventID = out.TelegramID, out.EventID
	if err = row.insert(ctx, tx, out, now); err != nil {
		return out, err
	}
	if err = advanceCursor(ctx, tx, in); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	if out.LogSeq > 0 {
		s.changed.fire()
	}
	return out, nil
}

// advanceCursor records the hand-off position with the input it came from.
func advanceCursor(ctx context.Context, tx *sql.Tx, in Input) error {
	if in.Seq <= 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, "UPDATE handoff_cursor SET seq=?,receipt_id=? WHERE id=1 AND seq<?", in.Seq, in.ReceiptID, in.Seq)
	return err
}

func (s *Store) apply(ctx context.Context, tx *sql.Tx, t eew.Telegram, in Input, now time.Time) (eew.Result, int64, error) {
	var hash string
	err := tx.QueryRowContext(ctx, "SELECT content_hash FROM inputs WHERE telegram_id=? AND content_hash<>'' ORDER BY processed_ms LIMIT 1", t.TelegramID).Scan(&hash)
	switch {
	case err == nil && hash == t.ContentHash:
		return eew.Result{Outcome: eew.Duplicate, Reason: "telegram_seen"}, 0, nil
	case err == nil:
		return eew.Result{Outcome: eew.Quarantined, Reason: "telegram_id_conflict"}, 0, nil
	case !errors.Is(err, sql.ErrNoRows):
		return eew.Result{}, 0, err
	}
	prev, err := loadState(ctx, tx, eew.EnvironmentProduction, t.EventID)
	if err != nil {
		return eew.Result{}, 0, err
	}
	next, result := eew.Apply(prev, t, eew.Context{ReceivedAt: in.ReceivedAt, Origin: in.Origin, Upstream: in.Upstream, Relay: in.Relay})
	if result.Outcome != eew.Applied {
		return result, 0, nil
	}
	if err = saveState(ctx, tx, next, now); err != nil {
		return result, 0, err
	}
	seq, err := appendLog(ctx, tx, StreamProduction, next.Event, "telegram", result.ChangedSeries, now)
	return result, seq, err
}

type inputRow struct {
	telegramID, code, eventID, infoType, contentHash, origin, upstream string
	serial                                                             *int
	seq                                                                int64
	received                                                           time.Time
	relayApplied                                                       bool
}

func (r inputRow) insert(ctx context.Context, tx *sql.Tx, out Outcome, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO inputs(receipt_id,telegram_id,code,event_id,serial,info_type,content_hash,ingress_seq,origin,upstream,
 relay_applied,received_ms,processed_ms,outcome,reason,changed_series,log_seq) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		out.ReceiptID, r.telegramID, r.code, r.eventID, r.serial, r.infoType, r.contentHash, r.seq, r.origin, r.upstream,
		r.relayApplied, r.received.UnixMilli(), now.UnixMilli(), out.Outcome, out.Reason, out.ChangedSeries, out.LogSeq)
	return err
}

func loadState(ctx context.Context, tx *sql.Tx, environment, eventID string) (*eew.State, error) {
	var state, meta string
	err := tx.QueryRowContext(ctx, "SELECT state,meta FROM events WHERE environment=? AND event_id=?", environment, eventID).Scan(&state, &meta)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s eew.State
	if err = json.Unmarshal([]byte(state), &s.Event); err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(meta), &s.Meta); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveState(ctx context.Context, tx *sql.Tx, s eew.State, now time.Time) error {
	state, err := json.Marshal(s.Event)
	if err != nil {
		return err
	}
	meta, err := json.Marshal(s.Meta)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events(environment,event_id,state,meta,terminal,updated_ms) VALUES(?,?,?,?,?,?)
 ON CONFLICT(environment,event_id) DO UPDATE SET state=excluded.state,meta=excluded.meta,terminal=excluded.terminal,updated_ms=excluded.updated_ms`,
		s.Event.Environment, s.Event.EventID, string(state), string(meta), s.Event.Terminal(), now.UnixMilli())
	return err
}

func appendLog(ctx context.Context, tx *sql.Tx, stream string, e eew.Event, cause, changed string, now time.Time) (int64, error) {
	event, err := json.Marshal(e)
	if err != nil {
		return 0, err
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, "UPDATE streams SET last_seq=last_seq+1 WHERE stream_id=? RETURNING last_seq", stream).Scan(&seq); err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO stream_log(stream_id,seq,environment,event_id,cause,changed_series,committed_ms,event) VALUES(?,?,?,?,?,?,?,?)`,
		stream, seq, e.Environment, e.EventID, cause, changed, now.UnixMilli(), string(event))
	return seq, err
}

// State returns the stored state of one event.
func (s *Store) State(ctx context.Context, environment, eventID string) (*eew.State, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return loadState(ctx, tx, environment, eventID)
}

// LogRecord is one committed delivery log entry.
type LogRecord struct {
	Seq           int64
	Cause         string
	ChangedSeries string
	CommittedAt   time.Time
	Event         eew.Event
}

type Stream struct {
	ID, Epoch              string
	LastSeq, PurgedThrough int64
}

func (s *Store) Stream(ctx context.Context, id string) (Stream, error) {
	out := Stream{ID: id}
	err := s.DB.QueryRowContext(ctx, "SELECT epoch,last_seq,purged_through FROM streams WHERE stream_id=?", id).Scan(&out.Epoch, &out.LastSeq, &out.PurgedThrough)
	return out, err
}

// Log returns entries after a position in order.
func (s *Store) Log(ctx context.Context, stream string, after int64, limit int) ([]LogRecord, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT seq,cause,changed_series,committed_ms,event FROM stream_log WHERE stream_id=? AND seq>? ORDER BY seq LIMIT ?", stream, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogRecord
	for rows.Next() {
		var r LogRecord
		var committed int64
		var event string
		if err = rows.Scan(&r.Seq, &r.Cause, &r.ChangedSeries, &committed, &event); err != nil {
			return nil, err
		}
		r.CommittedAt = time.UnixMilli(committed).UTC()
		if err = json.Unmarshal([]byte(event), &r.Event); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Retention bounds the delivery log and the event/input records.
type Retention struct {
	LogAge   time.Duration
	LogRows  int64
	StateAge time.Duration
}

// DefaultRetention follows the integration design: log 15 minutes or 10,000
// entries; terminal records and duplicate checks 24 hours.
var DefaultRetention = Retention{LogAge: 15 * time.Minute, LogRows: 10000, StateAge: 24 * time.Hour}

// Prune removes expired records and returns the log entries removed. The
// removed log range is recorded in the same transaction.
func (s *Store) Prune(ctx context.Context, now time.Time, r Retention) (int64, error) {
	if r.LogAge <= 0 || r.LogRows < 1 || r.StateAge <= 0 {
		return 0, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT stream_id,last_seq,purged_through FROM streams")
	if err != nil {
		return 0, err
	}
	var streams []Stream
	for rows.Next() {
		var st Stream
		if err = rows.Scan(&st.ID, &st.LastSeq, &st.PurgedThrough); err != nil {
			rows.Close()
			return 0, err
		}
		streams = append(streams, st)
	}
	rows.Close()
	var removed int64
	for _, st := range streams {
		var aged int64
		if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM stream_log WHERE stream_id=? AND committed_ms<?", st.ID, now.Add(-r.LogAge).UnixMilli()).Scan(&aged); err != nil {
			return 0, err
		}
		through := max(aged, st.LastSeq-r.LogRows)
		if through <= st.PurgedThrough {
			continue
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM stream_log WHERE stream_id=? AND seq<=?", st.ID, through)
		if err != nil {
			return 0, err
		}
		n, _ := result.RowsAffected()
		removed += n
		if _, err = tx.ExecContext(ctx, "UPDATE streams SET purged_through=? WHERE stream_id=?", through, st.ID); err != nil {
			return 0, err
		}
	}
	cutoff := now.Add(-r.StateAge).UnixMilli()
	if _, err = tx.ExecContext(ctx, "DELETE FROM events WHERE updated_ms<?", cutoff); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM inputs WHERE processed_ms<?", cutoff); err != nil {
		return 0, err
	}
	return removed, tx.Commit()
}
