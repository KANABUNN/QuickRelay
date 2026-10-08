package relay

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/model"
	"quakerelay/server/internal/store"
)

type Sender interface {
	Send(context.Context, apns.Request) (apns.Result, error)
}
type Worker struct {
	Store  *store.Store
	Sender Sender
	Logger *slog.Logger
	Now    func() time.Time
}

func Payload(r model.Report, p model.Preferences) ([]byte, error) {
	level := "active"
	sound := "default"
	if r.Attention() && p.TimeSensitiveEnabled {
		level = "time-sensitive"
	}
	if p.CustomSoundEnabled {
		sound = "normal.caf"
		if r.Attention() {
			sound = "quake_update.caf"
			if r.Warning || r.Cancelled {
				sound = "quake_warning.caf"
			}
		}
	}
	// Bound display text so a valid upstream headline cannot exceed APNs' 4 KB limit.
	trim := func(s string, n int) string {
		r := []rune(s)
		if len(r) > n {
			return string(r[:n]) + "…"
		}
		return s
	}
	payload := map[string]any{
		"aps": map[string]any{"alert": map[string]string{"title": trim(r.Title, 120), "body": trim(r.Body, 600)},
			"sound": sound, "interruption-level": level, "thread-id": r.EventID},
		"category": r.CategoryName(), "event_id": r.EventID, "report_id": r.ID, "server_sequence": r.ServerSequence, "eventId": r.EventID, "serial": r.Serial, "kind": r.Classification,
		"cancel": r.Cancelled, "final": r.Final, "warning": r.Warning,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(b) > 4096 {
		return nil, errors.New("payload exceeds APNs limit")
	}
	return b, nil
}
func (w *Worker) Step(ctx context.Context) (bool, error) {
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	d, err := w.Store.Claim(ctx, now)
	if err != nil || d == nil {
		return false, err
	}
	finish := func(status, reason string, next time.Time) (bool, error) {
		return true, w.Store.Finish(ctx, *d, status, reason, next)
	}
	// Also reject legacy jobs queued by a previous binary before the upgrade.
	if !d.Report.PushEligible() {
		return finish("skipped", "history_only_product", now)
	}
	device, err := w.Store.Device(ctx, d.DeviceID)
	if err != nil {
		return true, err
	}
	if !device.Active || !device.PushActive || !device.Preferences.NotificationsEnabled ||
		!slices.Contains(device.Preferences.EventTypes, d.Report.EventType) {
		return finish("skipped", "device_or_preference_disabled", now)
	}
	stale, err := w.Store.Superseded(ctx, d.Report, d.Attempts > 1)
	if err != nil {
		return true, err
	}
	if stale {
		return finish("skipped", "superseded", now)
	}
	body, err := Payload(d.Report, device.Preferences)
	if err != nil {
		return finish("failed", "invalid_payload", now)
	}
	expiry := d.Expires
	// EEW should never be stored by APNs for delivery to an offline device.
	if d.Report.IsEEW() {
		expiry = time.Time{}
	}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	result, sendErr := w.Sender.Send(sendCtx, apns.Request{Token: device.Token, Environment: device.Environment,
		Payload: body, Expiration: expiry, Priority: 10, ID: d.APNsID})
	cancel()
	if result.Accepted() && sendErr == nil {
		return finish("accepted", "", now)
	}
	if result.InvalidToken() {
		if err = w.Store.InvalidateToken(ctx, device, result.Timestamp); err != nil {
			return true, err
		}
		return finish("invalid_token", safeReason(result.Reason), now)
	}
	if result.Retryable() {
		delay := time.Second * time.Duration(1<<min(d.Attempts, 6))
		// Apple specifies a 15-minute delay for 5xx responses. EEW will expire first.
		if result.Status >= 500 {
			delay = 15 * time.Minute
		}
		delay = max(delay, result.RetryAfter)
		delay += time.Duration(rand.Int64N(int64(time.Second)))
		next := now.Add(delay)
		if !next.Before(d.Expires) {
			return finish("expired", "retry_after_expiry", now)
		}
		return finish("retry", safeReason(result.Reason), next)
	}
	reason := safeReason(result.Reason)
	if sendErr != nil && reason == "" {
		reason = "invalid_response"
	}
	if w.Logger != nil {
		w.Logger.Error("APNs delivery rejected", "status", result.Status, "reason", reason)
	}
	return finish("failed", reason, now)
}
func safeReason(s string) string {
	if len(s) > 100 {
		return "provider_error"
	}
	if strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") != "" {
		return "provider_error"
	}
	return s
}
func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := w.Step(ctx)
		if err != nil && w.Logger != nil {
			w.Logger.Error("delivery worker operation failed")
		}
		if err == nil && worked {
			continue
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
