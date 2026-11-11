package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/model"
	"quakerelay/server/internal/store"
	"time"
)

// ActivityKit uses epoch seconds and the shared widget's Codable property names.
type LiveContent struct {
	Title         string `json:"title"`
	Summary       string `json:"summary"`
	StatusText    string `json:"statusText"`
	IntensityText string `json:"intensityText"`
	ReportLabel   string `json:"reportLabel"`
	Category      string `json:"category"`
	Warning       bool   `json:"warning"`
	EEWWarning    bool   `json:"eewWarning,omitempty"`
	Ended         bool   `json:"ended"`
	Cancelled     bool   `json:"cancelled"`
	ReportedAt    int64  `json:"reportedAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

func liveText(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
func liveContent(r model.Report, now time.Time, end bool, forced bool) LiveContent {
	state := LiveContent{Title: liveText(r.Title, 80), Summary: liveText(r.Body, 220), Category: r.CategoryName(),
		Warning: r.Warning, Ended: end, Cancelled: r.Cancelled, ReportedAt: r.ReportedAt.Unix(), UpdatedAt: now.Unix()}
	state.EEWWarning = r.TelegramType == "VXSE43" && !end && !r.Cancelled && !r.Final && now.Before(r.LiveActivityDeadline())
	state.StatusText = "続報を待機"
	state.ReportLabel = "発表"
	if r.IsEEW() && r.Serial != nil {
		state.ReportLabel = fmt.Sprintf("第%d報", *r.Serial)
	}
	if r.MaxIntensity != nil {
		state.IntensityText = "最大震度 " + *r.MaxIntensity
	}
	if r.Hypocenter != nil && r.Hypocenter.Qualification() != "" {
		state.Summary = liveText(r.Hypocenter.Qualification()+" "+r.Body, 220)
	}
	switch {
	case forced:
		state.StatusText = "表示を終了"
	case r.Cancelled:
		state.StatusText = "取消（解除ではありません）"
	case r.IsEEW() && r.Final:
		state.StatusText = "受信終了（最終報）"
	case r.TelegramType == "VTSE41" && !r.Warning:
		state.StatusText = "この発表に警報・注意報なし"
	case end:
		state.StatusText = "表示を終了"
	case r.TelegramType == "VTSE41":
		state.StatusText = "津波警報・注意報"
	}
	return state
}
func LivePayload(r model.Report, p model.Preferences, event string, now time.Time, forced bool) ([]byte, error) {
	end := event == "end"
	aps := map[string]any{"timestamp": now.Unix(), "event": event, "content-state": liveContent(r, now, end, forced)}
	stale := now.Add(time.Minute)
	switch r.TelegramType {
	case "VXSE43":
		stale = r.LiveActivityDeadline()
	case "VTSE41":
		stale = now.Add(15 * time.Minute)
	}
	if end {
		aps["dismissal-date"] = now.Add(5 * time.Minute).Unix()
	} else {
		aps["stale-date"] = stale.Unix()
	}
	payload := map[string]any{"aps": aps, "category": r.CategoryName(), "event_id": r.EventID, "report_id": r.ID,
		"server_sequence": r.ServerSequence}
	if event == "start" {
		// Apple requires an alert on push-to-start. Reuse the primary delivery,
		// rather than sending an additional ordinary alert for this report.
		ordinary, err := Payload(r, p)
		if err != nil {
			return nil, err
		}
		var base map[string]any
		if err = json.Unmarshal(ordinary, &base); err != nil {
			return nil, err
		}
		normal := base["aps"].(map[string]any)
		alert := normal["alert"].(map[string]any)
		alert["sound"] = normal["sound"]
		aps["alert"] = alert
		aps["interruption-level"] = normal["interruption-level"]
		aps["attributes-type"] = "QuickRelayActivityAttributes"
		aps["attributes"] = map[string]any{"eventID": r.EventID, "telegramType": r.LiveActivityKey(), "startSequence": r.ServerSequence}
	}
	b, err := json.Marshal(payload)
	if err == nil && len(b) > 4096 {
		return nil, errors.New("live payload exceeds APNs limit")
	}
	return b, err
}

func (w *Worker) liveStart(ctx context.Context, d store.Delivery, device model.Device, now time.Time) (bool, error) {
	if !device.Preferences.LiveActivitiesEnabled {
		return false, nil
	}
	existing, lookupErr := w.Store.LiveActivity(ctx, d.DeviceID, d.Report.EventID, d.Report.LiveActivityKey())
	if lookupErr == nil && existing.StartSequence == d.Report.ServerSequence && existing.LastSequence >= d.Report.ServerSequence && existing.LastTimestamp > 0 {
		return true, nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, store.ErrNotFound) {
		return false, lookupErr
	}
	token, err := w.Store.ReserveLiveStart(ctx, d.DeviceID, d.Report, now)
	if err != nil || token == "" {
		return false, err
	}
	body, err := LivePayload(d.Report, device.Preferences, "start", now, false)
	if err != nil {
		// An optional widget payload must not prevent the ordinary alert.
		return false, w.Store.FinishLiveStart(ctx, d.DeviceID, d.Report, token, false, false, now)
	}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	result, sendErr := w.Sender.Send(sendCtx, apns.Request{Token: token, Environment: device.Environment, Payload: body,
		PushType: "liveactivity", Priority: 10, ID: d.APNsID})
	cancel()
	accepted := result.Accepted() && sendErr == nil
	if err = w.Store.FinishLiveStart(ctx, d.DeviceID, d.Report, token, accepted, result.InvalidToken(), now); err != nil {
		// If Apple accepted, never create a second alert merely because recording
		// the acceptance failed. The ordinary outbox's lease handles recovery.
		return accepted, err
	}
	return accepted, nil
}

// Both streams can wake the same activity. Prefer a current, eligible warning
// without letting one stream's cancellation cancel the other stream.
func eewLiveDisplay(forecast, warning model.Report, p model.Preferences, now time.Time) (model.Report, bool) {
	if forecast.Final && !forecast.Cancelled {
		return forecast, true
	}
	for _, report := range []model.Report{warning, forecast} {
		if report.StartsLiveActivity() && now.Before(report.LiveActivityDeadline()) && p.Allows(report, false) {
			return report, false
		}
	}
	if forecast.ServerSequence == 0 || warning.EndsNotificationLifecycle() && warning.ServerSequence > forecast.ServerSequence {
		return warning, true
	}
	return forecast, true
}

func (w *Worker) LiveStep(ctx context.Context) (bool, error) {
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	job, err := w.Store.ClaimLiveJob(ctx, now)
	if err != nil || job == nil {
		return false, err
	}
	finish := func(status, reason string, next time.Time, ended bool, timestamp int64) (bool, error) {
		return true, w.Store.FinishLiveJob(ctx, *job, status, reason, next, ended, timestamp)
	}
	key := job.Report.LiveActivityKey()
	live, err := w.Store.LiveActivity(ctx, job.DeviceID, job.Report.EventID, key)
	if errors.Is(err, store.ErrNotFound) {
		return finish("skipped", "not_started", now, false, 0)
	}
	if err != nil {
		return true, err
	}
	job.ActivityID, job.Token = live.ActivityID, live.Token
	if live.State == "ended" || live.State == "dismissed" {
		return finish("skipped", "already_ended", now, false, 0)
	}
	device, err := w.Store.Device(ctx, job.DeviceID)
	if err != nil {
		return true, err
	}
	report := job.Report
	forced := job.ForceEnd || live.State == "ending"
	end := forced || !device.Active || !device.PushActive || !device.Preferences.LiveActivitiesEnabled || !device.Preferences.NotificationsEnabled
	job.Revision = job.Report.ServerSequence
	if !forced {
		if key == "VXSE45" {
			forecast, warning, err := w.Store.EEWLiveReports(ctx, job.Report.EventID)
			if err != nil {
				return true, err
			}
			var complete bool
			report, complete = eewLiveDisplay(forecast, warning, device.Preferences, now)
			end = end || complete
			job.Revision = max(forecast.ServerSequence, warning.ServerSequence)
		} else {
			latest, err := w.Store.Stream(ctx, job.Report.EventID, job.Report.TelegramType)
			if err != nil {
				return true, err
			}
			if latest.ServerSequence > job.Report.ServerSequence {
				return finish("skipped", "superseded", now, false, 0)
			}
			end = end || !device.Preferences.Allows(report, true) || report.EndsNotificationLifecycle()
		}
	}
	if !end && job.Revision <= live.LastSequence {
		return finish("skipped", "already_sent", now, false, 0)
	}
	if live.Token == "" {
		next := now.Add(time.Second)
		if !next.Before(job.Expires) {
			return finish("expired", "no_activity_token", now, false, 0)
		}
		return finish("retry", "awaiting_activity_token", next, false, 0)
	}
	if now.Unix() <= live.LastTimestamp {
		return finish("retry", "timestamp_order", time.Unix(live.LastTimestamp+1, 0), false, 0)
	}
	event := "update"
	if end {
		event = "end"
	}
	body, err := LivePayload(report, device.Preferences, event, now, forced)
	if err != nil {
		return finish("failed", "invalid_payload", now, false, 0)
	}
	// Silent update/end pushes have separate IDs from ordinary report alerts.
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("live:%d:%s:%s", job.ID, live.ActivityID, event))).String()
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	result, sendErr := w.Sender.Send(sendCtx, apns.Request{Token: live.Token, Environment: device.Environment,
		Payload: body, PushType: "liveactivity", Expiration: time.Time{}, Priority: 10, ID: id})
	cancel()
	if result.Accepted() && sendErr == nil {
		return finish("accepted", "", now, end, now.Unix())
	}
	if result.InvalidToken() {
		// Invalidate this activity only; its token is not the installation's
		// normal APNs token or push-to-start token.
		return finish("invalid_token", safeReason(result.Reason), now, true, 0)
	}
	if result.Retryable() || sendErr != nil {
		delay := time.Second * time.Duration(1<<min(job.Attempts, 6))
		if result.Status >= 500 {
			delay = 15 * time.Minute
		}
		next := now.Add(max(delay, result.RetryAfter))
		if !next.Before(job.Expires) {
			return finish("expired", "retry_after_expiry", now, false, 0)
		}
		return finish("retry", safeReason(result.Reason), next, false, 0)
	}
	return finish("failed", safeReason(result.Reason), now, false, 0)
}
func (w *Worker) RunLive(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := w.LiveStep(ctx)
		if err != nil && w.Logger != nil {
			w.Logger.Error("Live Activity worker operation failed")
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
