package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/store"
	"strings"
	"testing"
	"time"
)

type mapFixture struct {
	fail  bool
	calls int
}

func (f *mapFixture) StationCatalog(context.Context) (dmdata.StationCatalog, error) {
	f.calls++
	if f.fail {
		return dmdata.StationCatalog{}, errors.New("PRIVATE_KEY_SENTINEL")
	}
	return dmdata.StationCatalog{Version: "synthetic", ChangeTime: time.Now(), FetchedAt: time.Now(), Items: []dmdata.MapStation{{Code: "fixture", Name: "合成点", Latitude: 35, Longitude: 139, Status: "現"}}}, nil
}
func TestMapStationsRequiresDeviceAuthAndHidesProviderErrors(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := strings.Repeat("s", 32)
	ctx := context.Background()
	now := time.Now()
	code, _ := st.CreatePairing(ctx, now)
	token, err := st.Pair(ctx, code, "phone", secret, now)
	if err != nil {
		t.Fatal(err)
	}
	app := New(st, secret)
	fixture := &mapFixture{}
	app.StationSource = fixture
	call := func(auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/v1/map/stations", nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		return w
	}
	if w := call(""); w.Code != 401 || fixture.calls != 0 {
		t.Fatal("anonymous catalog access", w.Code)
	}
	if w := call(token); w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"fixture"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	fixture.fail = true
	if w := call(token); w.Code != 502 || strings.Contains(w.Body.String(), "PRIVATE_KEY_SENTINEL") {
		t.Fatal("private provider error exposed", w.Code)
	}
	app.StationSource = nil
	if w := call(token); w.Code != 503 {
		t.Fatal("offline response", w.Code)
	}
}
