package eewbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"quakerelay/server/internal/eew"
	"quakerelay/server/internal/handoff"
)

// Feeder follows the relay hand-off socket and applies each committed EEW
// frame, in order, to the bridge store.
type Feeder struct {
	Store  *Store
	Health *Health
	Logger *slog.Logger
	// Socket is the relay hand-off socket path.
	Socket string
	// Wait is the long-poll duration; it bounds how stale upstream health can be.
	Wait   time.Duration
	client *http.Client
	// BaseURL replaces the socket in tests.
	BaseURL string
}

func (f *Feeder) httpClient() *http.Client {
	if f.client == nil {
		transport := &http.Transport{MaxIdleConns: 1, IdleConnTimeout: time.Minute}
		if f.BaseURL == "" {
			transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", f.Socket)
			}
		}
		f.client = &http.Client{Transport: transport, Timeout: f.wait() + 15*time.Second}
	}
	return f.client
}

func (f *Feeder) wait() time.Duration {
	if f.Wait <= 0 {
		return 4 * time.Second
	}
	return f.Wait
}

// Run follows the hand-off until ctx ends, reconnecting with backoff.
func (f *Feeder) Run(ctx context.Context) {
	delay := 500 * time.Millisecond
	for ctx.Err() == nil {
		started := time.Now()
		err := f.follow(ctx)
		if ctx.Err() != nil {
			return
		}
		f.Health.Fail(time.Now())
		f.Logger.Warn("EEW hand-off unavailable", "error", err.Error())
		if time.Since(started) > time.Minute {
			delay = 500 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 5*time.Second)
	}
}

var errCursorMismatch = errors.New("relay hand-off history changed")

func (f *Feeder) fetch(ctx context.Context, cursor HandoffCursor) (handoff.Batch, error) {
	var b handoff.Batch
	base := f.BaseURL
	if base == "" {
		base = "http://handoff"
	}
	q := url.Values{"after": {strconv.FormatInt(cursor.Seq, 10)}, "wait_ms": {strconv.FormatInt(f.wait().Milliseconds(), 10)}}
	if cursor.ReceiptID != "" {
		q.Set("after_receipt", cursor.ReceiptID)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/v1/handoff?"+q.Encode(), nil)
	if err != nil {
		return b, err
	}
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return b, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return b, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusConflict:
		return b, errCursorMismatch
	default:
		return b, fmt.Errorf("hand-off HTTP %d", resp.StatusCode)
	}
	if err = json.Unmarshal(body, &b); err != nil {
		return b, errors.New("invalid hand-off response")
	}
	return b, nil
}

// follow reads batches until an error. Rows up to the latest position seen at
// the first read are recovered catch-up; only later rows are live.
func (f *Feeder) follow(ctx context.Context) error {
	boundary := int64(-1)
	rotate := false
	for {
		cursor, err := f.Store.Cursor(ctx)
		if err != nil {
			return err
		}
		b, err := f.fetch(ctx, cursor)
		if errors.Is(err, errCursorMismatch) {
			// Restored relay database: start over from its retained rows and
			// make clients replace their state.
			f.Logger.Warn("EEW hand-off cursor does not match the relay; restarting from its retained rows", "seq", cursor.Seq)
			if err = f.Store.SetCursor(ctx, 0, ""); err != nil {
				return err
			}
			boundary, rotate = -1, true
			continue
		}
		if err != nil {
			return err
		}
		now := time.Now()
		f.Health.Observe(b.Upstream.Configured && b.Upstream.Connected && b.Upstream.Fresh, now)
		if boundary < 0 {
			boundary = b.Latest
		}
		if gen := b.Upstream.Generation; gen != "" && gen != cursor.Generation {
			if cursor.Generation != "" {
				// Reports may have been missed while DMDATA reconnected.
				if _, err = f.Store.Reconcile(ctx, now, false); err != nil {
					return err
				}
				f.Logger.Info("DMDATA connection changed; active warnings marked partial", "generation", gen)
			}
			if err = f.Store.SetGeneration(ctx, gen); err != nil {
				return err
			}
		}
		if b.Gap || rotate {
			if _, err = f.Store.Reconcile(ctx, now, true); err != nil {
				return err
			}
			f.Logger.Warn("EEW hand-off rows were missed; new delivery epoch", "after", cursor.Seq, "purged_through", b.PurgedThrough)
			if b.Gap {
				if err = f.Store.SetCursor(ctx, b.PurgedThrough, ""); err != nil {
					return err
				}
			}
			rotate = false
		}
		for _, item := range b.Items {
			origin := eew.OriginLive
			if item.Seq <= boundary {
				origin = eew.OriginRecovered
			}
			var relay *eew.RelayState
			if item.Applied != nil {
				relay = &eew.RelayState{Serial: item.Applied.Serial, Cancelled: item.Applied.Cancelled, Final: item.Applied.Final}
			}
			received, err := time.Parse(time.RFC3339Nano, item.ReceivedAt)
			if err != nil {
				return fmt.Errorf("hand-off row %d: invalid reception time", item.Seq)
			}
			out, err := f.Store.Process(ctx, Input{Seq: item.Seq, ReceiptID: item.ReceiptID, Frame: item.Frame, ReceivedAt: received,
				Upstream: item.Upstream, Origin: origin, Relay: relay}, time.Now())
			if err != nil {
				return fmt.Errorf("hand-off row %d: %w", item.Seq, err)
			}
			f.Logger.Info("EEW input", "ingress_seq", item.Seq, "telegram", out.TelegramID, "event", out.EventID, "origin", origin,
				"outcome", out.Outcome, "reason", out.Reason, "log_seq", out.LogSeq,
				"received_to_commit_ms", time.Since(received).Milliseconds())
		}
	}
}
