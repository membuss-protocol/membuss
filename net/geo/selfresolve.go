package geo

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Coords holds approximate geolocation for the local node.
type Coords struct {
	Country string  `json:"country"`
	City    string  `json:"city"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

var (
	// Self holds the local node's geolocation. Nil when
	// resolution failed or hasn't been attempted yet.
	Self *Coords
	once sync.Once
)

type apiResponse struct {
	Status  string  `json:"status"`
	Country string  `json:"country"`
	City    string  `json:"city"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

// services is an ordered list of free IP geolocation APIs.
// Each returns lat/lon/country/city for the caller's public IP.
var services = []struct {
	name string
	url  string
}{
	{"ip-api.com", "http://ip-api.com/json"},
	{"ipwho.is", "https://ipwho.is"},
	{"ipapi.co", "https://ipapi.co/json"},
}

// ResolveSelf resolves the node's own public IP to geographic
// coordinates using a free API. Tries multiple services for
// reliability. Safe to call multiple times — only executes once.
func ResolveSelf() *Coords {
	once.Do(func() {
		client := &http.Client{Timeout: 5 * time.Second}
		for _, svc := range services {
			resp, err := client.Get(svc.url)
			if err != nil {
				slog.Debug("geo: self-resolve attempt failed", "service", svc.name, "err", err)
				continue
			}
			func() {
				defer resp.Body.Close()
				var r apiResponse
				if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
					slog.Debug("geo: self-resolve decode failed", "service", svc.name, "err", err)
					return
				}
				// ip-api.com uses "success", ipwho.is uses "true" (bool as string),
				// ipapi.co always returns fields when successful.
				if r.Status != "success" && r.Status != "true" && r.Status != "" {
					slog.Debug("geo: self-resolve api status", "service", svc.name, "status", r.Status)
					return
				}
				if r.Country == "" && r.Lat == 0 && r.Lon == 0 {
					slog.Debug("geo: self-resolve empty result", "service", svc.name)
					return
				}
				Self = &Coords{
					Country: r.Country,
					City:    r.City,
					Lat:     r.Lat,
					Lon:     r.Lon,
				}
				slog.Info("geo: self-resolved", "service", svc.name, "country", Self.Country, "city", Self.City)
			}()
			if Self != nil {
				return
			}
		}
		if Self == nil {
			slog.Warn("geo: self-resolve failed — all services unavailable, peers will show no location")
		}
	})
	return Self
}
