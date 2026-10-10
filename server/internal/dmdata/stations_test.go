package dmdata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const stationFixture = `{"status":"ok","version":"synthetic-v1","changeTime":"2026-01-01T00:00:00Z","items":[{"code":"test001","name":"合成観測点","region":{"name":"合成地域"},"city":{"name":"合成市"},"latitude":"35.1","longitude":"139.2","status":"現"},{"code":"bad","name":"合成無効点","latitude":"NaN","longitude":"999","status":"現"}]}`

func TestStationCatalogAuthenticationCacheAndStaleFallback(t *testing.T) {
	for _, mode := range []string{"api_key", "oauth"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			var fail atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				expected := "Basic dGVzdC1rZXk6"
				if mode == "oauth" {
					expected = "Bearer test-key"
				}
				if r.Header.Get("Authorization") != expected || r.URL.Path != "/parameter/earthquake/station" {
					t.Error("wrong authorized parameter request")
				}
				if fail.Load() {
					http.Error(w, "private-provider-error", 403)
					return
				}
				fmt.Fprint(w, stationFixture)
			}))
			defer server.Close()
			c, _ := New("test-key", mode)
			c.baseURL = server.URL
			catalog, err := c.StationCatalog(context.Background())
			if err != nil || catalog.Stale || len(catalog.Items) != 1 || catalog.Items[0].Name != "合成観測点" || catalog.FetchedAt.IsZero() {
				t.Fatal(catalog, err)
			}
			_, _ = c.StationCatalog(context.Background())
			if calls.Load() != 1 || time.Until(c.parameterNextAttempt) < 71*time.Hour {
				t.Fatal("cache not honored", calls.Load())
			}
			fail.Store(true)
			c.parameterNextAttempt = time.Time{}
			stale, err := c.StationCatalog(context.Background())
			if err != nil || !stale.Stale || stale.Items[0].Code != "test001" || !stale.FetchedAt.Equal(catalog.FetchedAt) {
				t.Fatal("last good catalog lost", stale, err)
			}
			_, _ = c.StationCatalog(context.Background())
			if calls.Load() != 2 {
				t.Fatal("failed provider retried too frequently")
			}
		})
	}
}
func TestStationCatalogRefusesRedirectAndInvalidResponses(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	for _, body := range []string{"redirect", `{"status":"error"}`, strings.Repeat("x", maxStationCatalogBytes+1), strings.ReplaceAll(stationFixture, `"35.1"`, `"999"`)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if body == "redirect" {
				http.Redirect(w, r, target.URL, 302)
				return
			}
			fmt.Fprint(w, body)
		}))
		c, _ := New("test-key", "api_key")
		c.baseURL = server.URL
		if _, err := c.StationCatalog(context.Background()); err == nil {
			t.Error("invalid catalog accepted")
		}
		server.Close()
	}
	if leaked.Load() != 0 {
		t.Fatal("credentials could leak on redirect")
	}
}
func TestStationParameterFetchDoesNotBlockReceptionSnapshot(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release; fmt.Fprint(w, stationFixture) }))
	defer server.Close()
	c, _ := New("test-key", "api_key")
	c.baseURL = server.URL
	done := make(chan struct{})
	go func() { defer close(done); _, _ = c.StationCatalog(context.Background()) }()
	<-started
	snapshot := make(chan struct{})
	go func() { c.Snapshot(); close(snapshot) }()
	select {
	case <-snapshot:
	case <-time.After(time.Second):
		t.Error("metadata fetch blocked receiver lock")
	}
	close(release)
	<-done
}

func TestCancelledStationFetchCanRetryWhenMapReopens(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, stationFixture)
	}))
	defer server.Close()
	c, _ := New("test-key", "api_key")
	c.baseURL = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.StationCatalog(ctx); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("initial station request did not arrive")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled initial fetch unexpectedly succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled station request did not finish")
	}
	catalog, err := c.StationCatalog(context.Background())
	if err != nil || len(catalog.Items) != 1 || calls.Load() != 2 {
		t.Fatal("reopening the map was blocked by a cancelled request", catalog, err, calls.Load())
	}
}
