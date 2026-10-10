package dmdata

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"
)

const maxStationCatalogBytes = 8 * 1024 * 1024

type MapStation struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	RegionName string  `json:"region_name"`
	CityName   string  `json:"city_name"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	Status     string  `json:"status"`
}
type StationCatalog struct {
	Version    string       `json:"version"`
	ChangeTime time.Time    `json:"change_time"`
	FetchedAt  time.Time    `json:"fetched_at"`
	Stale      bool         `json:"stale"`
	Items      []MapStation `json:"items"`
}

// Parameters are fetched at most every three days, independently of the reception lock.
// Only authenticated local clients receive location metadata; upstream credentials
// and subscriber-only parameter files never enter the public repository.
func (c *Client) StationCatalog(ctx context.Context) (StationCatalog, error) {
	c.parameterMu.Lock()
	defer c.parameterMu.Unlock()
	now := time.Now().UTC()
	if now.Before(c.parameterNextAttempt) {
		if c.parameterCatalog != nil {
			return *c.parameterCatalog, nil
		}
		return StationCatalog{}, errors.New("station catalog unavailable")
	}
	c.parameterNextAttempt = now.Add(24 * time.Hour)
	catalog, err := c.fetchStations(ctx, now)
	if err != nil {
		// Closing a map cancels its request; allow the next map to retry.
		// The provider-failure cooldown must not persist a client cancellation.
		if ctx.Err() != nil {
			c.parameterNextAttempt = time.Time{}
		}
		if c.parameterCatalog != nil {
			old := *c.parameterCatalog
			old.Stale = true
			c.parameterCatalog = &old
			return old, nil
		}
		return StationCatalog{}, err
	}
	c.parameterCatalog = &catalog
	c.parameterNextAttempt = now.Add(72 * time.Hour)
	return catalog, nil
}
func (c *Client) fetchStations(ctx context.Context, now time.Time) (StationCatalog, error) {
	var catalog StationCatalog
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/parameter/earthquake/station", nil)
	if err != nil {
		return catalog, errors.New("invalid station catalog request")
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return catalog, errors.New("station catalog transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return catalog, errors.New("station catalog authorization or provider failure")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxStationCatalogBytes+1))
	if err != nil || len(data) > maxStationCatalogBytes {
		return catalog, errors.New("invalid station catalog size")
	}
	var raw struct {
		Status     string    `json:"status"`
		Version    string    `json:"version"`
		ChangeTime time.Time `json:"changeTime"`
		Items      []struct {
			Code   string `json:"code"`
			Name   string `json:"name"`
			Region struct {
				Name string `json:"name"`
			} `json:"region"`
			City struct {
				Name string `json:"name"`
			} `json:"city"`
			Latitude  string `json:"latitude"`
			Longitude string `json:"longitude"`
			Status    string `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &raw) != nil || raw.Status != "ok" || raw.ChangeTime.IsZero() || len(raw.Items) > 20000 {
		return catalog, errors.New("invalid station catalog data")
	}
	catalog = StationCatalog{Version: raw.Version, ChangeTime: raw.ChangeTime, FetchedAt: now, Items: []MapStation{}}
	for _, item := range raw.Items {
		lat, e1 := strconv.ParseFloat(item.Latitude, 64)
		lon, e2 := strconv.ParseFloat(item.Longitude, 64)
		if e1 != nil || e2 != nil || math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) ||
			lat < -90 || lat > 90 || lon < -180 || lon > 180 || item.Code == "" || item.Name == "" {
			continue
		}
		switch item.Status {
		case "現", "変更", "新規", "廃止":
		default:
			continue
		}
		catalog.Items = append(catalog.Items, MapStation{Code: item.Code, Name: item.Name, RegionName: item.Region.Name,
			CityName: item.City.Name, Latitude: lat, Longitude: lon, Status: item.Status})
	}
	if len(catalog.Items) == 0 {
		return catalog, errors.New("station catalog contains no valid positions")
	}
	return catalog, nil
}
