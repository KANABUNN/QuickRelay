package api

import (
	"fmt"
	"net/http"
	"time"

	"quakerelay/server/internal/dmdata"
)

func (s *Server) StatusRoutes() {
	s.mux.HandleFunc("GET /health", s.health)
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /readyz", s.ready)
	s.mux.HandleFunc("GET /api/v1/status", s.auth(s.status))
	s.mux.HandleFunc("GET /metrics", s.auth(s.metrics))
}
func (s *Server) source() dmdata.Stats {
	if s.Source == nil {
		return dmdata.Stats{}
	}
	return s.Source.Snapshot()
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	source := s.source()
	db := s.Store.DB.PingContext(r.Context()) == nil
	ready := db && s.APNsConfigured && source.Connected && time.Since(source.LastFrameAt) < 100*time.Second
	status := 200
	if !ready {
		status = 503
	}
	reply(w, status, map[string]any{"ok": ready, "db": db, "source_connected": source.Connected, "apns_configured": s.APNsConfigured})
}
func (s *Server) status(w http.ResponseWriter, r *http.Request, _ string) {
	counts, err := s.Store.DeliveryCounts(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true, "source": s.source(), "deliveries": counts, "apns_configured": s.APNsConfigured})
}
func (s *Server) metrics(w http.ResponseWriter, r *http.Request, _ string) {
	counts, err := s.Store.DeliveryCounts(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	state := s.source()
	connected := 0
	if state.Connected {
		connected = 1
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "quakerelay_dmdata_connected %d\nquakerelay_dmdata_reconnects_total %d\nquakerelay_dmdata_rejected_total %d\n", connected, state.Reconnects, state.Rejected)
	for _, status := range []string{"pending", "retry", "sending", "accepted", "failed", "expired", "invalid_token", "skipped"} {
		_, _ = fmt.Fprintf(w, "quakerelay_deliveries{status=%q} %d\n", status, counts[status])
	}
}
