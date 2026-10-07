package dmdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSocketStartPingPongFreshTicketAndCleanup(t *testing.T) {
	var starts, closes atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/socket":
			if r.Header.Get("Authorization") != "Basic dGVzdC1rZXk6" {
				t.Error("bad basic auth")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["formatMode"] != "json" || body["test"] != "no" || body["appName"] != "Quick Relay" {
				t.Error("bad start options")
			}
			starts.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "websocket": map[string]any{"id": starts.Load(), "url": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?ticket=secret"}})
		case r.Method == "DELETE":
			closes.Add(1)
			w.WriteHeader(204)
		case r.URL.Path == "/ws":
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"dmdata.v2"}})
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.CloseNow()
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ping","pingId":"test"}`))
			_, b, err := conn.Read(ctx)
			if err != nil || !strings.Contains(string(b), `"pingId":"test"`) || !strings.Contains(string(b), `"type":"pong"`) {
				t.Error("missing pong", err)
			}
			_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"data","id":"received"}`))
			<-ctx.Done()
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, _ := New("test-key", "api_key")
	c.baseURL = server.URL
	c.allowTestEndpoint = true
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		received := false
		_ = c.session(ctx, func(_ context.Context, b []byte) error { received = true; cancel(); return nil })
		cancel()
		if !received {
			t.Fatal("no data")
		}
	}
	if starts.Load() != 2 || closes.Load() != 2 {
		t.Fatal("ticket reuse or missing cleanup", starts.Load(), closes.Load())
	}
}

func TestDisconnectDrainsAlreadyReceivedFrames(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "websocket": map[string]any{"id": 1, "url": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
			return
		}
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"dmdata.v2"}})
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		for range 20 {
			if err = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"data"}`)); err != nil {
				return
			}
		}
		_ = conn.Close(websocket.StatusNormalClosure, "test")
	}))
	defer server.Close()
	c, _ := New("key", "api_key")
	c.baseURL = server.URL
	c.allowTestEndpoint = true
	count := 0
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.session(ctx, func(context.Context, []byte) error {
		count++
		time.Sleep(time.Millisecond)
		return nil
	})
	if count != 20 {
		t.Fatalf("lost received frames: %d/20", count)
	}
}

func TestSubscriptionMatchesConfiguredContracts(t *testing.T) {
	c, err := NewWithClassifications("test-key", "api_key", []string{" eew.warning ", "telegram.earthquake"})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got socketRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		if got.Test != "no" || got.FormatMode != "json" || got.AppName != "Quick Relay" ||
			!reflect.DeepEqual(got.Classifications, []string{"eew.warning", "telegram.earthquake"}) ||
			!reflect.DeepEqual(got.Types, []string{"VXSE43", "VXSE51", "VXSE52", "VXSE53"}) {
			t.Error("subscription does not match configured contracts")
		}
		_, _ = w.Write([]byte(`{"status":"ok","websocket":{"id":1,"url":"wss://ws.api.dmdata.jp/v2/websocket?ticket=test"}}`))
	}))
	defer s.Close()
	c.baseURL = s.URL
	if _, err := c.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]string{{}, {""}, {"eew.forecast", "eew.forecast"}, {"telegram.weather"}} {
		if _, err := NewWithClassifications("key", "api_key", invalid); err == nil {
			t.Fatal("invalid subscription accepted")
		}
	}
}

func TestRunReconnectsWithFreshTicketsAfterMalformedFrame(t *testing.T) {
	var starts, closes atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			n := starts.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "websocket": map[string]any{"id": n, "url": "ws" + strings.TrimPrefix(server.URL, "http") + fmt.Sprintf("/ws?ticket=fresh-%d", n)}})
		case "DELETE":
			closes.Add(1)
			w.WriteHeader(204)
		default:
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"dmdata.v2"}})
			if err != nil {
				return
			}
			defer conn.CloseNow()
			ctx, stop := context.WithTimeout(r.Context(), 4*time.Second)
			defer stop()
			if r.URL.Query().Get("ticket") == "fresh-1" {
				_ = conn.Write(ctx, websocket.MessageText, []byte("{malformed"))
			} else {
				if r.URL.Query().Get("ticket") != "fresh-2" {
					t.Error("ticket reused")
				}
				_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"data","id":"reconnected"}`))
			}
			_, _, _ = conn.Read(ctx)
		}
	}))
	defer server.Close()
	c, _ := New("test-key", "api_key")
	c.baseURL = server.URL
	c.allowTestEndpoint = true
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	received := false
	c.Run(ctx, func(context.Context, []byte) error { received = true; cancel(); return nil })
	if !received || starts.Load() != 2 || closes.Load() != 2 || c.Snapshot().Reconnects != 1 {
		t.Fatal("reconnect failed", starts.Load(), closes.Load(), c.Snapshot())
	}
}
