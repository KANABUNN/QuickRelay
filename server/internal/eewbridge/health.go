package eewbridge

import (
	"context"
	"sync"
	"time"
)

const (
	Healthy  = "healthy"
	Degraded = "degraded"
	// An observation older than this no longer counts as healthy.
	healthTTL = 10 * time.Second
)

// Health tracks the two conditions clients need before new speech: the relay's
// DMDATA reception (upstream) and the bridge's own hand-off intake (capture).
type Health struct {
	mu         sync.Mutex
	upstreamOK bool
	upstreamAt time.Time
	captureAt  time.Time
	last       [2]string
	changed    signal
}

// Observe records a successful hand-off read and the relay's reception state.
func (h *Health) Observe(upstreamHealthy bool, now time.Time) {
	h.mu.Lock()
	h.upstreamOK, h.upstreamAt, h.captureAt = upstreamHealthy, now, now
	h.mu.Unlock()
	h.refresh(now)
}

// Fail records a failed hand-off read; both conditions become unknown.
func (h *Health) Fail(now time.Time) {
	h.mu.Lock()
	h.upstreamOK, h.upstreamAt, h.captureAt = false, time.Time{}, time.Time{}
	h.mu.Unlock()
	h.refresh(now)
}

// Status returns upstream and capture as healthy or degraded.
func (h *Health) Status(now time.Time) (upstream, capture string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status(now)
}

func (h *Health) status(now time.Time) (string, string) {
	fresh := func(at time.Time) bool { return !at.IsZero() && now.Sub(at) < healthTTL && now.Sub(at) >= -time.Second }
	upstream, capture := Degraded, Degraded
	if h.upstreamOK && fresh(h.upstreamAt) {
		upstream = Healthy
	}
	if fresh(h.captureAt) {
		capture = Healthy
	}
	return upstream, capture
}

// Changed is closed by the next change of Status.
func (h *Health) Changed() <-chan struct{} { return h.changed.wait() }

func (h *Health) refresh(now time.Time) {
	h.mu.Lock()
	up, capture := h.status(now)
	moved := h.last != [2]string{up, capture}
	h.last = [2]string{up, capture}
	h.mu.Unlock()
	if moved {
		h.changed.fire()
	}
}

// Run re-evaluates expiry once a second until ctx ends.
func (h *Health) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.refresh(now)
		}
	}
}
