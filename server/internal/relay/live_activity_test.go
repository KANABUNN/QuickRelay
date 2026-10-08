package relay

import (
	"context"
	"encoding/json"
	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/model"
	"quakerelay/server/internal/store"
	"testing"
	"time"
)

func enableLive(t *testing.T, st *store.Store, r model.Report) {
	t.Helper()
	ctx := context.Background()
	p := model.DefaultPreferences()
	p.LiveActivitiesEnabled = true
	if err := st.Preferences(ctx, "phone", p); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterLiveStartToken(ctx, "phone", "ccdd", r.ReportedAt); err != nil {
		t.Fatal(err)
	}
}
func ingestLive(t *testing.T, st *store.Store, r model.Report) model.Report {
	t.Helper()
	out, err := st.Ingest(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	r.ServerSequence = out.Sequence
	return r
}
func liveAPS(t *testing.T, r apns.Request) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p["aps"].(map[string]any)
}
func TestLiveStartReplacesPrimaryAlertAndUpdatesSilently(t *testing.T) {
	st, w, f, r := setup(t)
	ctx := context.Background()
	enableLive(t, st, r)
	r = ingestLive(t, st, r)
	if _, err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 || f.requests[0].PushType != "liveactivity" || f.requests[0].Token != "ccdd" {
		t.Fatal("start must be the single primary alert")
	}
	first := liveAPS(t, f.requests[0])
	if first["event"] != "start" || first["alert"] == nil || first["attributes-type"] != "QuickRelayActivityAttributes" {
		t.Fatal(first)
	}
	if err := st.RegisterLiveActivityToken(ctx, "phone", r.EventID, r.TelegramType, "activity1", "eeff", r.ServerSequence, r.ReportedAt); err != nil {
		t.Fatal(err)
	}
	n := 2
	next := r
	next.ID = "report2"
	next.MessageID = "message2"
	next.Serial = &n
	next.ReportedAt = r.ReportedAt.Add(time.Second)
	next.ReceivedAt = next.ReportedAt
	next = ingestLive(t, st, next)
	now := r.ReportedAt.Add(3 * time.Second)
	w.Now = func() time.Time { return now }
	if _, err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := w.LiveStep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.requests) != 3 {
		t.Fatalf("requests=%d, want start, ordinary report 2, silent update", len(f.requests))
	}
	update := liveAPS(t, f.requests[2])
	if update["event"] != "update" || update["alert"] != nil || update["sound"] != nil {
		t.Fatal("duplicate alert", update)
	}
	counts, _ := st.DeliveryCounts(ctx)
	if counts["accepted"] != 2 {
		t.Fatal(counts)
	}
	// A final report stays visible as final, never a false all-clear.
	final := next
	final.ID = "final"
	final.MessageID = "final-msg"
	final.Final = true
	final.ReportedAt = now
	final.ReceivedAt = now
	final = ingestLive(t, st, final)
	now = now.Add(2 * time.Second)
	if _, err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := w.LiveStep(ctx); err != nil {
		t.Fatal(err)
	}
	end := liveAPS(t, f.requests[len(f.requests)-1])
	if end["event"] != "end" || end["alert"] != nil {
		t.Fatal(end)
	}
	content := end["content-state"].(map[string]any)
	if content["statusText"] != "受信終了（最終報）" || content["ended"] != true {
		t.Fatal(content)
	}
	record, _ := st.LiveActivity(ctx, "phone", r.EventID, r.TelegramType)
	if record.State != "ended" || record.Token != "" {
		t.Fatal(record.State)
	}
}

type startRejected struct{ requests []apns.Request }

func (f *startRejected) Send(_ context.Context, r apns.Request) (apns.Result, error) {
	f.requests = append(f.requests, r)
	if r.PushType == "liveactivity" {
		return apns.Result{Status: 410, Reason: "Unregistered"}, nil
	}
	return apns.Result{Status: 200}, nil
}
func TestLiveStartFailureFallsBackWithoutInvalidatingNormalDevice(t *testing.T) {
	st, w, _, r := setup(t)
	enableLive(t, st, r)
	r = ingestLive(t, st, r)
	sender := &startRejected{}
	w.Sender = sender
	if _, err := w.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.requests) != 2 || sender.requests[1].PushType != "" || sender.requests[1].Token != "aabb" {
		t.Fatal("ordinary fallback missing")
	}
	d, _ := st.Device(context.Background(), "phone")
	if !d.PushActive {
		t.Fatal("normal token invalidated by ActivityKit rejection")
	}
	counts, _ := st.DeliveryCounts(context.Background())
	if counts["accepted"] != 1 {
		t.Fatal(counts)
	}
}
func TestLiveOptOutBeforeTokenArrivesStillEndsSilently(t *testing.T) {
	st, w, f, r := setup(t)
	ctx := context.Background()
	enableLive(t, st, r)
	r = ingestLive(t, st, r)
	if _, err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	p := model.DefaultPreferences()
	if err := st.Preferences(ctx, "phone", p); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterLiveActivityToken(ctx, "phone", r.EventID, r.TelegramType, "activity1", "eeff", r.ServerSequence, r.ReportedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	w.Now = func() time.Time { return r.ReportedAt.Add(2 * time.Second) }
	if _, err := w.LiveStep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 2 {
		t.Fatal("opt-out not ended")
	}
	aps := liveAPS(t, f.requests[1])
	if aps["event"] != "end" || aps["alert"] != nil {
		t.Fatal(aps)
	}
}
func TestLiveTokenGapCoalescesReportsAndCancelIsScoped(t *testing.T) {
	st, w, f, r := setup(t)
	ctx := context.Background()
	enableLive(t, st, r)
	r = ingestLive(t, st, r)
	if _, err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	n := 2
	newer := r
	newer.ID = "newer"
	newer.MessageID = "newer"
	newer.Serial = &n
	newer.ReportedAt = r.ReportedAt.Add(time.Second)
	newer.ReceivedAt = newer.ReportedAt
	newer = ingestLive(t, st, newer)
	// A warning with the same event and serial remains a separate ordinary alert.
	warning := newer
	warning.ID = "warning"
	warning.MessageID = "warning"
	warning.TelegramType = "VXSE43"
	warning.EventType = "eew_warning"
	warning.Classification = "eew.warning"
	warning.Warning = true
	warning = ingestLive(t, st, warning)
	if err := st.RegisterLiveActivityToken(ctx, "phone", r.EventID, r.TelegramType, "activity1", "eeff", r.ServerSequence, newer.ReportedAt); err != nil {
		t.Fatal(err)
	}
	w.Now = func() time.Time { return r.ReportedAt.Add(3 * time.Second) }
	for i := 0; i < 4; i++ {
		if _, err := w.LiveStep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.requests) != 2 || liveAPS(t, f.requests[1])["event"] != "update" {
		t.Fatal("latest stream not recovered")
	}
	// A dismissed activity is not recreated by late token registration.
	if err := st.EndLiveActivity(ctx, "phone", r.EventID, r.TelegramType, "activity1", r.ServerSequence, true); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterLiveActivityToken(ctx, "phone", r.EventID, r.TelegramType, "activity1", "eeff", r.ServerSequence, newer.ReportedAt); err != store.ErrInvalid {
		t.Fatal("late token resurrected", err)
	}
}
func TestLiveUpdateTokenRotationDoesNotEraseNewToken(t *testing.T) {
	st, _, _, r := setup(t)
	ctx := context.Background()
	enableLive(t, st, r)
	r = ingestLive(t, st, r)
	if _, err := st.ReserveLiveStart(ctx, "phone", r, r.ReportedAt); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterLiveActivityToken(ctx, "phone", r.EventID, r.TelegramType, "activity1", "aacc", r.ServerSequence, r.ReportedAt); err != nil {
		t.Fatal(err)
	}
	j, err := st.ClaimLiveJob(ctx, r.ReportedAt)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	j.Token, j.ActivityID = "aacc", "activity1"
	if err := st.RegisterLiveActivityToken(ctx, "phone", r.EventID, r.TelegramType, "activity1", "eeff", r.ServerSequence, r.ReportedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishLiveJob(ctx, *j, "invalid_token", "Unregistered", r.ReportedAt, true, 0); err != nil {
		t.Fatal(err)
	}
	live, _ := st.LiveActivity(ctx, "phone", r.EventID, r.TelegramType)
	if live.Token != "eeff" || live.State != "active" {
		t.Fatal("rotated token lost")
	}
}
func TestLiveDefaultsAndPayloadQualification(t *testing.T) {
	st, w, f, r := setup(t)
	ctx := context.Background()
	r = ingestLive(t, st, r)
	if _, err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if f.requests[0].PushType != "" {
		t.Fatal("opt-in default changed")
	}
	r.Hypocenter = &model.Hypocenter{Status: "assumed"}
	r.Cancelled = true
	b, err := LivePayload(r, model.DefaultPreferences(), "end", r.ReportedAt, false)
	if err != nil {
		t.Fatal(err)
	}
	aps := liveAPS(t, apns.Request{Payload: b})
	state := aps["content-state"].(map[string]any)
	if state["statusText"] != "取消（解除ではありません）" || state["summary"] == "テスト" {
		t.Fatal("qualification or cancellation lost", state)
	}
}

func TestCancellationDoesNotFollowItselfAndRetainsPreviouslySentStream(t *testing.T) {
	t.Run("never followed", func(t *testing.T) {
		st, w, f, r := setup(t)
		ctx := context.Background()
		p := model.DefaultPreferences()
		p.EventTypes = []string{"system_test"}
		if err := st.Preferences(ctx, "phone", p); err != nil {
			t.Fatal(err)
		}
		r.Cancelled = true
		r.Final = true
		r.EventType = "eew_cancel"
		r = ingestLive(t, st, r)
		if _, err := w.Step(ctx); err != nil {
			t.Fatal(err)
		}
		if len(f.requests) != 0 {
			t.Fatal("unfollowed cancellation bypassed settings")
		}
	})
	t.Run("followed before filter change", func(t *testing.T) {
		st, w, f, r := setup(t)
		ctx := context.Background()
		r = ingestLive(t, st, r)
		if _, err := w.Step(ctx); err != nil {
			t.Fatal(err)
		}
		p := model.DefaultPreferences()
		p.EventTypes = []string{"system_test"}
		if err := st.Preferences(ctx, "phone", p); err != nil {
			t.Fatal(err)
		}
		r.ID = "cancel"
		r.MessageID = "cancel"
		r.Cancelled = true
		r.Final = true
		r.EventType = "eew_cancel"
		r.ReportedAt = r.ReportedAt.Add(time.Second)
		r.ReceivedAt = r.ReportedAt
		r = ingestLive(t, st, r)
		w.Now = func() time.Time { return r.ReportedAt }
		if _, err := w.Step(ctx); err != nil {
			t.Fatal(err)
		}
		if len(f.requests) != 2 {
			t.Fatal("followed cancellation was lost")
		}
	})
}
func TestRecordedLiveStartRecoversPrimaryLeaseWithoutSecondAlert(t *testing.T) {
	st, w, f, r := setup(t)
	ctx := context.Background()
	enableLive(t, st, r)
	r = ingestLive(t, st, r)
	d, err := st.Claim(ctx, r.ReportedAt)
	if err != nil {
		t.Fatal(err)
	}
	token, err := st.ReserveLiveStart(ctx, "phone", r, r.ReportedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.FinishLiveStart(ctx, "phone", r, token, true, false, r.ReportedAt); err != nil {
		t.Fatal(err)
	}
	// Simulate a process exit after recording start acceptance, before completing
	// the ordinary delivery. The lease is retried but no second alert is sent.
	w.Now = func() time.Time { return r.ReportedAt.Add(31 * time.Second) }
	if _, err = w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 0 {
		t.Fatal("accepted start was sent again")
	}
	var status string
	if err = st.DB.QueryRowContext(ctx, "SELECT status FROM deliveries WHERE id=?", d.ID).Scan(&status); err != nil || status != "accepted" {
		t.Fatal(status, err)
	}
}

func TestTsunamiNewWarningDoesNotAcceptOldActivityGeneration(t *testing.T) {
	st, w, _, r := setup(t)
	ctx := context.Background()
	enableLive(t, st, r)
	r.EventID = "tsunami-fixture"
	r.TelegramType = "VTSE41"
	r.Classification = "telegram.earthquake"
	r.Category = "tsunami"
	r.EventType = "tsunami_warning"
	r.Warning = true
	r.Serial = nil
	r = ingestLive(t, st, r)
	if _, err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterLiveActivityToken(ctx, "phone", r.EventID, r.TelegramType, "oldActivity", "eeff", r.ServerSequence, r.ReportedAt); err != nil {
		t.Fatal(err)
	}
	clear := r
	clear.ID = "clear"
	clear.MessageID = "clear"
	clear.Warning = false
	clear.EventType = "tsunami_info"
	clear.ReportedAt = r.ReportedAt.Add(time.Second)
	clear.ReceivedAt = clear.ReportedAt
	clear = ingestLive(t, st, clear)
	now := clear.ReportedAt.Add(time.Second)
	w.Now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		if _, err := w.LiveStep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	renewed := r
	renewed.ID = "renewed"
	renewed.MessageID = "renewed"
	renewed.ReportedAt = now
	renewed.ReceivedAt = now
	renewed = ingestLive(t, st, renewed)
	now = now.Add(time.Second)
	for i := 0; i < 2; i++ {
		if _, err := w.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RegisterLiveActivityToken(ctx, "phone", r.EventID, r.TelegramType, "oldActivity", "eeff", r.ServerSequence, now); err != store.ErrInvalid {
		t.Fatal("old generation accepted", err)
	}
	if err := st.RegisterLiveActivityToken(ctx, "phone", r.EventID, r.TelegramType, "newActivity", "aacc", renewed.ServerSequence, now); err != nil {
		t.Fatal("new generation rejected", err)
	}
}

func TestLiveActivityEndBeforeTokenUploadRetainsTerminalReason(t *testing.T) {
	for _, dismissed := range []bool{false, true} {
		name := "natural completion"
		if dismissed {
			name = "user dismissal"
		}
		t.Run(name, func(t *testing.T) {
			st, w, _, r := setup(t)
			ctx := context.Background()
			r.EventID = "tsunami-gap"
			r.TelegramType = "VTSE41"
			r.Classification = "telegram.earthquake"
			r.Category = "tsunami"
			r.EventType = "tsunami_warning"
			r.Warning = true
			r.Serial = nil
			enableLive(t, st, r)
			r = ingestLive(t, st, r)
			if _, err := w.Step(ctx); err != nil {
				t.Fatal(err)
			}
			// The client closes before its update token reaches the server.
			if err := st.EndLiveActivity(ctx, "phone", r.EventID, r.TelegramType, "earlyActivity", r.ServerSequence, dismissed); err != nil {
				t.Fatal(err)
			}
			// A late natural-end callback must not erase a user dismissal.
			if err := st.EndLiveActivity(ctx, "phone", r.EventID, r.TelegramType, "earlyActivity", r.ServerSequence, false); err != nil {
				t.Fatal(err)
			}
			if err := st.RegisterLiveActivityToken(ctx, "phone", r.EventID, r.TelegramType, "earlyActivity", "eeff", r.ServerSequence, r.ReportedAt); err != store.ErrInvalid {
				t.Fatal("late token resurrected ended activity", err)
			}
			record, err := st.LiveActivity(ctx, "phone", r.EventID, r.TelegramType)
			expected := "ended"
			if dismissed {
				expected = "dismissed"
			}
			if err != nil || record.State != expected {
				t.Fatal(record, err)
			}
			newer := r
			newer.ID, newer.MessageID = "new-warning", "new-warning"
			newer.ReportedAt = r.ReportedAt.Add(time.Second)
			newer.ReceivedAt = newer.ReportedAt
			newer = ingestLive(t, st, newer)
			token, err := st.ReserveLiveStart(ctx, "phone", newer, newer.ReportedAt)
			if err != nil {
				t.Fatal(err)
			}
			if dismissed {
				if token != "" {
					t.Fatal("user-dismissed activity was recreated")
				}
			} else {
				if token != "ccdd" {
					t.Fatal("new warning cannot start after natural completion")
				}
				if err := st.EndLiveActivity(ctx, "phone", r.EventID, r.TelegramType, "earlyActivity", r.ServerSequence, false); err != store.ErrInvalid {
					t.Fatal("old end callback accepted by new generation", err)
				}
				current, _ := st.LiveActivity(ctx, "phone", newer.EventID, newer.TelegramType)
				if current.State != "starting" {
					t.Fatal("old callback ended new activity")
				}
			}
		})
	}
}
