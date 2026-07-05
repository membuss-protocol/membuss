# Design: Client-Side Geolocation for Explorer

## Problem

The membus server currently downloads and loads a ~70MB GeoLite2-City MMDB file at startup
to resolve peer IP addresses to geographic coordinates for the explorer map. This:

- Adds ~70MB to startup time and memory footprint
- Requires auto-download logic with external dependency (GitHub releases)
- Requires the `maxminddb-golang` library
- Fails silently if download fails, degrading explorer experience
- Centralizes geolocation on the server — a single point of failure

## Goal

Move geolocation entirely to the client side:

1. Each node self-resolves its own IP via a free CDN API on startup
2. Location is propagated via existing PEX gossip (no new protocol)
3. Explorer frontend renders an interactive D3 worldmap (replacing Leaflet + tile server)
4. Remove all server-side MMDB dependencies

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Geo resolution model | Client self-resolve | No server DB needed, no ongoing API calls |
| Reporting mechanism | Startup resolution + PEX announce | One-time cost, signed via existing PEX sig |
| Map library | D3.js with SVG | Lightweight, interactive, no tile server dependency |
| Geo API | ip-api.com | Free, no API key, 45 RPM (sufficient for 1 call/node) |
| Wire format | Add fields to existing PeerInfo proto | Clean, signed, uses existing gossip |

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│  Node Startup                                            │
│                                                          │
│  1. Create libp2p host                                   │
│  2. Call ip-api.com/json → { lat, lon, country, city }  │
│  3. Store in geo.Self (package-level var)                │
│  4. buildSelfPeerInfo() includes geo fields              │
│  5. PEX gossip propagates to all peers                   │
└──────────────────────┬──────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────┐
│  PEX Wire (PeerInfo proto)                               │
│                                                          │
│  peer_id, addrs, relay_addrs, ...                        │
│  country = "Germany"      ← NEW                          │
│  city     = "Berlin"      ← NEW                          │
│  lat      = 52.52          ← NEW                         │
│  lon      = 13.405         ← NEW                         │
│  signature = ...  (covers all fields including new)      │
└──────────────────────┬──────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────┐
│  Explorer Frontend (D3 SVG Map)                          │
│                                                          │
│  GET /peers?format=json → [{ PeerID, Country, Lat, ...}]│
│                                                          │
│  ┌─────────────────────────────────────────┐             │
│  │  ○ Berlin (anchor)                      │             │
│  │      ○ Tokyo        ○ New York          │             │
│  │          ○ Sydney                       │             │
│  │  ○ São Paulo                            │             │
│  └─────────────────────────────────────────┘             │
│  Dark SVG worldmap + peer circles + hover/click          │
└─────────────────────────────────────────────────────────┘
```

## Detailed Changes

### 1. Proto Changes (`rpc/proto/membuss.proto`)

Add to `PeerInfo` (PEX wire format):

```protobuf
message PeerInfo {
  // ... existing fields (1-9) ...
  string country = 10;
  string city    = 11;
  double lat     = 12;
  double lon     = 13;
}
```

Add to `NodePeerInfo` (gRPC):

```protobuf
message NodePeerInfo {
  // ... existing fields (1-3) ...
  string country = 4;
  string city    = 5;
  double lat     = 6;
  double lon     = 7;
}
```

### 2. Self-Resolution (new file: `internal/geo/selfresolve.go`)

```go
package geo

import (
    "encoding/json"
    "net/http"
    "sync"
)

type Coords struct {
    Country string  `json:"country"`
    City    string  `json:"city"`
    Lat     float64 `json:"lat"`
    Lon     float64 `json:"lon"`
}

var (
    Self *Coords
    once sync.Once
)

func ResolveSelf() *Coords {
    once.Do(func() {
        client := &http.Client{Timeout: 5 * time.Second}
        resp, err := client.Get("http://ip-api.com/json")
        if err != nil {
            return
        }
        defer resp.Body.Close()
        var result struct {
            Country string  `json:"country"`
            City    string  `json:"city"`
            Lat     float64 `json:"lat"`
            Lon     float64 `json:"lon"`
            Status  string  `json:"status"`
        }
        if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Status != "success" {
            return
        }
        Self = &Coords{
            Country: result.Country,
            City:    result.City,
            Lat:     result.Lat,
            Lon:     result.Lon,
        }
    })
    return Self
}
```

Called in `main.go` after host creation, before PEX starts.

### 3. PEX Changes (`net/pex/pex.go`)

In `buildSelfPeerInfo()`:

```go
if g := geo.Self; g != nil {
    pi.Country = g.Country
    pi.City = g.City
    pi.Lat = g.Lat
    pi.Lon = g.Lon
}
```

In `mergeFromMessage()` — geo fields are already decoded by protobuf. No explicit handling needed.

### 4. Explorer Backend Changes

**`gateway/explorer/explorer.go` `handlePeers()`:**

Remove the `e.geo.Lookup(ip)` block. The backend adapter now returns pre-resolved location:

```go
func (e *Explorer) handlePeers(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    peers, _ := e.cfg.Backend.Peers(ctx, e.cfg.PeerLimit)
    // No more geo enrichment — location comes from peer data
    e.render(w, r, "peers.html", peersData{...})
}
```

**`cmd/membuss/explorer_backend.go` `Peers()`:**

Map PEX geo fields to explorer PeerInfo:

```go
func (b *explorerAdapter) Peers(ctx context.Context, limit int) ([]explorer.PeerInfo, error) {
    pexPeers := b.pex.Peers()
    anchors := b.getKnownAnchors()
    result := make([]explorer.PeerInfo, 0, len(pexPeers))
    for _, p := range pexPeers {
        result = append(result, explorer.PeerInfo{
            PeerID:   p.PeerID,
            Addrs:    p.Addrs,
            IsAnchor: anchors[p.PeerID],
            Country:  p.Country,
            City:     p.City,
            Lat:      p.Lat,
            Lon:      p.Lon,
        })
    }
    return result, nil
}
```

### 5. Frontend Changes (`explorer-web/`)

**Replace Leaflet with D3.js:**

- `src/routes/peers/+page.svelte` — rewrite map section:
  - Load world TopoJSON (bundled `static/world-110m.json`, ~100KB)
  - `d3.geoNaturalEarth1()` projection
  - SVG `<path>` for country outlines (dark theme: `#1a1a2e` fill, `#16213e` stroke)
  - `<circle>` for peers: green = regular, blue = anchor
  - `d3.zoom()` for mouse wheel + drag pan/zoom
  - Hover tooltip with peer details
  - Click to copy peer ID
  - Auto-fit bounds on load
- `package.json` — add `d3`, `@types/d3`; remove `leaflet`, `@types/leaflet`
- `static/world-110m.json` — TopoJSON world atlas (~100KB)

**Keep existing table** below the map — it already displays location, just remove the Leaflet CSS import.

### 6. Removals

| File/Code | What | Why |
|-----------|------|-----|
| `gateway/explorer/geo.go` | GeoResolver, GeoResult, MMDB lookup | Replaced by self-resolve |
| `cmd/membuss/main.go:961-1073` | `ensureGeoIPDatabase()` | No more MMDB download |
| `cmd/membuss/main.go:471-473` | GeoResolver creation | No more MMDB |
| `config/config.go:160-170` | `EnableGeolocation`, `GeolocationDB` | No config needed |
| `config/write.go:190-195` | Geolocation config template section | No config needed |
| `cmd/membuss-entrypoint/main.go:128` | `MEMBUSS_ENABLE_GEOLOCATION` env var | No config needed |
| `desktop/daemon.go:705-706` | geolocation_db path normalization | No config needed |
| `desktop/app.go:164-168` | geolocation_db path normalization | No config needed |
| `go.mod` | `github.com/oschwald/maxminddb-golang` | No more MMDB |
| `explorer-web/package.json` | `leaflet`, `@types/leaflet` | Replaced by D3 |

### 7. Wire Compatibility

- New fields (tags 10-13) are **backward compatible** — old nodes ignore unknown protobuf fields
- Old nodes won't populate geo fields → peers on old versions show no location on new explorers
- This is acceptable graceful degradation
- No protocol version bump needed (fields are additive)

### 8. Config Migration

- `enable_geolocation` and `geolocation_db` YAML fields become no-ops
- Go's YAML parser ignores unknown fields — old configs still parse
- Log deprecation warning if these fields are detected:
  ```
  WARN: enable_geolocation and geolocation_db are deprecated and ignored.
        Geolocation is now self-resolved by each node.
  ```

## Edge Cases

| Scenario | Behavior |
|----------|----------|
| Node behind NAT | ip-api.com returns public IP coords — works correctly |
| Node can't reach ip-api.com | `geo.Self` stays nil, peer has no location — graceful degradation |
| Dynamic IP changes | Location re-resolved on next restart |
| ip-api.com rate limit (45 RPM) | Not a concern — 1 call per node at startup |
| ip-api.com downtime | Same as "can't reach" — no location, graceful degradation |
| Old nodes in network | Won't have geo fields — show as "Unknown" location on map |

## Testing

1. **Unit test** `internal/geo/selfresolve.go` — mock HTTP server returning known coords
2. **Integration test** — start node, verify PEX messages include geo fields
3. **Frontend test** — load peers page, verify D3 map renders peer circles at correct positions
4. **Graceful degradation test** — block ip-api.com, verify node starts without crash, peers show no location
5. **Wire compat test** — old node + new node in same network, verify no crash, old peers show no location
