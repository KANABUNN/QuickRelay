package handoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/model"
	"quakerelay/server/internal/store"
)

type fakeUpstream struct{ stats dmdata.Stats }

func (f fakeUpstream) Snapshot() dmdata.Stats { return f.stats }
func (f fakeUpstream) Generation() string     { return "boot:3" }

func open(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func commit(t *testing.T, st *store.Store, serial int) string {
	t.Helper()
	frame := []byte(fmt.Sprintf(`{"type":"data","id":"frame-%d"}`, serial))
	now := time.Now()
	id, err := st.ArchiveFrame(context.Background(), frame, now)
	if err != nil {
		t.Fatal(err)
	}
	r := model.Report{ID: fmt.Sprint("r", serial), MessageID: fmt.Sprint("m", serial), EventID: "20261125000000", TelegramType: "VXSE45",
		Classification: "eew.forecast", EventType: "eew_forecast", Serial: &serial, ReportedAt: now, ReceivedAt: now, Raw: frame}
	if _, err = st.IngestWithHandoff(context.Background(), r, store.Handoff{ReceiptID: id, Upstream: "boot:3"}); err != nil {
		t.Fatal(err)
	}
	return id
}

func get(t *testing.T, h http.Handler, target string) (*httptest.ResponseRecorder, Batch) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
	var b Batch
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
	}
	return w, b
}

func TestBatchReturnsExactFramesAndUpstream(t *testing.T) {
	st := open(t)
	receipt := commit(t, st, 1)
	now := time.Now()
	h := New(st, fakeUpstream{dmdata.Stats{Connected: true, LastFrameAt: now, Reconnects: 2}})
	w, b := get(t, h, "/v1/handoff?after=0")
	if w.Code != 200 || len(b.Items) != 1 || b.Latest != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	item := b.Items[0]
	sum := sha256.Sum256(item.Frame)
	if hex.EncodeToString(sum[:]) != receipt || item.ReceiptID != receipt || item.Upstream != "boot:3" || item.Applied == nil {
		t.Fatal("frame bytes or identity changed", item)
	}
	if !b.Upstream.Configured || !b.Upstream.Connected || !b.Upstream.Fresh || b.Upstream.Generation != "boot:3" || b.Upstream.Reconnects != 2 {
		t.Fatal("upstream", b.Upstream)
	}
	// Offline: reception not configured.
	w = httptest.NewRecorder()
	New(st, nil).ServeHTTP(w, httptest.NewRequest("GET", "/v1/upstream", nil))
	var up UpstreamState
	if err := json.Unmarshal(w.Body.Bytes(), &up); err != nil || up.Configured || up.Fresh || up.ServerTime == "" {
		t.Fatal("offline upstream", w.Body.String())
	}
}

func TestBatchRejectsBadParametersAndForeignCursor(t *testing.T) {
	st := open(t)
	receipt := commit(t, st, 1)
	h := New(st, nil)
	for _, target := range []string{"/v1/handoff?after=-1", "/v1/handoff?after=x", "/v1/handoff?limit=0", "/v1/handoff?limit=1000",
		"/v1/handoff?max_bytes=0", "/v1/handoff?wait_ms=60000"} {
		if w, _ := get(t, h, target); w.Code != 400 {
			t.Fatal(target, w.Code)
		}
	}
	for _, target := range []string{"/v1/handoff?after=2", "/v1/handoff?after=1&after_receipt=other"} {
		if w, _ := get(t, h, target); w.Code != 409 {
			t.Fatal(target, w.Code)
		}
	}
	if w, b := get(t, h, "/v1/handoff?after=1&after_receipt="+receipt); w.Code != 200 || len(b.Items) != 0 || b.Items == nil {
		t.Fatal("caught-up cursor", w.Code, w.Body.String())
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/handoff", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal("write method accepted", w.Code)
	}
}

func TestLongPollWakesOnCommit(t *testing.T) {
	st := open(t)
	h := New(st, nil)
	type result struct {
		b       Batch
		code    int
		elapsed time.Duration
	}
	done := make(chan result, 1)
	started := time.Now()
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/handoff?after=0&wait_ms=10000", nil))
		var b Batch
		_ = json.Unmarshal(w.Body.Bytes(), &b)
		done <- result{b, w.Code, time.Since(started)}
	}()
	time.Sleep(100 * time.Millisecond)
	commit(t, st, 1)
	select {
	case r := <-done:
		if r.code != 200 || len(r.b.Items) != 1 || r.elapsed > 5*time.Second {
			t.Fatal("long-poll", r.code, len(r.b.Items), r.elapsed)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("long-poll did not wake on commit")
	}
	// Without new rows the wait ends at its deadline with an empty batch.
	started = time.Now()
	w, b := get(t, h, "/v1/handoff?after=1&wait_ms=200")
	if w.Code != 200 || len(b.Items) != 0 || time.Since(started) < 150*time.Millisecond {
		t.Fatal("deadline", w.Code, time.Since(started))
	}
}

func TestLongPollEndsWithRequest(t *testing.T) {
	st := open(t)
	h := New(st, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/handoff?wait_ms=20000", nil).WithContext(ctx))
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("abandoned long-poll kept running")
	}
}

func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "qrh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "h.sock")
	if len(path) > 100 {
		t.Skip("temporary directory too long for a Unix socket path")
	}
	return path
}

func TestUnixSocketServesAndShutsDown(t *testing.T) {
	st := open(t)
	commit(t, st, 1)
	path := socketPath(t)
	ln, err := Listen(path, "")
	if err != nil {
		t.Skip("Unix sockets unavailable:", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, ln, New(st, nil)) }()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}, Timeout: 5 * time.Second}
	resp, err := client.Get("http://handoff/v1/handoff?after=0")
	if err != nil {
		t.Fatal(err)
	}
	var b Batch
	err = json.NewDecoder(resp.Body).Decode(&b)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || len(b.Items) != 1 {
		t.Fatal(resp.StatusCode, err)
	}
	// A waiting long-poll does not hold shutdown.
	go func() {
		if resp, err := client.Get("http://handoff/v1/handoff?after=1&wait_ms=20000"); err == nil {
			resp.Body.Close()
		}
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err = <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown waited for the long-poll")
	}
	// A crashed process leaves its socket file behind; a restart replaces it.
	ln, err = Listen(path, "")
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if _, err = os.Lstat(path); err != nil {
		t.Fatal("stale socket not left behind", err)
	}
	ln, err = Listen(path, "")
	if err != nil {
		t.Fatal("stale socket not replaced", err)
	}
	ln.Close()
}

func TestListenRefusesNonSocketFile(t *testing.T) {
	path := socketPath(t)
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if ln, err := Listen(path, ""); err == nil {
		ln.Close()
		t.Fatal("replaced a regular file")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "data" {
		t.Fatal("regular file changed", err)
	}
}
