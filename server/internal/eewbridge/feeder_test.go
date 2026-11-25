package eewbridge

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/eew"
	"quakerelay/server/internal/handoff"
	"quakerelay/server/internal/ingest"
	"quakerelay/server/internal/store"
)

type fakeUpstream struct {
	mu         sync.Mutex
	generation string
}

func (f *fakeUpstream) Snapshot() dmdata.Stats {
	// A frame received just before: the relay's freshness check rejects a time
	// after its own clock reading, which a nanosecond clock can produce here.
	return dmdata.Stats{Connected: true, LastFrameAt: time.Now().Add(-time.Second)}
}
func (f *fakeUpstream) Generation() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.generation
}
func (f *fakeUpstream) set(g string) { f.mu.Lock(); f.generation = g; f.mu.Unlock() }

type relayFixture struct {
	t        *testing.T
	relay    *store.Store
	upstream *fakeUpstream
	url      string
	received time.Time
}

func newRelay(t *testing.T) *relayFixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	r := &relayFixture{t: t, relay: st, upstream: &fakeUpstream{generation: "boot:1"}, received: sampleTime.Add(3 * time.Minute)}
	server := httptest.NewServer(handoff.New(st, r.upstream))
	t.Cleanup(func() { server.Close(); st.Close() })
	r.url = server.URL
	return r
}

func (r *relayFixture) ingest(frame []byte) {
	r.t.Helper()
	r.received = r.received.Add(time.Second)
	got, err := (ingest.Processor{Store: r.relay, Handoff: true, Upstream: r.upstream.Generation()}).Process(context.Background(), frame, r.received)
	if err != nil || got.Rejection != nil || !got.Ingest.Inserted {
		r.t.Fatal(got.Rejection, err)
	}
}

func startFeeder(t *testing.T, bridge *Store, url string) (*Health, context.CancelFunc) {
	t.Helper()
	health := &Health{}
	f := &Feeder{Store: bridge, Health: health, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), BaseURL: url, Wait: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return health, cancel
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func origins(t *testing.T, s *Store) map[string]string {
	t.Helper()
	rows, err := s.DB.Query("SELECT telegram_id,origin FROM inputs")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, origin string
		if err = rows.Scan(&id, &origin); err != nil {
			t.Fatal(err)
		}
		out[id] = origin
	}
	return out
}

func cursorAt(s *Store, seq int64) func() bool {
	return func() bool { c, err := s.Cursor(context.Background()); return err == nil && c.Seq >= seq }
}

func TestFeederCatchUpThenLive(t *testing.T) {
	r := newRelay(t)
	r.ingest(frame(t, "VXSE45", "f1", sample(t, "77_01_01_240613_VXSE45.xml")))
	r.ingest(frame(t, "VXSE45", "f5", withSerial(sample(t, "77_01_02_240613_VXSE45.xml"), 5)))
	bridge := openBridge(t)
	health, _ := startFeeder(t, bridge, r.url)
	eventually(t, "catch-up", cursorAt(bridge, 2))
	eventually(t, "health", func() bool { up, capture := health.Status(time.Now()); return up == Healthy && capture == Healthy })
	// A lower-numbered cancellation committed by the relay ends the forecast.
	r.ingest(frame(t, "VXSE45", "c4", withSerial(sample(t, "77_01_33_240613_VXSE45.xml"), 4)))
	eventually(t, "live", cursorAt(bridge, 3))
	o := origins(t, bridge)
	if o["f1"] != eew.OriginRecovered || o["f5"] != eew.OriginRecovered || o["c4"] != eew.OriginLive {
		t.Fatal(o)
	}
	state, err := bridge.State(context.Background(), eew.EnvironmentProduction, "20240417231454")
	if err != nil || state.Event.Forecast.Status != eew.StatusCanceled || *state.Event.Forecast.Source.Serial != "4" || state.Event.Forecast.Source.Origin != eew.OriginLive {
		t.Fatal(state, err)
	}
	c, _ := bridge.Cursor(context.Background())
	if c.Generation != "boot:1" || c.ReceiptID == "" {
		t.Fatal(c)
	}
}

func TestFeederMarksWarningsPartialOnReconnect(t *testing.T) {
	r := newRelay(t)
	r.ingest(frame(t, "VXSE43", "w1", sample(t, "37_01_01_240613_VXSE43.xml")))
	bridge := openBridge(t)
	startFeeder(t, bridge, r.url)
	eventually(t, "warning", cursorAt(bridge, 1))
	stream, _ := bridge.Stream(context.Background(), StreamProduction)
	r.upstream.set("boot:2")
	eventually(t, "reconcile", func() bool {
		s, _ := bridge.State(context.Background(), eew.EnvironmentProduction, "20240417231454")
		return s != nil && s.Event.Warning.Data.Coverage == eew.CoveragePartial
	})
	log, err := bridge.Log(context.Background(), StreamProduction, 0, 10)
	if err != nil || len(log) != 2 || log[1].Cause != "reconcile" || log[1].Event.Warning.Revision != 2 {
		t.Fatal(log, err)
	}
	if again, _ := bridge.Stream(context.Background(), StreamProduction); again.Epoch != stream.Epoch {
		t.Fatal("a reconnect must not change the delivery epoch")
	}
}

func TestFeederRecoversFromGapAndRestoredRelay(t *testing.T) {
	r := newRelay(t)
	base := sample(t, "77_01_02_240613_VXSE45.xml")
	for i := 2; i <= 4; i++ {
		r.ingest(frame(t, "VXSE45", "f"+string(rune('0'+i)), withSerial(base, i)))
	}
	// The relay removed rows 1-2 before the bridge read them.
	if _, err := r.relay.DB.Exec("DELETE FROM eew_handoff WHERE ingress_seq<=2; UPDATE eew_handoff_meta SET purged_through=2"); err != nil {
		t.Fatal(err)
	}
	bridge := openBridge(t)
	before, _ := bridge.Stream(context.Background(), StreamProduction)
	_, cancel := startFeeder(t, bridge, r.url)
	eventually(t, "after gap", cursorAt(bridge, 3))
	after, _ := bridge.Stream(context.Background(), StreamProduction)
	if after.Epoch == before.Epoch {
		t.Fatal("missed rows must start a new epoch")
	}
	if o := origins(t, bridge); len(o) != 1 || o["f4"] == "" {
		t.Fatal(o)
	}
	cancel()
	// A restored relay no longer has the bridge's position.
	if err := bridge.SetCursor(context.Background(), 50, "unknown"); err != nil {
		t.Fatal(err)
	}
	startFeeder(t, bridge, r.url)
	eventually(t, "restart from retained rows", func() bool {
		s, err := bridge.Stream(context.Background(), StreamProduction)
		c, _ := bridge.Cursor(context.Background())
		return err == nil && s.Epoch != after.Epoch && c.Seq == 3
	})
}
