package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/dmdata"
)

var samples = filepath.Join("..", "..", "internal", "dmdata", "testdata", "jma")

func TestSyntheticTelegramsAreCurrentAndOrdered(t *testing.T) {
	tg, err := loadTelegrams(samples)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, want := range map[int]string{0: "VXSE45", 3: "VXSE43", 20: "VXSE45"} {
		id, frame, err := tg.frame(i, now)
		if err != nil {
			t.Fatal(err)
		}
		r, err := dmdata.Normalize(frame, now)
		if err != nil {
			t.Fatal(i, err)
		}
		if r.TelegramType != want || r.MessageID != id || r.ReportedAt.After(now) || now.Sub(r.ReportedAt) > time.Second {
			t.Fatal(i, r.TelegramType, r.MessageID, r.ReportedAt)
		}
		if i == 20 && r.SourceEventID != "90000000000001" {
			t.Fatal("events do not advance", r.SourceEventID)
		}
	}
}

func TestLoadRunDeliversEveryTelegram(t *testing.T) {
	o := options{Dir: t.TempDir(), Samples: samples, Rate: 20, Duration: time.Second, Clients: 3, Baseline: true, TargetP95: time.Minute}
	r, err := run(context.Background(), o, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Pass || r.Expected != 60 || r.Received != 60 || r.Ingest.Count != 20 || r.IngestBaseline == nil || r.IngestBaseline.Count != 20 {
		t.Fatalf("%+v", r)
	}
	var out bytes.Buffer
	printReport(&out, r)
	if !strings.Contains(out.String(), "Received 60 of 60 messages") || !strings.Contains(out.String(), "PASS") {
		t.Fatal(out.String())
	}
	if _, err = run(context.Background(), options{Dir: t.TempDir(), Samples: samples, Rate: 20, Duration: 500 * time.Millisecond, Clients: 1}, nil); err == nil {
		t.Fatal("accepted a duration under one second")
	}
}

func TestSummarize(t *testing.T) {
	var values []time.Duration
	for i := 1; i <= 100; i++ {
		values = append(values, time.Duration(i)*time.Millisecond)
	}
	if s := summarize(values); s.P50 != 50 || s.P95 != 95 || s.P99 != 99 || s.Max != 100 || s.Count != 100 {
		t.Fatalf("%+v", s)
	}
	if s := summarize(nil); s.Count != 0 {
		t.Fatal(s)
	}
}
