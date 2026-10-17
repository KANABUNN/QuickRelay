package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// ArchiveFrame commits exact transport bytes before any decoding/normalization.
// Duplicate deliveries retain the first receipt and count further receptions.
func (s *Store) ArchiveFrame(ctx context.Context, frame []byte, received time.Time) (string, error) {
	if len(frame) > 2<<20 {
		return "", ErrInvalid
	}
	// Even an empty malformed WebSocket message is evidence, not a database failure.
	// Bind an empty BLOB rather than SQL NULL when the input slice is nil.
	if frame == nil {
		frame = []byte{}
	}
	sum := sha256.Sum256(frame)
	id := hex.EncodeToString(sum[:])
	_, err := s.DB.ExecContext(ctx, `INSERT INTO received_telegrams(frame_sha256,frame,received_ms,last_received_ms)
 VALUES(?,?,?,?) ON CONFLICT(frame_sha256) DO UPDATE SET
 last_received_ms=MAX(last_received_ms,excluded.last_received_ms),receptions=receptions+1`, id, frame, received.UnixMilli(), received.UnixMilli())
	return id, err
}

// CompleteFrame is deliberately separate from Report ingestion. If a process stops
// between the commits, pending frames remain available for idempotent historical replay.
func (s *Store) CompleteFrame(ctx context.Context, id, state, report string) error {
	if state != "normalized" && state != "ignored" && state != "rejected" {
		return ErrInvalid
	}
	result, err := s.DB.ExecContext(ctx, "UPDATE received_telegrams SET state=?,report_id=? WHERE frame_sha256=?", state, report, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("receipt not found")
	}
	return err
}
