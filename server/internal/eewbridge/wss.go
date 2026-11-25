package eewbridge

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"quakerelay/server/internal/eew"
)

const (
	maxFrameBytes    = 256 << 10
	maxSyncBytes     = 4 << 20
	syncTimeout      = 10 * time.Second
	writeTimeout     = 5 * time.Second
	snapshotWindow   = 10 * time.Minute
	maxResumeRecords = 2000
	liveBatch        = 64
	authFailureLimit = 20
)

// Periods of the protocol; variables only so tests can shorten them.
var (
	subscribeTimeout = 5 * time.Second
	healthPeriod     = 5 * time.Second
	revocationPeriod = 5 * time.Second
)

// WebSocket close codes. Clients reconnect with backoff except after 4401/4403/4410,
// which need new credentials or configuration.
const (
	CloseBadRequest   websocket.StatusCode = 4400
	CloseForbidden    websocket.StatusCode = 4403
	CloseTimeout      websocket.StatusCode = 4408
	CloseResync       websocket.StatusCode = 4409
	CloseRevoked      websocket.StatusCode = 4410
	CloseTooLarge     websocket.StatusCode = 4413
	CloseSlowConsumer websocket.StatusCode = 4429
)

type closeError struct {
	code   websocket.StatusCode
	reason string
}

func (e closeError) Error() string { return e.reason }

// Server delivers the EEW stream to authenticated clients over WebSocket.
// It listens on loopback; Caddy terminates TLS and forwards /v1/eew.
type Server struct {
	Store       *Store
	Health      *Health
	Logger      *slog.Logger
	MaxSessions int
	MaxPerToken int

	mu       sync.Mutex
	sessions map[*session]struct{}
	failures []time.Time
	wg       sync.WaitGroup
}

type session struct {
	token  Token
	cancel context.CancelCauseFunc
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/eew", s.serveWS)
	mux.HandleFunc("GET /healthz", s.healthz)
	return mux
}

func (s *Server) limits() (int, int) {
	total, perToken := s.MaxSessions, s.MaxPerToken
	if total <= 0 {
		total = 32
	}
	if perToken <= 0 {
		perToken = 4
	}
	return total, perToken
}

// authAllowed bounds credential guessing across all clients.
func (s *Server) authAllowed(now time.Time, failed bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.failures[:0]
	for _, at := range s.failures {
		if now.Sub(at) < time.Minute {
			kept = append(kept, at)
		}
	}
	s.failures = kept
	if failed {
		s.failures = append(s.failures, now)
	}
	return len(s.failures) < authFailureLimit
}

func bearer(r *http.Request) string {
	scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(value)
}

func (s *Server) unregister(sess *session) {
	s.mu.Lock()
	delete(s.sessions, sess)
	s.mu.Unlock()
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	if !s.authAllowed(now, false) {
		http.Error(w, "too many failed authentications", http.StatusTooManyRequests)
		return
	}
	token, err := s.Store.Authenticate(r.Context(), bearer(r))
	if errors.Is(err, ErrUnauthorized) {
		s.authAllowed(now, true)
		w.Header().Set("WWW-Authenticate", `Bearer realm="eew"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	s.wg.Add(1)
	defer s.wg.Done()
	// Sessions end only through their cause (shutdown, revocation, resync),
	// never by an unexplained cancellation, so each close carries its code.
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	s.mu.Lock()
	total, perToken := s.limits()
	if s.sessions == nil {
		s.sessions = map[*session]struct{}{}
	}
	same := 0
	for other := range s.sessions {
		if other.token.ID == token.ID {
			same++
		}
	}
	status := 0
	switch {
	case len(s.sessions) >= total:
		status = http.StatusServiceUnavailable
	case same >= perToken:
		status = http.StatusTooManyRequests
	}
	sess := &session{token: token, cancel: cancel}
	if status == 0 {
		s.sessions[sess] = struct{}{}
	}
	s.mu.Unlock()
	if status != 0 {
		http.Error(w, "too many sessions", status)
		return
	}
	defer s.unregister(sess)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4096)
	started := time.Now()
	err = s.session(ctx, conn, token)
	code, reason := websocket.StatusInternalError, "unavailable"
	var ce closeError
	switch {
	case errors.As(err, &ce):
		code, reason = ce.code, ce.reason
	case errors.As(context.Cause(ctx), &ce):
		code, reason = ce.code, ce.reason
	case errors.Is(err, errPeerClosed) || websocket.CloseStatus(err) != -1:
		code, reason = websocket.StatusNormalClosure, ""
	}
	s.Logger.Info("EEW session closed", "token", token.ID, "code", int(code), "reason", reason, "duration_ms", time.Since(started).Milliseconds())
	_ = conn.Close(code, reason)
}

// errPeerClosed ends a session the client closed.
var errPeerClosed = errors.New("peer closed")

// readSubscribe waits for subscribe. Cancelling a read would drop the TCP
// connection without a close frame, so the deadline is a timer instead.
func readSubscribe(ctx context.Context, conn *websocket.Conn, token Token) (SubscribeMessage, error) {
	var sub SubscribeMessage
	type result struct {
		typ  websocket.MessageType
		data []byte
		err  error
	}
	read := make(chan result, 1)
	go func() {
		typ, data, err := conn.Read(context.Background())
		read <- result{typ, data, err}
	}()
	timer := time.NewTimer(subscribeTimeout)
	defer timer.Stop()
	var r result
	select {
	case r = <-read:
	case <-timer.C:
		return sub, closeError{CloseTimeout, "subscribe_timeout"}
	case <-ctx.Done():
		return sub, context.Cause(ctx)
	}
	typ, data, err := r.typ, r.data, r.err
	if err != nil {
		return sub, err
	}
	if typ != websocket.MessageText || json.Unmarshal(data, &sub) != nil || sub.Type != "subscribe" {
		return sub, closeError{CloseBadRequest, "subscribe_expected"}
	}
	if sub.SchemaVersion != SchemaVersion {
		return sub, closeError{CloseBadRequest, "unsupported_schema_version"}
	}
	if sub.Environment != token.Environment {
		return sub, closeError{CloseForbidden, "environment_not_authorized"}
	}
	return sub, nil
}

func (s *Server) send(ctx context.Context, conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.write(ctx, conn, data)
}

func (s *Server) write(ctx context.Context, conn *websocket.Conn, data []byte) error {
	if len(data) > maxFrameBytes {
		return closeError{CloseTooLarge, "frame_too_large"}
	}
	// Session cancellation takes effect between frames: cancelling a write in
	// progress would drop the connection without its close code.
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	writeCtx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if err := conn.Write(writeCtx, websocket.MessageText, data); err != nil {
		if writeCtx.Err() != nil {
			return closeError{CloseSlowConsumer, "slow_consumer"}
		}
		return err
	}
	return nil
}

func (s *Server) health(now time.Time) HealthMessage {
	up, capture := s.Health.Status(now)
	return HealthMessage{SchemaVersion: SchemaVersion, Type: "health", ServerTime: now.UTC().Format("2006-01-02T15:04:05.000Z"), Upstream: up, Capture: capture}
}

func (s *Server) session(ctx context.Context, conn *websocket.Conn, token Token) error {
	sub, err := readSubscribe(ctx, conn, token)
	if err != nil {
		return err
	}
	// From here the client only closes; any data frame ends the session. The
	// reader is not tied to ctx: cancelling it would drop the close frame.
	peer := conn.CloseRead(context.Background())
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	go func() {
		select {
		case <-peer.Done():
			stop(errPeerClosed)
		case <-ctx.Done():
		}
	}()
	syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	position, err := s.sync(syncCtx, conn, sub)
	cancel()
	if err != nil {
		if syncCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return closeError{CloseTimeout, "sync_timeout"}
		}
		return err
	}
	return s.live(ctx, conn, position)
}

// sync sends sync_begin, the replay or snapshot up to asOfSeq, and sync_complete.
func (s *Server) sync(ctx context.Context, conn *websocket.Conn, sub SubscribeMessage) (Resume, error) {
	var resume *Resume
	if c := sub.Resume; c != nil {
		// An unparsable or foreign cursor falls back to a snapshot.
		if seq, err := strconv.ParseInt(c.Seq, 10, 64); err == nil && seq >= 0 {
			resume = &Resume{StreamID: c.StreamID, Epoch: c.Epoch, Seq: seq}
		}
	}
	since := time.Now().Add(-snapshotWindow)
	view, err := s.Store.SyncView(ctx, StreamProduction, resume, since, maxResumeRecords)
	if err != nil {
		return Resume{}, err
	}
	frames, err := syncFrames(view)
	if err == errSyncTooLarge && view.Mode == "resume" {
		if view, err = s.Store.SyncView(ctx, StreamProduction, nil, since, maxResumeRecords); err == nil {
			frames, err = syncFrames(view)
		}
	}
	if err == errSyncTooLarge {
		return Resume{}, closeError{CloseTooLarge, "sync_too_large"}
	}
	if err != nil {
		return Resume{}, err
	}
	for _, f := range frames {
		if err = s.write(ctx, conn, f); err != nil {
			return Resume{}, err
		}
	}
	return Resume{StreamID: view.Stream.ID, Epoch: view.Stream.Epoch, Seq: view.Stream.LastSeq}, nil
}

var errSyncTooLarge = errors.New("sync too large")

// syncFrames encodes a whole synchronization before anything is sent, so a
// client never receives a partial one that the server knew it could not finish.
func syncFrames(view SyncView) ([][]byte, error) {
	st := view.Stream
	asOf := seqText(st.LastSeq)
	var frames [][]byte
	total := 0
	add := func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if len(data) > maxFrameBytes {
			return closeError{CloseTooLarge, "frame_too_large"}
		}
		total += len(data)
		if total > maxSyncBytes {
			return errSyncTooLarge
		}
		frames = append(frames, data)
		return nil
	}
	if err := add(SyncBeginMessage{SchemaVersion: SchemaVersion, Type: "sync_begin", StreamID: st.ID, Epoch: st.Epoch, Mode: view.Mode, AsOfSeq: asOf}); err != nil {
		return nil, err
	}
	if view.Mode == "resume" {
		for _, r := range view.Records {
			if err := add(stateMessage(st, r, "replay")); err != nil {
				return nil, err
			}
		}
	} else {
		parts, err := packSnapshot(view.Events)
		if err != nil {
			return nil, err
		}
		for i, events := range parts {
			if err := add(SnapshotMessage{SchemaVersion: SchemaVersion, Type: "snapshot", StreamID: st.ID, Epoch: st.Epoch, AsOfSeq: asOf,
				Part: i + 1, Parts: len(parts), Events: events}); err != nil {
				return nil, err
			}
		}
	}
	if err := add(SyncCompleteMessage{SchemaVersion: SchemaVersion, Type: "sync_complete", Cursor: Cursor{StreamID: st.ID, Epoch: st.Epoch, Seq: asOf}}); err != nil {
		return nil, err
	}
	return frames, nil
}

// packSnapshot splits events into parts that each fit one frame. An empty
// state is one part with no events.
func packSnapshot(events []eew.Event) ([][]eew.Event, error) {
	const envelope = 512 // message fields around the events array
	parts := [][]eew.Event{{}}
	size := envelope
	for _, e := range events {
		data, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		if envelope+len(data) > maxFrameBytes {
			return nil, closeError{CloseTooLarge, "frame_too_large"}
		}
		if size+len(data)+1 > maxFrameBytes {
			parts = append(parts, []eew.Event{})
			size = envelope
		}
		last := len(parts) - 1
		parts[last] = append(parts[last], e)
		size += len(data) + 1
	}
	return parts, nil
}

// live sends log entries after the synchronized position as they commit, and
// health every five seconds or on change, before any later entry.
func (s *Server) live(ctx context.Context, conn *websocket.Conn, position Resume) error {
	ticker := time.NewTicker(healthPeriod)
	defer ticker.Stop()
	stream := Stream{ID: position.StreamID, Epoch: position.Epoch}
	var sentHealth HealthMessage
	sendHealth := func() error {
		sentHealth = s.health(time.Now())
		return s.send(ctx, conn, sentHealth)
	}
	if err := sendHealth(); err != nil {
		return err
	}
	for {
		changed, healthChanged := s.Store.Changed(), s.Health.Changed()
		if h := s.health(time.Now()); h.Upstream != sentHealth.Upstream || h.Capture != sentHealth.Capture {
			if err := sendHealth(); err != nil {
				return err
			}
		}
		records, err := s.Store.LiveBatch(ctx, stream.ID, stream.Epoch, position.Seq, liveBatch)
		if errors.Is(err, ErrResync) {
			return closeError{CloseResync, "resync_required"}
		}
		if err != nil {
			return err
		}
		for _, r := range records {
			if err = s.send(ctx, conn, stateMessage(stream, r, "live")); err != nil {
				return err
			}
			position.Seq = r.Seq
		}
		if len(records) == liveBatch {
			continue
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-changed:
		case <-healthChanged:
		case <-ticker.C:
			if err = sendHealth(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	up, capture := s.Health.Status(time.Now())
	stream, err := s.Store.Stream(r.Context(), StreamProduction)
	s.mu.Lock()
	sessions := len(s.sessions)
	s.mu.Unlock()
	status := http.StatusOK
	if err != nil || up != Healthy || capture != Healthy {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"upstream": up, "capture": capture, "sessions": sessions, "epoch": stream.Epoch, "last_seq": stream.LastSeq})
}

// WatchRevocations closes sessions whose token was revoked, until ctx ends.
func (s *Server) WatchRevocations(ctx context.Context) {
	ticker := time.NewTicker(revocationPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		var ids []string
		for sess := range s.sessions {
			ids = append(ids, sess.token.ID)
		}
		s.mu.Unlock()
		if len(ids) == 0 {
			continue
		}
		revoked, err := s.Store.RevokedAmong(ctx, ids)
		if err != nil {
			continue
		}
		s.mu.Lock()
		for sess := range s.sessions {
			if revoked[sess.token.ID] {
				sess.cancel(closeError{CloseRevoked, "token_revoked"})
			}
		}
		s.mu.Unlock()
	}
}

func (s *Server) closeSessions(cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sess := range s.sessions {
		sess.cancel(cause)
	}
}

// Serve accepts connections on ln until ctx ends, then closes every session
// with 1001 (going away).
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 8192}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	s.closeSessions(closeError{websocket.StatusGoingAway, "server_shutdown"})
	finished := make(chan struct{})
	go func() { s.wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-shutdown.Done():
	}
	<-done
	return nil
}
