package api

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/relay"
	"quakerelay/server/internal/store"
	"time"
)

type NotificationTestSender interface {
	Send(context.Context, apns.Request) (apns.Result, error)
}

func (s *Server) requestNotificationTest(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		RequestID string `json:"request_id"`
		Style     string `json:"style"`
	}
	if err := decode(w, r, &body); err != nil {
		fail(w, err)
		return
	}
	d, err := s.Store.Device(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if !d.Active || !d.PushActive || !d.Preferences.NotificationsEnabled {
		reply(w, 409, map[string]any{"ok": false, "error": map[string]string{"code": "notifications_disabled", "message": "通知をオンにしてAPNs登録を完了してください。"}})
		return
	}
	if !s.APNsConfigured || s.TestSender == nil {
		reply(w, 503, map[string]any{"ok": false, "error": map[string]string{"code": "push_unavailable", "message": "サーバーの通知設定を確認してください。"}})
		return
	}
	test, created, err := s.Store.ReserveNotificationTest(r.Context(), id, body.RequestID, body.Style, time.Now())
	if errors.Is(err, store.ErrRateLimited) {
		w.Header().Set("Retry-After", "60")
		reply(w, 429, map[string]any{"ok": false, "error": map[string]string{"code": "test_rate_limited", "message": "テスト通知は1分に1回です。時間をおいて再試行してください。"}})
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	if created {
		payload, err := relay.NotificationTestPayload(test, d.Preferences)
		if err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 9*time.Second)
		result, sendErr := s.TestSender.Send(ctx, apns.Request{Token: d.Token, Environment: d.Environment, Payload: payload,
			Priority: 10, Expiration: time.Now().Add(time.Minute),
			ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("notification-test:"+id+":"+test.ID)).String()})
		cancel()
		test.Status = "rejected"
		if sendErr != nil {
			test.Status = "result_unknown"
		} else if result.Accepted() {
			test.Status = "apns_accepted"
		}
		// A disconnected client must not leave an accepted result unrecorded.
		saveCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		err = s.Store.FinishNotificationTest(saveCtx, id, test.ID, test.Status, time.Now())
		stop()
		if err != nil {
			fail(w, err)
			return
		}
	}
	reply(w, 200, map[string]any{"ok": true, "test": test, "cooldown_seconds": 60})
}

func (s *Server) notificationTest(w http.ResponseWriter, r *http.Request, id string) {
	test, err := s.Store.NotificationTest(r.Context(), id, r.PathValue("test"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true, "test": test, "cooldown_seconds": 60})
}
