package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"quakerelay/server/internal/dmdata"
	"strconv"
	"time"

	"quakerelay/server/internal/store"
)

func (s *Server) HistoryRoutes() {
	s.mux.HandleFunc("GET /api/v1/reports/{id}/source", s.auth(s.sourceReport))
	s.mux.HandleFunc("GET /api/v1/sync", s.auth(s.syncHistory))
	s.mux.HandleFunc("GET /api/v1/events", s.auth(s.events))
	s.mux.HandleFunc("GET /api/v1/events/current", s.auth(s.events))
	s.mux.HandleFunc("GET /api/v1/events/{id}", s.auth(s.event))
}
func queryNumber(r *http.Request, key string, fallback int64) (int64, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, store.ErrInvalid
	}
	return n, nil
}
func pageLimit(r *http.Request) (int, error) {
	n, err := queryNumber(r, "limit", 100)
	if err != nil || n < 1 || n > 200 {
		return 0, store.ErrInvalid
	}
	return int(n), nil
}
func (s *Server) syncHistory(w http.ResponseWriter, r *http.Request, _ string) {
	after, err := queryNumber(r, "after_sequence", 0)
	if err != nil {
		fail(w, err)
		return
	}
	limit, err := pageLimit(r)
	if err != nil {
		fail(w, err)
		return
	}
	p, err := s.Store.Sync(r.Context(), after, limit)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, p)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request, _ string) {
	cursor, err := queryNumber(r, "cursor", 0)
	if err != nil {
		fail(w, err)
		return
	}
	limit, err := pageLimit(r)
	if err != nil {
		fail(w, err)
		return
	}
	events, err := s.Store.Events(r.Context(), cursor, limit+1)
	if err != nil {
		fail(w, err)
		return
	}
	var next *string
	if len(events) > limit {
		events = events[:limit]
		s := strconv.FormatInt(events[len(events)-1].LatestRevision, 10)
		next = &s
	}
	reply(w, 200, map[string]any{"ok": true, "items": events, "next_cursor": next, "server_time": store.Time(time.Now())})
}
func (s *Server) event(w http.ResponseWriter, r *http.Request, _ string) {
	id := r.PathValue("id")
	if !store.ValidID(id) {
		fail(w, store.ErrInvalid)
		return
	}
	event, reports, err := s.Store.Event(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true, "event": event, "reports": reports})
}

func (s *Server) sourceReport(w http.ResponseWriter, r *http.Request, _ string) {
	id := r.PathValue("id")
	if !store.ValidID(id) {
		fail(w, store.ErrInvalid)
		return
	}
	_, raw, err := s.Store.SourceReport(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	var e dmdata.Envelope
	if err = json.Unmarshal(raw, &e); err != nil {
		fail(w, err)
		return
	}
	data, err := dmdata.DecodeBody(e)
	if err != nil {
		fail(w, err)
		return
	}
	ext, mime := "bin", "application/octet-stream"
	switch e.Format {
	case "json":
		ext, mime = "json", "application/json"
	case "a/n":
		ext = "txt"
	case "binary":
		ext = "bufr"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, id, ext))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
