package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/model"
	"quakerelay/server/internal/store"
)

type SourceSnapshotter interface{ Snapshot() dmdata.Stats }

type Server struct {
	TestSender             NotificationTestSender
	Source                 SourceSnapshotter
	Store                  *store.Store
	PairingSecret          string
	APNsConfigured         bool
	APNsEnvironmentAllowed func(string) bool
	mux                    *http.ServeMux
	mu                     sync.Mutex
	pairWindow             time.Time
	pairCount              int
}

func New(st *store.Store, secret string) *Server {
	s := &Server{Store: st, PairingSecret: secret, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/v1/health", s.health)
	s.mux.HandleFunc("POST /api/v1/pair/complete", s.pair)
	s.mux.HandleFunc("POST /api/v1/devices/register", s.auth(s.register))
	s.mux.HandleFunc("PUT /api/v1/devices/me", s.auth(s.register))
	s.mux.HandleFunc("GET /api/v1/devices/me", s.auth(s.device))
	s.mux.HandleFunc("DELETE /api/v1/devices/me", s.auth(s.revoke))
	s.mux.HandleFunc("PATCH /api/v1/devices/me/preferences", s.auth(s.preferences))
	s.mux.HandleFunc("POST /api/v1/devices/me/notification-tests", s.auth(s.requestNotificationTest))
	s.mux.HandleFunc("GET /api/v1/devices/me/notification-tests/{test}", s.auth(s.notificationTest))
	s.mux.HandleFunc("PUT /api/v1/devices/me/live-activity/start-token", s.auth(s.liveStartToken))
	s.mux.HandleFunc("DELETE /api/v1/devices/me/live-activity/start-token", s.auth(s.clearLiveStartToken))
	s.mux.HandleFunc("PUT /api/v1/devices/me/live-activity/token", s.auth(s.liveActivityToken))
	s.mux.HandleFunc("POST /api/v1/devices/me/live-activity/ended", s.auth(s.liveActivityEnded))
	s.HistoryRoutes()
	s.StatusRoutes()
	return s
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	status := 500
	code := "internal_error"
	message := "Server operation failed."
	switch {
	case errors.Is(err, store.ErrUnauthorized):
		status = 401
		code = "unauthorized"
		message = "Authentication required."
	case errors.Is(err, store.ErrInvalid):
		status = 400
		code = "invalid_request"
		message = "Invalid request."
	case errors.Is(err, store.ErrNotFound):
		status = 404
		code = "not_found"
		message = "Not found."
	}
	reply(w, status, map[string]any{"ok": false, "error": map[string]string{"code": code, "message": message}})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return store.ErrInvalid
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return store.ErrInvalid
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return store.ErrInvalid
	}
	return nil
}
func (s *Server) auth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			fail(w, store.ErrUnauthorized)
			return
		}
		id, err := s.Store.Authenticate(r.Context(), strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			fail(w, err)
			return
		}
		next(w, r, id)
	}
}
func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if time.Since(s.pairWindow) > time.Minute {
		s.pairWindow = time.Now()
		s.pairCount = 0
	}
	s.pairCount++
	limited := s.pairCount > 10
	s.mu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "60")
		reply(w, 429, map[string]any{"ok": false})
		return
	}
	var body struct {
		Code string `json:"pairing_code"`
		ID   string `json:"installation_id"`
	}
	if err := decode(w, r, &body); err != nil {
		fail(w, err)
		return
	}
	token, err := s.Store.Pair(r.Context(), body.Code, body.ID, s.PairingSecret, time.Now())
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true, "device_access_token": token, "expires_at": nil})
}
func (s *Server) register(w http.ResponseWriter, r *http.Request, id string) {
	var body model.Registration
	if err := decode(w, r, &body); err != nil {
		fail(w, err)
		return
	}
	if s.APNsEnvironmentAllowed != nil && !s.APNsEnvironmentAllowed(body.Environment) {
		reply(w, 400, map[string]any{"ok": false, "error": map[string]string{"code": "apns_environment_unavailable", "message": "This APNs environment is not configured on the server."}})
		return
	}
	if err := s.Store.Register(r.Context(), id, body, time.Now()); err != nil {
		fail(w, err)
		return
	}
	s.device(w, r, id)
}
func (s *Server) device(w http.ResponseWriter, r *http.Request, id string) {
	d, err := s.Store.Device(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true, "device": d})
}
func (s *Server) revoke(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.Store.Revoke(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}
func (s *Server) preferences(w http.ResponseWriter, r *http.Request, id string) {
	var p struct {
		NotificationsEnabled  *bool     `json:"notifications_enabled"`
		TimeSensitiveEnabled  *bool     `json:"time_sensitive_enabled"`
		CustomSoundEnabled    *bool     `json:"custom_sound_enabled"`
		EventTypes            *[]string `json:"event_types"`
		EarthquakeRegions     *[]string `json:"earthquake_regions"`
		TsunamiRegions        *[]string `json:"tsunami_regions"`
		MinimumIntensity      *string   `json:"minimum_intensity"`
		LiveActivitiesEnabled *bool     `json:"live_activities_enabled"`
	}
	if err := decode(w, r, &p); err != nil {
		fail(w, err)
		return
	}
	d, err := s.Store.Device(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if p.NotificationsEnabled != nil {
		d.Preferences.NotificationsEnabled = *p.NotificationsEnabled
	}
	if p.TimeSensitiveEnabled != nil {
		d.Preferences.TimeSensitiveEnabled = *p.TimeSensitiveEnabled
	}
	if p.CustomSoundEnabled != nil {
		d.Preferences.CustomSoundEnabled = *p.CustomSoundEnabled
	}
	if p.EventTypes != nil {
		d.Preferences.EventTypes = *p.EventTypes
	}
	if p.EarthquakeRegions != nil {
		d.Preferences.EarthquakeRegions = *p.EarthquakeRegions
	}
	if p.TsunamiRegions != nil {
		d.Preferences.TsunamiRegions = *p.TsunamiRegions
	}
	if p.MinimumIntensity != nil {
		d.Preferences.MinimumIntensity = *p.MinimumIntensity
	}
	if p.LiveActivitiesEnabled != nil {
		d.Preferences.LiveActivitiesEnabled = *p.LiveActivitiesEnabled
	}
	if err := s.Store.Preferences(r.Context(), id, d.Preferences); err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true, "preferences": d.Preferences})
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ok := s.Store.DB.PingContext(r.Context()) == nil
	status := 200
	if !ok {
		status = 503
	}
	reply(w, status, map[string]any{"ok": ok, "version": "vps-1", "db": ok, "apns_http2": s.APNsConfigured, "config_loaded": true, "migration_current": ok, "time": store.Time(time.Now())})
}
