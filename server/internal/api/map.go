package api

import (
	"context"
	"net/http"
	"quakerelay/server/internal/dmdata"
)

type StationProvider interface {
	StationCatalog(context.Context) (dmdata.StationCatalog, error)
}

func (s *Server) mapStations(w http.ResponseWriter, r *http.Request, _ string) {
	if s.StationSource == nil {
		reply(w, 503, map[string]any{"ok": false, "error": map[string]string{"code": "map_catalog_unavailable", "message": "Station locations are not configured."}})
		return
	}
	catalog, err := s.StationSource.StationCatalog(r.Context())
	if err != nil {
		// Provider errors may contain private data. Never forward them to clients.
		reply(w, 502, map[string]any{"ok": false, "error": map[string]string{"code": "map_catalog_unavailable", "message": "Station locations could not be retrieved."}})
		return
	}
	reply(w, 200, struct {
		OK     bool   `json:"ok"`
		Source string `json:"source"`
		dmdata.StationCatalog
	}{true, "DMDATA earthquake station parameters", catalog})
}
