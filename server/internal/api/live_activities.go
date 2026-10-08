package api

import (
	"net/http"
	"time"
)

func (s *Server) liveStartToken(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		PushToken string `json:"push_token"`
	}
	if err := decode(w, r, &body); err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.RegisterLiveStartToken(r.Context(), id, body.PushToken, time.Now()); err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}
func (s *Server) liveActivityToken(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		EventID       string `json:"event_id"`
		TelegramType  string `json:"telegram_type"`
		ActivityID    string `json:"activity_id"`
		PushToken     string `json:"push_token"`
		StartSequence int64  `json:"start_sequence"`
	}
	if err := decode(w, r, &body); err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.RegisterLiveActivityToken(r.Context(), id, body.EventID, body.TelegramType, body.ActivityID, body.PushToken, body.StartSequence, time.Now()); err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}
func (s *Server) liveActivityEnded(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		EventID       string `json:"event_id"`
		TelegramType  string `json:"telegram_type"`
		ActivityID    string `json:"activity_id"`
		StartSequence int64  `json:"start_sequence"`
		Dismissed     bool   `json:"dismissed"`
	}
	if err := decode(w, r, &body); err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.EndLiveActivity(r.Context(), id, body.EventID, body.TelegramType, body.ActivityID, body.StartSequence, body.Dismissed); err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}

func (s *Server) clearLiveStartToken(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := s.Store.DB.ExecContext(r.Context(), "DELETE FROM live_activity_start_tokens WHERE device_id=?", id); err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}
