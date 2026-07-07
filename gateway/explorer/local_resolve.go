package explorer

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SelfLocation struct {
	IP      string  `json:"ip"`
	City    string  `json:"city"`
	Country string  `json:"country"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

var (
	selfLocCache *SelfLocation
	selfIPMu     sync.RWMutex
	lastChecked  time.Time
)

// ResolveSelfIP fetches the local node's public IP address and geolocation details.
// It prioritizes ipinfo.io for accurate client-side positioning (e.g., city accuracy).
// Caches the result for 1 hour to prevent excessive calls to external providers.
func ResolveSelfIP(ctx context.Context) *SelfLocation {
	selfIPMu.RLock()
	if selfLocCache != nil && time.Since(lastChecked) < 1*time.Hour {
		loc := selfLocCache
		selfIPMu.RUnlock()
		return loc
	}
	selfIPMu.RUnlock()

	selfIPMu.Lock()
	defer selfIPMu.Unlock()

	// Double check cache under lock
	if selfLocCache != nil && time.Since(lastChecked) < 1*time.Hour {
		return selfLocCache
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// Try ipinfo.io for accurate geolocation including lat/lon
	req, err := http.NewRequestWithContext(ctx, "GET", "https://ipinfo.io/json", nil)
	if err == nil {
		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var res struct {
					IP      string `json:"ip"`
					City    string `json:"city"`
					Country string `json:"country"`
					Loc     string `json:"loc"` // "lat,lon"
				}
				if err := json.NewDecoder(resp.Body).Decode(&res); err == nil && res.IP != "" {
					loc := &SelfLocation{
						IP:      res.IP,
						City:    res.City,
						Country: res.Country,
					}
					if res.Loc != "" {
						parts := strings.Split(res.Loc, ",")
						if len(parts) == 2 {
							lat, _ := strconv.ParseFloat(parts[0], 64)
							lon, _ := strconv.ParseFloat(parts[1], 64)
							loc.Lat = lat
							loc.Lon = lon
						}
					}
					selfLocCache = loc
					lastChecked = time.Now()
					return loc
				}
			}
		}
	}

	// Fallback to simple api.ipify.org
	req, err = http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org?format=json", nil)
	if err == nil {
		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var res struct {
					IP string `json:"ip"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&res); err == nil && res.IP != "" {
					loc := &SelfLocation{
						IP: res.IP,
					}
					selfLocCache = loc
					lastChecked = time.Now()
					return loc
				}
			}
		}
	}

	return nil
}
