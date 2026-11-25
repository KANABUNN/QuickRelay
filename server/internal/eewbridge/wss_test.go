package eewbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"quakerelay/server/internal/eew"
)

type wsFixture struct {
	t      *testing.T
	store  *Store
	health *Health
	server *Server
	http   *httptest.Server
	secret string
	token  Token
}

func newWS(t *testing.T) *wsFixture {
	t.Helper()
	st := openBridge(t)
	f := &wsFixture{t: t, store: st, health: &Health{}}
	f.server = &Server{Store: st, Health: f.health, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	f.http = httptest.NewServer(f.server.Handler())
	t.Cleanup(func() { f.server.closeSessions(closeError{websocket.StatusGoingAway, "test_end"}); f.http.Close() })
	var err error
	if f.token, f.secret, err = st.IssueToken(context.Background(), "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *wsFixture) dial(secret string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	if secret != "" {
		header.Set("Authorization", "Bearer "+secret)
	}
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.http.URL, "http")+"/v1/eew", &websocket.DialOptions{HTTPHeader: header})
}

func (f *wsFixture) connect(resume *Cursor) *websocket.Conn {
	f.t.Helper()
	conn, _, err := f.dial(f.secret)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { conn.CloseNow() })
	sub, _ := json.Marshal(SubscribeMessage{SchemaVersion: 1, Type: "subscribe", Environment: "production", Resume: resume})
	if err = conn.Write(context.Background(), websocket.MessageText, sub); err != nil {
		f.t.Fatal(err)
	}
	return conn
}

func next(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// nextOf skips health messages.
func nextOf(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	for {
		if m := next(t, conn); m["type"] != "health" {
			return m
		}
	}
}

func closeCode(t *testing.T, conn *websocket.Conn) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

func (f *wsFixture) process(frame []byte, relay *eew.RelayState) {
	f.t.Helper()
	if out, err := f.store.Process(context.Background(), Input{Frame: frame, ReceivedAt: sampleTime, Origin: eew.OriginLive, Upstream: "u1", Relay: relay}, time.Now()); err != nil || out.Outcome != eew.Applied {
		f.t.Fatal(out, err)
	}
}

func TestWSRequiresActiveToken(t *testing.T) {
	f := newWS(t)
	for _, secret := range []string{"", "eqb_wrong", f.secret + "x"} {
		if _, resp, err := f.dial(secret); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatal("accepted", secret)
		}
	}
	if err := f.store.RevokeToken(context.Background(), f.token.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, resp, err := f.dial(f.secret); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("revoked token accepted")
	}
	for range authFailureLimit {
		f.dial("eqb_guess")
	}
	if _, resp, _ := f.dial("eqb_guess"); resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatal("guessing not limited")
	}
}

// Behind Caddy every connection comes from loopback; one client's failures must
// not stop the others from reconnecting.
func TestAuthFailuresCountPerForwardedClient(t *testing.T) {
	f := newWS(t)
	dial := func(secret, client string) int {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		header := http.Header{"Authorization": {"Bearer " + secret}, "X-Forwarded-For": {client}}
		conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.http.URL, "http")+"/v1/eew", &websocket.DialOptions{HTTPHeader: header})
		if err == nil {
			conn.CloseNow()
			return http.StatusSwitchingProtocols
		}
		return resp.StatusCode
	}
	for range authFailureLimit {
		dial("eqb_guess", "203.0.113.7")
	}
	if code := dial(f.secret, "203.0.113.7"); code != http.StatusTooManyRequests {
		t.Fatal("guessing client not limited", code)
	}
	if code := dial(f.secret, "198.51.100.2"); code != http.StatusSwitchingProtocols {
		t.Fatal("another client was locked out", code)
	}

	request := func(remote string, forwarded ...string) *http.Request {
		r := httptest.NewRequest("GET", "/v1/eew", nil)
		r.RemoteAddr = remote
		for _, v := range forwarded {
			r.Header.Add("X-Forwarded-For", v)
		}
		return r
	}
	for _, c := range []struct {
		r    *http.Request
		want string
	}{
		{request("127.0.0.1:5000", "198.51.100.1, 203.0.113.9"), "203.0.113.9"},
		{request("[::1]:5000", "10.0.0.1", "2001:db8::5"), "2001:db8::5"},
		{request("127.0.0.1:5000", "not-an-address"), "127.0.0.1"},
		{request("127.0.0.1:5000"), "127.0.0.1"},
		// Only the local proxy may name the client.
		{request("192.0.2.4:5000", "203.0.113.9"), "192.0.2.4"},
	} {
		if got := clientAddress(c.r); got != c.want {
			t.Fatal(c.r.RemoteAddr, c.r.Header, got)
		}
	}
	s := &Server{}
	now := time.Now()
	for i := range maxTrackedClients {
		s.authAllowed(fmt.Sprintf("10.0.%d.%d", i/256, i%256), now, true)
	}
	// A full table shares one count for new addresses and keeps tracked ones.
	for range authFailureLimit {
		s.authAllowed("192.0.2.1", now, true)
	}
	if s.authAllowed("192.0.2.2", now, false) || !s.authAllowed("10.0.0.1", now, false) {
		t.Fatal("overflow bucket not shared")
	}
	if !s.authAllowed("192.0.2.2", now.Add(2*time.Minute), false) || len(s.failures) != 0 {
		t.Fatal("expired failures kept", len(s.failures))
	}
}

func TestWSSnapshotThenLive(t *testing.T) {
	f := newWS(t)
	f.process(frame(t, "VXSE45", "f1", sample(t, "77_01_01_240613_VXSE45.xml")), applied(1))
	f.process(frame(t, "VXSE43", "w1", sample(t, "37_01_01_240613_VXSE43.xml")), applied(1))
	conn := f.connect(nil)
	begin := next(t, conn)
	stream, _ := f.store.Stream(context.Background(), StreamProduction)
	if begin["type"] != "sync_begin" || begin["mode"] != "snapshot" || begin["asOfSeq"] != "2" || begin["epoch"] != stream.Epoch || begin["streamId"] != "eew-production" {
		t.Fatal(begin)
	}
	snap := next(t, conn)
	events := snap["events"].([]any)
	if snap["type"] != "snapshot" || snap["part"] != 1.0 || snap["parts"] != 1.0 || snap["asOfSeq"] != "2" || len(events) != 1 {
		t.Fatal(snap)
	}
	event := events[0].(map[string]any)
	if event["revision"] != "2" || event["forecast"] == nil || event["warning"] == nil {
		t.Fatal(event)
	}
	done := next(t, conn)
	if done["type"] != "sync_complete" || done["cursor"].(map[string]any)["seq"] != "2" {
		t.Fatal(done)
	}
	if h := next(t, conn); h["type"] != "health" || h["upstream"] != Degraded || h["capture"] != Degraded {
		t.Fatal("health after sync", h)
	}
	f.health.Observe(true, time.Now())
	if h := next(t, conn); h["type"] != "health" || h["upstream"] != Healthy || h["capture"] != Healthy {
		t.Fatal("health change", h)
	}
	cancel := withSerial(sample(t, "77_01_33_240613_VXSE45.xml"), 4)
	f.process(frame(t, "VXSE45", "c4", cancel), &eew.RelayState{Cancelled: true})
	state := nextOf(t, conn)
	forecast := state["event"].(map[string]any)["forecast"].(map[string]any)
	if state["type"] != "eew.state" || state["seq"] != "3" || state["delivery"] != "live" || state["cause"] != "telegram" ||
		state["changedSeries"] != "forecast" || forecast["status"] != "canceled" || forecast["data"] != nil || state["committedAt"] == "" {
		t.Fatal(state)
	}
}

func TestWSResumeAndFallbacks(t *testing.T) {
	f := newWS(t)
	f.server.MaxPerToken = 16
	f.process(frame(t, "VXSE45", "f1", sample(t, "77_01_01_240613_VXSE45.xml")), applied(1))
	f.process(frame(t, "VXSE45", "f2", sample(t, "77_01_02_240613_VXSE45.xml")), applied(2))
	stream, _ := f.store.Stream(context.Background(), StreamProduction)
	conn := f.connect(&Cursor{StreamID: StreamProduction, Epoch: stream.Epoch, Seq: "1"})
	if m := next(t, conn); m["mode"] != "resume" || m["asOfSeq"] != "2" {
		t.Fatal(m)
	}
	if m := next(t, conn); m["type"] != "eew.state" || m["seq"] != "2" || m["delivery"] != "replay" {
		t.Fatal(m)
	}
	if m := next(t, conn); m["type"] != "sync_complete" {
		t.Fatal(m)
	}
	for name, c := range map[string]*Cursor{
		"other_epoch": {StreamID: StreamProduction, Epoch: "g0", Seq: "1"},
		"ahead":       {StreamID: StreamProduction, Epoch: stream.Epoch, Seq: "9"},
		"invalid":     {StreamID: StreamProduction, Epoch: stream.Epoch, Seq: "x"},
		"other":       {StreamID: "eew-other", Epoch: stream.Epoch, Seq: "1"},
	} {
		if m := next(t, f.connect(c)); m["mode"] != "snapshot" {
			t.Fatal(name, m)
		}
	}
	// Up to date: resume with nothing to replay.
	conn = f.connect(&Cursor{StreamID: StreamProduction, Epoch: stream.Epoch, Seq: "2"})
	if m := next(t, conn); m["mode"] != "resume" {
		t.Fatal(m)
	}
	if m := next(t, conn); m["type"] != "sync_complete" {
		t.Fatal(m)
	}
}

func TestWSCloseCodes(t *testing.T) {
	old := subscribeTimeout
	subscribeTimeout = 200 * time.Millisecond
	defer func() { subscribeTimeout = old }()
	f := newWS(t)
	for want, sub := range map[websocket.StatusCode]string{
		CloseBadRequest: `{"schemaVersion":2,"type":"subscribe","environment":"production","resume":null}`,
		CloseForbidden:  `{"schemaVersion":1,"type":"subscribe","environment":"training","resume":null}`,
	} {
		conn, _, err := f.dial(f.secret)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Write(context.Background(), websocket.MessageText, []byte(sub))
		if got := closeCode(t, conn); got != want {
			t.Fatal(sub, got)
		}
	}
	conn, _, err := f.dial(f.secret)
	if err != nil {
		t.Fatal(err)
	}
	if got := closeCode(t, conn); got != CloseTimeout {
		t.Fatal("no subscribe", got)
	}
	// An epoch change ends live sessions so clients resynchronize.
	conn = f.connect(nil)
	nextOf(t, conn)
	if _, err = f.store.Reconcile(context.Background(), time.Now(), true); err != nil {
		t.Fatal(err)
	}
	if got := closeCode(t, conn); got != CloseResync {
		t.Fatal("epoch change", got)
	}
}

func TestWSRevocationLimitsAndShutdown(t *testing.T) {
	oldPeriod := revocationPeriod
	revocationPeriod = 50 * time.Millisecond
	defer func() { revocationPeriod = oldPeriod }()
	f := newWS(t)
	f.server.MaxPerToken = 1
	conn := f.connect(nil)
	nextOf(t, conn)
	if _, resp, err := f.dial(f.secret); err == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatal("per-token limit", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.server.WatchRevocations(ctx)
	if err := f.store.RevokeToken(context.Background(), f.token.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := closeCode(t, conn); got != CloseRevoked {
		t.Fatal("revocation", got)
	}
	_, secret, _ := f.store.IssueToken(context.Background(), "second", time.Now())
	f.secret = secret
	conn = f.connect(nil)
	nextOf(t, conn)
	f.server.closeSessions(closeError{websocket.StatusGoingAway, "server_shutdown"})
	if got := closeCode(t, conn); got != websocket.StatusGoingAway {
		t.Fatal("shutdown", got)
	}
}

func TestSnapshotPartsAndSyncLimit(t *testing.T) {
	big := func(id string) eew.Event {
		var regions []eew.ActiveRegion
		for i := range 600 {
			ref := strings.Repeat("r", 100)
			regions = append(regions, eew.ActiveRegion{Region: eew.Region{CodeType: "地震情報／細分区域", Code: id + string(rune('a'+i%26)), Name: "地域", Level: "region"}, SourceRef: &ref})
		}
		return eew.Event{EventID: id, Environment: "production", Revision: 1,
			Warning: &eew.WarningSeries{Status: "active", Revision: 1, Data: &eew.WarningData{ActiveRegions: regions, Coverage: "complete"}}}
	}
	var events []eew.Event
	for _, id := range []string{"a", "b", "c", "d"} {
		events = append(events, big(id))
	}
	parts, err := packSnapshot(events)
	if err != nil || len(parts) < 2 {
		t.Fatal(len(parts), err)
	}
	frames, err := syncFrames(SyncView{Stream: Stream{ID: StreamProduction, Epoch: "g1", LastSeq: 4}, Mode: "snapshot", Events: events})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if len(f) > maxFrameBytes {
			t.Fatal("frame over limit", len(f))
		}
	}
	if empty, _ := packSnapshot(nil); len(empty) != 1 || len(empty[0]) != 0 {
		t.Fatal("empty state must be one explicit part")
	}
	var records []LogRecord
	for i := range 40 {
		records = append(records, LogRecord{Seq: int64(i + 1), Cause: "telegram", ChangedSeries: "warning", Event: big("x")})
	}
	if _, err = syncFrames(SyncView{Stream: Stream{ID: StreamProduction, Epoch: "g1", LastSeq: 40}, Mode: "resume", Records: records}); !errors.Is(err, errSyncTooLarge) {
		t.Fatal("sync limit", err)
	}
}
