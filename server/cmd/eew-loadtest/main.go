// Command eew-loadtest measures the EEW path from relay receipt to WebSocket
// clients on isolated databases: synthetic VXSE45/VXSE43 telegrams are ingested
// at a fixed rate through a relay store with the hand-off, the hand-off socket,
// the bridge and its WebSocket server, all in this process. It never touches the
// production relay or bridge (docs/eew-bridge-deployment.md).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/eewbridge"
	"quakerelay/server/internal/handoff"
	"quakerelay/server/internal/ingest"
	"quakerelay/server/internal/store"
)

type options struct {
	Dir       string
	Samples   string
	Rate      int
	Duration  time.Duration
	Clients   int
	Baseline  bool
	TargetP95 time.Duration
}

// Stats summarizes durations in milliseconds.
type Stats struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50_ms"`
	P95   float64 `json:"p95_ms"`
	P99   float64 `json:"p99_ms"`
	Max   float64 `json:"max_ms"`
}

type Report struct {
	Telegrams    int     `json:"telegrams"`
	Rate         int     `json:"rate"`
	AchievedRate float64 `json:"achieved_rate"`
	Clients      int     `json:"clients"`
	Transport    string  `json:"handoff_transport"`
	// IngestBaseline is Processor.Process without the hand-off; Ingest with it.
	IngestBaseline *Stats `json:"ingest_without_handoff,omitempty"`
	Ingest         Stats  `json:"ingest_with_handoff"`
	// Delivery runs from receipt (the Process call) to a client reading the
	// live eew.state, over every client.
	Delivery  Stats   `json:"receipt_to_client"`
	Expected  int     `json:"expected_messages"`
	Received  int     `json:"received_messages"`
	TargetP95 float64 `json:"target_p95_ms"`
	Pass      bool    `json:"pass"`
}

func summarize(values []time.Duration) Stats {
	if len(values) == 0 {
		return Stats{}
	}
	sorted := append([]time.Duration{}, values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(q float64) float64 {
		i := int(math.Ceil(q*float64(len(sorted)))) - 1
		return float64(sorted[max(i, 0)].Microseconds()) / 1000
	}
	return Stats{Count: len(sorted), P50: at(0.50), P95: at(0.95), P99: at(0.99), Max: at(1)}
}

// telegrams builds synthetic frames from the official JMA samples. Every frame
// starts a new serial of its series, so each one changes state and is sent.
type telegrams struct {
	forecast, warning []byte
	run               string
}

var (
	jmaTime    = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})`)
	eventID    = regexp.MustCompile(`<EventID>[^<]*</EventID>`)
	serialTag  = regexp.MustCompile(`<Serial>[^<]*</Serial>`)
	reportTime = regexp.MustCompile(`<ReportDateTime>([^<]+)</ReportDateTime>`)
)

func loadTelegrams(dir string) (telegrams, error) {
	var t telegrams
	var err error
	if t.forecast, err = os.ReadFile(filepath.Join(dir, "77_01_02_240613_VXSE45.xml")); err != nil {
		return t, err
	}
	if t.warning, err = os.ReadFile(filepath.Join(dir, "37_01_01_240613_VXSE43.xml")); err != nil {
		return t, err
	}
	t.run = fmt.Sprintf("%x", time.Now().UnixNano())
	return t, nil
}

// reportsPerEvent is how many reports each synthetic event gets; every fourth
// is a VXSE43 warning.
const reportsPerEvent = 20

// frame returns telegram i with its times moved to now.
func (t telegrams) frame(i int, now time.Time) (id string, data []byte, err error) {
	event, n := i/reportsPerEvent, i%reportsPerEvent
	code, classification, xml, serial := "VXSE45", "eew.forecast", t.forecast, n-n/4+1
	if n%4 == 3 {
		code, classification, xml, serial = "VXSE43", "eew.warning", t.warning, n/4+1
	}
	base, err := time.Parse(time.RFC3339, string(reportTime.FindSubmatch(xml)[1]))
	if err != nil {
		return "", nil, err
	}
	shift := now.Truncate(time.Second).Sub(base)
	body := jmaTime.ReplaceAllFunc(xml, func(m []byte) []byte {
		at, err := time.Parse(time.RFC3339, string(m))
		if err != nil {
			return m
		}
		layout := "2006-01-02T15:04:05-07:00"
		if m[len(m)-1] == 'Z' {
			layout = "2006-01-02T15:04:05Z"
		}
		return []byte(at.Add(shift).In(at.Location()).Format(layout))
	})
	body = eventID.ReplaceAll(body, []byte(fmt.Sprintf("<EventID>%014d</EventID>", 90000000000000+event)))
	body = serialTag.ReplaceAll(body, []byte(fmt.Sprintf("<Serial>%d</Serial>", serial)))
	id = fmt.Sprintf("load-%s-%06d", t.run, i)
	e := dmdata.Envelope{Type: "data", ID: id, Classification: classification, Format: "xml", Encoding: "utf-8", Body: string(body)}
	e.Head.Type = code
	data, err = json.Marshal(e)
	return id, data, err
}

// upstream reports a connected DMDATA socket whose last frame was just received.
type upstream struct{}

func (upstream) Snapshot() dmdata.Stats {
	return dmdata.Stats{Connected: true, LastFrameAt: time.Now().Add(-time.Second)}
}
func (upstream) Generation() string { return "loadtest:1" }

// inject ingests count telegrams at rate per second and records each receipt
// time and Process duration. The span runs from the first receipt to the last.
func inject(ctx context.Context, p ingest.Processor, t telegrams, count, rate int, receipts map[string]time.Time, mu *sync.Mutex) ([]time.Duration, time.Duration, error) {
	interval := time.Second / time.Duration(rate)
	started := time.Now()
	var span time.Duration
	durations := make([]time.Duration, 0, count)
	for i := range count {
		if wait := time.Until(started.Add(time.Duration(i) * interval)); wait > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(wait):
			}
		}
		received := time.Now()
		span = received.Sub(started)
		id, frame, err := t.frame(i, received)
		if err != nil {
			return nil, 0, err
		}
		if receipts != nil {
			mu.Lock()
			receipts[id] = received
			mu.Unlock()
		}
		got, err := p.Process(ctx, frame, received)
		if err != nil {
			return nil, 0, err
		}
		if got.Rejection != nil || !got.Ingest.Inserted {
			return nil, 0, fmt.Errorf("telegram %d not ingested: %v", i, got.Rejection)
		}
		durations = append(durations, time.Since(received))
	}
	return durations, span, nil
}

func run(ctx context.Context, o options, logger *slog.Logger) (Report, error) {
	r := Report{Telegrams: o.Rate * int(o.Duration/time.Second), Rate: o.Rate, Clients: o.Clients, TargetP95: float64(o.TargetP95.Microseconds()) / 1000}
	if o.Rate <= 0 || r.Telegrams <= 0 || o.Clients <= 0 {
		return r, errors.New("rate, duration (whole seconds) and clients must be positive")
	}
	t, err := loadTelegrams(o.Samples)
	if err != nil {
		return r, fmt.Errorf("samples: %w", err)
	}
	if o.Baseline {
		relay, err := store.Open(filepath.Join(o.Dir, "baseline-relay.db"))
		if err != nil {
			return r, err
		}
		durations, _, err := inject(ctx, ingest.Processor{Store: relay}, t, r.Telegrams, o.Rate, nil, nil)
		relay.Close()
		if err != nil {
			return r, err
		}
		s := summarize(durations)
		r.IngestBaseline = &s
	}

	relay, err := store.Open(filepath.Join(o.Dir, "relay.db"))
	if err != nil {
		return r, err
	}
	defer relay.Close()
	bridge, err := eewbridge.Open(filepath.Join(o.Dir, "bridge.db"))
	if err != nil {
		return r, err
	}
	defer bridge.Close()
	// Every task stops before the databases close.
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	goTask := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }

	feeder := &eewbridge.Feeder{Logger: logger}
	// The production path is a Unix socket; loopback TCP stands in where none exist.
	r.Transport = "unix"
	ln, err := handoff.Listen(filepath.Join(o.Dir, "handoff.sock"), "")
	if err != nil {
		r.Transport = "tcp"
		if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			return r, err
		}
		feeder.BaseURL = "http://" + ln.Addr().String()
	} else {
		feeder.Socket = ln.Addr().String()
	}
	goTask(func() { _ = handoff.Serve(ctx, ln, handoff.New(relay, upstream{})) })

	health := &eewbridge.Health{}
	feeder.Store, feeder.Health = bridge, health
	server := &eewbridge.Server{Store: bridge, Health: health, Logger: logger, MaxSessions: o.Clients + 4}
	wsln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return r, err
	}
	goTask(func() { health.Run(ctx) })
	goTask(func() { feeder.Run(ctx) })
	goTask(func() { _ = server.Serve(ctx, wsln) })
	if err = waitHealthy(ctx, health); err != nil {
		return r, err
	}

	receipts := map[string]time.Time{}
	var mu sync.Mutex
	var delivery []time.Duration
	url := "ws://" + wsln.Addr().String() + "/v1/eew"
	clientsDone := make(chan struct{}, o.Clients)
	for i := range o.Clients {
		_, secret, err := bridge.IssueToken(ctx, fmt.Sprintf("load %d", i), time.Now())
		if err != nil {
			return r, err
		}
		conn, err := subscribe(ctx, url, secret)
		if err != nil {
			return r, fmt.Errorf("client %d: %w", i, err)
		}
		goTask(func() {
			defer conn.CloseNow()
			got := 0
			for got < r.Telegrams {
				state, err := readLive(ctx, conn)
				if err != nil {
					break
				}
				series := state.Event.Forecast
				source := ""
				if state.ChangedSeries == "warning" && state.Event.Warning != nil {
					source = state.Event.Warning.Source.TelegramID
				} else if series != nil {
					source = series.Source.TelegramID
				}
				now := time.Now()
				mu.Lock()
				if at, ok := receipts[source]; ok {
					delivery = append(delivery, now.Sub(at))
					got++
				}
				mu.Unlock()
			}
			clientsDone <- struct{}{}
		})
	}

	durations, span, err := inject(ctx, ingest.Processor{Store: relay, Handoff: true, Upstream: upstream{}.Generation()}, t, r.Telegrams, o.Rate, receipts, &mu)
	if err != nil {
		return r, err
	}
	r.Ingest = summarize(durations)
	if r.AchievedRate = float64(o.Rate); r.Telegrams > 1 && span > 0 {
		r.AchievedRate = math.Round(float64(r.Telegrams-1)/span.Seconds()*100) / 100
	}
	drain := time.NewTimer(10 * time.Second)
	defer drain.Stop()
	for range o.Clients {
		select {
		case <-clientsDone:
		case <-drain.C:
		case <-ctx.Done():
		}
	}
	cancel()
	mu.Lock()
	r.Delivery = summarize(delivery)
	mu.Unlock()
	r.Expected = r.Telegrams * o.Clients
	r.Received = r.Delivery.Count
	r.Pass = r.Received == r.Expected && r.Delivery.P95 <= r.TargetP95
	return r, nil
}

func waitHealthy(ctx context.Context, h *eewbridge.Health) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		if up, capture := h.Status(time.Now()); up == eewbridge.Healthy && capture == eewbridge.Healthy {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("bridge did not become healthy")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// subscribe connects and reads up to sync_complete.
func subscribe(ctx context.Context, url, secret string) (*websocket.Conn, error) {
	dial, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dial, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + secret}}})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(1 << 20)
	sub, _ := json.Marshal(eewbridge.SubscribeMessage{SchemaVersion: eewbridge.SchemaVersion, Type: "subscribe", Environment: "production"})
	if err = conn.Write(dial, websocket.MessageText, sub); err != nil {
		conn.CloseNow()
		return nil, err
	}
	for {
		_, data, err := conn.Read(dial)
		if err != nil {
			conn.CloseNow()
			return nil, err
		}
		var m struct{ Type string }
		if json.Unmarshal(data, &m) == nil && m.Type == "sync_complete" {
			return conn, nil
		}
	}
}

// readLive returns the next live eew.state, skipping health messages.
func readLive(ctx context.Context, conn *websocket.Conn) (eewbridge.StateMessage, error) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return eewbridge.StateMessage{}, err
		}
		var m eewbridge.StateMessage
		if json.Unmarshal(data, &m) == nil && m.Type == "eew.state" && m.Delivery == "live" {
			return m, nil
		}
	}
}

func printReport(w io.Writer, r Report) {
	line := func(label string, s Stats) {
		fmt.Fprintf(w, "  %-28s p50 %7.2f ms  p95 %7.2f ms  p99 %7.2f ms  max %7.2f ms  (n=%d)\n", label, s.P50, s.P95, s.P99, s.Max, s.Count)
	}
	fmt.Fprintf(w, "EEW load test: %d telegrams at %d/s (achieved %.2f/s), %d clients, hand-off over %s\n", r.Telegrams, r.Rate, r.AchievedRate, r.Clients, r.Transport)
	if r.IngestBaseline != nil {
		line("ingest without hand-off", *r.IngestBaseline)
	}
	line("ingest with hand-off", r.Ingest)
	line("receipt to client", r.Delivery)
	result := "FAIL"
	if r.Pass {
		result = "PASS"
	}
	fmt.Fprintf(w, "Received %d of %d messages; target p95 <= %.0f ms: %s\n", r.Received, r.Expected, r.TargetP95, result)
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// errTarget reports a missed target in the exit status.
var errTarget = errors.New("load test target missed")

func main() {
	o := options{}
	flag.StringVar(&o.Dir, "dir", "", "directory for the temporary databases, on the disk under test (default: a new temporary directory)")
	flag.StringVar(&o.Samples, "samples", filepath.Join("internal", "dmdata", "testdata", "jma"), "directory with the JMA sample telegrams")
	flag.IntVar(&o.Rate, "rate", 20, "telegrams per second")
	flag.DurationVar(&o.Duration, "duration", time.Minute, "injection time, whole seconds")
	flag.IntVar(&o.Clients, "clients", 10, "WebSocket clients")
	flag.BoolVar(&o.Baseline, "baseline", true, "first measure ingest without the hand-off on a separate database")
	flag.DurationVar(&o.TargetP95, "target-p95", 150*time.Millisecond, "receipt to client p95 target")
	keep := flag.Bool("keep", false, "keep the databases")
	asJSON := flag.Bool("json", false, "print the report as JSON")
	flag.Parse()
	err := func() error {
		dir, err := os.MkdirTemp(o.Dir, "eew-loadtest-")
		if err != nil {
			return err
		}
		if *keep {
			fmt.Fprintln(os.Stderr, "Databases kept in", dir)
		} else {
			defer os.RemoveAll(dir)
		}
		o.Dir = dir
		ctx, stop := signalContext()
		defer stop()
		r, err := run(ctx, o, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
		if err != nil {
			return err
		}
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(r)
		} else {
			printReport(os.Stdout, r)
		}
		if !r.Pass {
			return errTarget
		}
		return nil
	}()
	switch {
	case errors.Is(err, errTarget):
		os.Exit(3)
	case err != nil:
		fmt.Fprintln(os.Stderr, strings.TrimSpace(err.Error()))
		os.Exit(1)
	}
}
