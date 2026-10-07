package dmdata

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type Stats struct {
	Connected   bool      `json:"connected"`
	LastFrameAt time.Time `json:"last_frame_at"`
	LastDataAt  time.Time `json:"last_data_at"`
	Reconnects  uint64    `json:"reconnects"`
	Rejected    uint64    `json:"rejected"`
	LastError   string    `json:"last_error,omitempty"`
}
type Client struct {
	token, authMode   string
	subscription      socketRequest
	http              *http.Client
	baseURL           string
	allowTestEndpoint bool
	mu                sync.Mutex
	stats             Stats
}

type socketRequest struct {
	Classifications []string `json:"classifications"`
	Types           []string `json:"types"`
	Test            string   `json:"test"`
	AppName         string   `json:"appName"`
	FormatMode      string   `json:"formatMode"`
}

func New(token, authMode string) (*Client, error) {
	return NewWithClassifications(token, authMode, nil)
}
func NewWithClassifications(token, authMode string, classifications []string) (*Client, error) {
	if classifications == nil {
		classifications = []string{"eew.forecast", "eew.warning", "telegram.earthquake"}
	}
	request := socketRequest{Test: "no", AppName: "Quick Relay", FormatMode: "json"}
	supported := map[string][]string{"eew.forecast": {"VXSE45"}, "eew.warning": {"VXSE43"}, "telegram.earthquake": {"VXSE51", "VXSE52", "VXSE53"}}
	seen := make(map[string]bool)
	for _, value := range classifications {
		name := strings.TrimSpace(value)
		types, ok := supported[name]
		if !ok || seen[name] {
			return nil, errors.New("DMDATA_CLASSIFICATIONS contains an unsupported or duplicate classification")
		}
		seen[name] = true
		request.Classifications = append(request.Classifications, name)
		request.Types = append(request.Types, types...)
	}
	if len(request.Classifications) == 0 {
		return nil, errors.New("DMDATA_CLASSIFICATIONS must not be empty")
	}

	if token == "" {
		return nil, errors.New("DMDATA_TOKEN is required")
	}
	if authMode != "api_key" && authMode != "oauth" {
		return nil, errors.New("DMDATA_AUTH_MODE must be api_key or oauth")
	}
	return &Client{token: token, authMode: authMode, subscription: request, baseURL: "https://api.dmdata.jp/v2", http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Snapshot() Stats { c.mu.Lock(); defer c.mu.Unlock(); return c.stats }
func (c *Client) Rejected()       { c.mu.Lock(); c.stats.Rejected++; c.mu.Unlock() }
func (c *Client) authorize(req *http.Request) {
	if c.authMode == "api_key" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.token+":")))
	} else {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

type ticket struct {
	Status    string `json:"status"`
	WebSocket struct {
		ID  json.RawMessage `json:"id"`
		URL string          `json:"url"`
	} `json:"websocket"`
}

func (c *Client) start(ctx context.Context) (ticket, error) {
	var t ticket
	body, _ := json.Marshal(c.subscription)
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/socket", bytes.NewReader(body))
	if err != nil {
		return t, errors.New("invalid socket start URL")
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return t, errors.New("DMDATA socket start transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return t, fmt.Errorf("DMDATA socket start HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(b) > 65536 {
		return t, errors.New("invalid socket start response")
	}
	if err = json.Unmarshal(b, &t); err != nil || t.Status != "ok" {
		return t, errors.New("DMDATA socket start failed")
	}
	u, err := url.Parse(t.WebSocket.URL)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return t, errors.New("invalid WebSocket URL")
	}
	if !c.allowTestEndpoint && (u.Scheme != "wss" || !strings.HasSuffix(u.Hostname(), ".api.dmdata.jp") || (u.Port() != "" && u.Port() != "443")) {
		return t, errors.New("untrusted DMDATA WebSocket endpoint")
	}
	if _, err = socketID(t); err != nil {
		return t, err
	}
	return t, nil
}
func socketID(t ticket) (string, error) {
	raw := strings.Trim(string(t.WebSocket.ID), "\"")
	if _, err := strconv.ParseUint(raw, 10, 64); err != nil {
		return "", errors.New("invalid DMDATA socket id")
	}
	return raw, nil
}
func (c *Client) closeSocket(t ticket) {
	id, err := socketID(t)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+"/socket/"+id, nil)
	if err != nil {
		return
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}
}
func (c *Client) session(parent context.Context, handle func(context.Context, []byte) error) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	t, err := c.start(ctx)
	if err != nil {
		return err
	}
	defer c.closeSocket(t)
	conn, _, err := websocket.Dial(ctx, t.WebSocket.URL, &websocket.DialOptions{Subprotocols: []string{"dmdata.v2"}, HTTPClient: c.http})
	if err != nil {
		return errors.New("DMDATA WebSocket connection failed")
	} // ticket is secret.
	defer conn.CloseNow()
	if conn.Subprotocol() != "dmdata.v2" {
		return errors.New("DMDATA subprotocol mismatch")
	}
	conn.SetReadLimit(MaxMessageBytes)
	c.mu.Lock()
	c.stats.Connected = true
	c.stats.LastError = ""
	c.stats.LastFrameAt = time.Now()
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.stats.Connected = false; c.mu.Unlock() }()
	frames := make(chan []byte, 256)
	errs := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := c.read(ctx, conn, frames)
		errs <- err
	}()
	defer func() { cancel(); conn.CloseNow(); <-done }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case readErr := <-errs:
			// The reader has stopped. Persist frames already received before reconnecting.
			for {
				select {
				case frame := <-frames:
					if err := handle(ctx, frame); err != nil {
						return err
					}
					c.mu.Lock()
					c.stats.LastDataAt = time.Now()
					c.mu.Unlock()
				default:
					return readErr
				}
			}
		case frame := <-frames:
			if err := handle(ctx, frame); err != nil {
				return err
			}
			c.mu.Lock()
			c.stats.LastDataAt = time.Now()
			c.mu.Unlock()
		}
	}
}
func (c *Client) read(ctx context.Context, conn *websocket.Conn, frames chan<- []byte) error {
	for {
		readCtx, cancel := context.WithTimeout(ctx, 100*time.Second)
		_, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			return errors.New("DMDATA WebSocket disconnected or heartbeat timed out")
		}
		c.mu.Lock()
		c.stats.LastFrameAt = time.Now()
		c.mu.Unlock()
		var msg struct {
			Type   string          `json:"type"`
			PingID json.RawMessage `json:"pingId"`
			Close  bool            `json:"close"`
		}
		if err = json.Unmarshal(data, &msg); err != nil {
			return errors.New("invalid DMDATA frame")
		}
		switch msg.Type {
		case "ping":
			if len(msg.PingID) > 1024 {
				return errors.New("invalid ping id")
			}
			pong := map[string]any{"type": "pong"}
			if len(msg.PingID) > 0 {
				pong["pingId"] = msg.PingID
			}
			out, _ := json.Marshal(pong)
			writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, out)
			stop()
			if err != nil {
				return errors.New("DMDATA pong failed")
			}
		case "data":
			select {
			case frames <- data:
			case <-ctx.Done():
				return ctx.Err()
			default:
				return errors.New("DMDATA ingestion queue overflow; reception gap")
			}
		case "error":
			return errors.New("DMDATA server reported an error")
		case "start", "pong":
		default:
			c.Rejected()
		}
	}
}

// Run always obtains a fresh ticket. It does not claim to backfill a disconnected interval.
func (c *Client) Run(ctx context.Context, handle func(context.Context, []byte) error) {
	delay := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := c.session(ctx, handle)
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		c.stats.Reconnects++
		if err != nil {
			c.stats.LastError = err.Error()
		}
		c.mu.Unlock()
		if time.Since(started) > time.Minute {
			delay = time.Second
		}
		wait := delay + time.Duration(rand.Int64N(int64(delay/2)+1))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 60*time.Second)
	}
}
