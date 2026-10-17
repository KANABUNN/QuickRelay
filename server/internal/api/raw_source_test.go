package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/store"
)

func TestXMLSourceIsAuthenticatedLosslessAndDownloadable(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	ctx := context.Background()
	code, err := st.CreatePairing(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := st.Pair(ctx, code, "phone", strings.Repeat("s", 32), now)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../dmdata/testdata/jma/38-39_02_01_250206_VTSE41.xml")
	if err != nil {
		t.Fatal(err)
	}
	e := dmdata.Envelope{Type: "data", ID: "source-xml", Classification: "telegram.earthquake", Format: "xml", Encoding: "utf-8", Body: string(data)}
	e.Head.Type = "VTSE41"
	frame, _ := json.Marshal(e)
	report, err := dmdata.Normalize(frame, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.IngestHistorical(ctx, report); err != nil {
		t.Fatal(err)
	}
	app := New(st, strings.Repeat("s", 32))
	for _, auth := range []string{"", token} {
		req := httptest.NewRequest("GET", "/api/v1/reports/"+report.ID+"/source", nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, req)
		if auth == "" {
			if response.Code != 401 {
				t.Fatal("source exposed")
			}
			continue
		}
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), data) || response.Header().Get("Content-Type") != "application/xml; charset=utf-8" || !strings.HasSuffix(response.Header().Get("Content-Disposition"), ".xml\"") || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("source changed", response.Code, response.Header())
		}
	}
}
