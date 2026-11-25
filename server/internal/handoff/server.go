// Package handoff serves committed EEW hand-off rows to the EEW bridge over a
// Unix domain socket. It only reads; the bridge never receives database files,
// DMDATA or APNs credentials. See docs/eew-handoff.md for the contract.
package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/store"
)

const (
	DefaultLimit    = 64
	MaxLimit        = 256
	DefaultMaxBytes = 512 << 10
	MaxMaxBytes     = 4 << 20
	MaxWait         = 25 * time.Second
	// Concurrent batch reads share the relay's single database connection with ingestion.
	maxConcurrent = 2
)

// Upstream is the DMDATA client state; nil when reception is not configured.
type Upstream interface {
	Snapshot() dmdata.Stats
	Generation() string
}

type Server struct {
	Store  *store.Store
	Source Upstream
	mux    *http.ServeMux
	slots  chan struct{}
}

func New(st *store.Store, source Upstream) *Server {
	s := &Server{Store: st, Source: source, mux: http.NewServeMux(), slots: make(chan struct{}, maxConcurrent)}
	s.mux.HandleFunc("GET /v1/handoff", s.batch)
	s.mux.HandleFunc("GET /v1/upstream", s.upstreamHandler)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

type UpstreamState struct {
	Configured  bool    `json:"configured"`
	Connected   bool    `json:"connected"`
	Fresh       bool    `json:"fresh"`
	Generation  string  `json:"generation,omitempty"`
	LastFrameAt *string `json:"last_frame_at"`
	LastDataAt  *string `json:"last_data_at"`
	Reconnects  uint64  `json:"reconnects"`
	ServerTime  string  `json:"server_time"`
}

type Item struct {
	Seq            int64                 `json:"ingress_seq"`
	ReceiptID      string                `json:"receipt_id"`
	ReportSequence int64                 `json:"report_sequence"`
	ReportID       string                `json:"report_id"`
	TelegramType   string                `json:"telegram_type"`
	ReceivedAt     string                `json:"received_at"`
	CommittedAt    string                `json:"committed_at"`
	Upstream       string                `json:"upstream_generation"`
	Applied        *store.HandoffApplied `json:"applied"`
	// Exact frame bytes; SHA-256 of them equals receipt_id.
	Frame []byte `json:"frame"`
}

type Batch struct {
	Items         []Item        `json:"items"`
	Latest        int64         `json:"latest"`
	PurgedThrough int64         `json:"purged_through"`
	Gap           bool          `json:"gap"`
	More          bool          `json:"more"`
	Upstream      UpstreamState `json:"upstream"`
}

func stamp(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	v := store.Time(t)
	return &v
}

func (s *Server) upstream(now time.Time) UpstreamState {
	out := UpstreamState{ServerTime: store.Time(now)}
	if s.Source == nil {
		return out
	}
	stats := s.Source.Snapshot()
	elapsed := now.Sub(stats.LastFrameAt)
	out.Configured, out.Connected = true, stats.Connected
	// Same freshness rule as /readyz: DMDATA pings at least every 100 seconds.
	out.Fresh = stats.Connected && !stats.LastFrameAt.IsZero() && elapsed >= 0 && elapsed < 100*time.Second
	out.Generation = s.Source.Generation()
	out.LastFrameAt, out.LastDataAt, out.Reconnects = stamp(stats.LastFrameAt), stamp(stats.LastDataAt), stats.Reconnects
	return out
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, code string) {
	reply(w, status, map[string]string{"error": code})
}

func (s *Server) upstreamHandler(w http.ResponseWriter, r *http.Request) {
	reply(w, http.StatusOK, s.upstream(time.Now()))
}

func intParam(r *http.Request, name string, fallback, low, high int64) (int64, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	return v, err == nil && v >= low && v <= high
}

// batch returns rows after ?after=. With ?wait_ms= it waits for the next commit
// when nothing is available yet. The database read completes before writing.
func (s *Server) batch(w http.ResponseWriter, r *http.Request) {
	after, ok1 := intParam(r, "after", 0, 0, 1<<62)
	limit, ok2 := intParam(r, "limit", DefaultLimit, 1, MaxLimit)
	maxBytes, ok3 := intParam(r, "max_bytes", DefaultMaxBytes, 1, MaxMaxBytes)
	waitMS, ok4 := intParam(r, "wait_ms", 0, 0, MaxWait.Milliseconds())
	receipt := r.URL.Query().Get("after_receipt")
	if !ok1 || !ok2 || !ok3 || !ok4 || len(receipt) > 64 {
		failure(w, http.StatusBadRequest, "invalid_parameters")
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		failure(w, http.StatusTooManyRequests, "busy")
		return
	}
	ctx := r.Context()
	deadline := time.Now().Add(time.Duration(waitMS) * time.Millisecond)
	for {
		changed := s.Store.HandoffChanged()
		b, err := s.Store.HandoffBatch(ctx, after, receipt, int(limit), int(maxBytes))
		switch {
		case errors.Is(err, store.ErrHandoffCursor):
			failure(w, http.StatusConflict, "cursor_mismatch")
			return
		case err != nil:
			if ctx.Err() == nil {
				failure(w, http.StatusInternalServerError, "unavailable")
			}
			return
		}
		remaining := time.Until(deadline)
		if len(b.Items) > 0 || b.Gap || remaining <= 0 {
			reply(w, http.StatusOK, s.encode(b))
			return
		}
		if !wait(ctx, changed, remaining) {
			return
		}
	}
}

// wait reports false when the request was abandoned.
func wait(ctx context.Context, changed <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-changed:
	case <-timer.C:
	case <-ctx.Done():
		return false
	}
	return true
}

func (s *Server) encode(b store.HandoffBatch) Batch {
	out := Batch{Items: make([]Item, 0, len(b.Items)), Latest: b.Latest, PurgedThrough: b.PurgedThrough, Gap: b.Gap, More: b.More,
		Upstream: s.upstream(time.Now())}
	for _, item := range b.Items {
		out.Items = append(out.Items, Item{Seq: item.Seq, ReceiptID: item.ReceiptID, ReportSequence: item.ReportSequence, ReportID: item.ReportID,
			TelegramType: item.TelegramType, ReceivedAt: store.Time(item.ReceivedAt), CommittedAt: store.Time(item.CommittedAt),
			Upstream: item.Upstream, Applied: item.Applied, Frame: item.Frame})
	}
	return out
}
