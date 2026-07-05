# Client-Side Geolocation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move peer geolocation from server-side MMDB to client-side self-resolution via ip-api.com, replace Leaflet map with D3 SVG worldmap.

**Architecture:** Each node resolves its own IP on startup via ip-api.com, stores coords in package-level var, includes in PEX gossip via existing signed PeerInfo proto. Explorer backend reads pre-resolved coords from PEX table. Frontend renders interactive D3 SVG worldmap replacing Leaflet+tile server.

**Tech Stack:** Go, Protocol Buffers, libp2p PEX, ip-api.com, D3.js, TopoJSON, SvelteKit

---

## File Map

| Action | File | Purpose |
|--------|------|---------|
| Create | `net/geo/selfresolve.go` | Self-resolution via ip-api.com |
| Create | `net/geo/selfresolve_test.go` | Unit tests with mock HTTP server |
| Create | `explorer-web/static/world-110m.json` | TopoJSON world atlas (~100KB) |
| Modify | `rpc/proto/membuss.proto` | Add geo fields to PeerInfo + NodePeerInfo |
| Modify | `net/pex/pex.go` | Include geo in buildSelfPeerInfo |
| Modify | `cmd/membuss/main.go` | Call ResolveSelf, remove MMDB logic |
| Modify | `cmd/membuss/backend.go` | Map PEX geo to NodePeerInfo |
| Modify | `cmd/membuss/explorer_backend.go` | Map PEX geo to explorer PeerInfo |
| Modify | `gateway/explorer/explorer.go` | Remove GeoResolver, simplify handlePeers |
| Modify | `gateway/explorer/geo.go` | DELETE file |
| Modify | `config/config.go` | Remove EnableGeolocation, GeolocationDB |
| Modify | `config/write.go` | Remove geolocation template section |
| Modify | `cmd/membuss-entrypoint/main.go` | Remove MEMBUSS_ENABLE_GEOLOCATION |
| Modify | `desktop/daemon.go` | Remove geolocation_db normalization |
| Modify | `desktop/app.go` | Remove geolocation_db normalization |
| Modify | `go.mod` | Remove maxminddb-golang |
| Modify | `explorer-web/package.json` | Swap leaflet → d3 |
| Modify | `explorer-web/src/routes/peers/+page.svelte` | Rewrite map with D3 |

---

## Task 1: Proto Changes

**Files:**
- Modify: `rpc/proto/membuss.proto:212-222` (PeerInfo)
- Modify: `rpc/proto/membuss.proto:119-123` (NodePeerInfo)

- [ ] **Step 1: Add geo fields to PeerInfo**

In `rpc/proto/membuss.proto`, add fields after the existing `pub_key` and `seq` fields in `PeerInfo`:

```protobuf
message PeerInfo {
  string        peer_id            = 1;
  repeated string addrs            = 2;
  repeated string relay_addrs      = 3;
  int64          last_seen         = 4;
  Reachability   reachability      = 5;
  bool           last_dial_success = 6;
  bytes          signature         = 7;
  bytes          pub_key           = 8;
  int64          seq               = 9;
  string         country           = 10;
  string         city              = 11;
  double         lat               = 12;
  double         lon               = 13;
}
```

- [ ] **Step 2: Add geo fields to NodePeerInfo**

```protobuf
message NodePeerInfo {
  string   peer_id = 1;
  repeated string addrs = 2;
  bool     is_anchor = 3;
  string   country = 4;
  string   city    = 5;
  double   lat     = 6;
  double   lon     = 7;
}
```

- [ ] **Step 3: Regenerate protobuf Go bindings**

Run the proto generation tool (check Makefile or `buf generate`):

```bash
# Check how protos are generated in this project
grep -r "protoc\|buf generate\|protoc-gen" Makefile* buf.* 2>/dev/null || echo "Check proto generation method"
```

If using `buf`:
```bash
buf generate
```

If using `protoc`:
```bash
protoc --go_out=. --go-grpc_out=. rpc/proto/membuss.proto
```

- [ ] **Step 4: Verify compilation**

```bash
go build ./...
```

Expected: compiles cleanly with new fields.

- [ ] **Step 5: Commit**

```bash
git add rpc/proto/membuss.proto rpc/proto/membuss.pb.go rpc/proto/membuss_grpc.pb.go
git commit -m "feat(geo): add country/city/lat/lon fields to PeerInfo and NodePeerInfo protos"
```

---

## Task 2: Self-Resolution Package

**Files:**
- Create: `net/geo/selfresolve.go`
- Create: `net/geo/selfresolve_test.go`

- [ ] **Step 1: Write the failing test**

Create `net/geo/selfresolve_test.go`:

```go
package geo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveSelf_Success(t *testing.T) {
	// Mock ip-api.com response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"country": "Germany",
			"city":    "Berlin",
			"lat":     52.52,
			"lon":     13.405,
		})
	}))
	defer server.Close()

	// Reset once for test
	once = sync.Once{}
	Self = nil

	// Override the URL for testing
	origURL := apiURL
	apiURL = server.URL
	defer func() { apiURL = origURL }()

	result := ResolveSelf()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Country != "Germany" {
		t.Errorf("country = %q, want %q", result.Country, "Germany")
	}
	if result.City != "Berlin" {
		t.Errorf("city = %q, want %q", result.City, "Berlin")
	}
	if result.Lat != 52.52 {
		t.Errorf("lat = %f, want %f", result.Lat, 52.52)
	}
	if result.Lon != 13.405 {
		t.Errorf("lon = %f, want %f", result.Lon, 13.405)
	}
}

func TestResolveSelf_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	once = sync.Once{}
	Self = nil

	origURL := apiURL
	apiURL = server.URL
	defer func() { apiURL = origURL }()

	result := ResolveSelf()
	if result != nil {
		t.Errorf("expected nil result on HTTP error, got %+v", result)
	}
}

func TestResolveSelf_StatusFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "fail",
			"message": "reserved range",
		})
	}))
	defer server.Close()

	once = sync.Once{}
	Self = nil

	origURL := apiURL
	apiURL = server.URL
	defer func() { apiURL = origURL }()

	result := ResolveSelf()
	if result != nil {
		t.Errorf("expected nil result on status fail, got %+v", result)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./net/geo/ -v -run TestResolveSelf
```

Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write implementation**

Create `net/geo/selfresolve.go`:

```go
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
	Self   *Coords
	once   sync.Once
	apiURL = "http://ip-api.com/json"
)

// ResolveSelf calls ip-api.com to resolve the node's own public IP
// to geographic coordinates. Safe to call multiple times — only
// executes once. Returns nil on any failure (graceful degradation).
func ResolveSelf() *Coords {
	once.Do(func() {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(apiURL)
		if err != nil {
			slog.Warn("geo: self-resolve failed", "err", err)
			return
		}
		defer resp.Body.Close()

		var result struct {
			Status  string  `json:"status"`
			Country string  `json:"country"`
			City    string  `json:"city"`
			Lat     float64 `json:"lat"`
			Lon     float64 `json:"lon"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			slog.Warn("geo: self-resolve decode failed", "err", err)
			return
		}
		if result.Status != "success" {
			slog.Warn("geo: self-resolve api status", "status", result.Status)
			return
		}
		Self = &Coords{
			Country: result.Country,
			City:    result.City,
			Lat:     result.Lat,
			Lon:     result.Lon,
		}
		slog.Info("geo: self-resolved", "country", Self.Country, "city", Self.City)
	})
	return Self
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./net/geo/ -v -run TestResolveSelf
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add net/geo/selfresolve.go net/geo/selfresolve_test.go
git commit -m "feat(geo): add self-resolution via ip-api.com"
```

---

## Task 3: Wire Geo into PEX Gossip

**Files:**
- Modify: `net/pex/pex.go:994-1002` (buildSelfPeerInfo)

- [ ] **Step 1: Add import**

In `net/pex/pex.go`, add import:

```go
"github.com/nnlgsakib/membuss/net/geo"
```

- [ ] **Step 2: Include geo in buildSelfPeerInfo**

In `net/pex/pex.go`, at line 994 after `info := &membusspb.PeerInfo{`, add geo fields to the struct literal:

```go
	info := &membusspb.PeerInfo{
		PeerId:       p.host.ID().String(),
		Addrs:        finalAddrs,
		RelayAddrs:   relayAddrs,
		LastSeen:     p.now().Unix(),
		Reachability: reach,
		Seq:          p.now().Unix(),
		PubKey:       pubKeyBytes,
	}

	// Include self-resolved geolocation when available.
	if g := geo.Self; g != nil {
		info.Country = g.Country
		info.City = g.City
		info.Lat = g.Lat
		info.Lon = g.Lon
	}
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./net/pex/
```

Expected: compiles cleanly.

- [ ] **Step 4: Commit**

```bash
git add net/pex/pex.go
git commit -m "feat(geo): include self-resolved coords in PEX gossip"
```

---

## Task 4: Wire into Main Startup

**Files:**
- Modify: `cmd/membuss/main.go:112-120` (remove ensureGeoIPDatabase)
- Modify: `cmd/membuss/main.go:471-475` (remove GeoResolver creation)
- Modify: `cmd/membuss/main.go:786` (startGateway signature)
- Modify: `cmd/membuss/main.go:926-936` (buildExplorer)
- Modify: `cmd/membuss/main.go:613-615` (shutdown geo.Close)
- Modify: `cmd/membuss/main.go:961-1073` (delete ensureGeoIPDatabase)

- [ ] **Step 1: Add geo import and ResolveSelf call**

In `cmd/membuss/main.go`, add import:

```go
"github.com/nnlgsakib/membuss/net/geo"
```

Replace lines 112-120 (the ensureGeoIPDatabase block) with:

```go
	// Resolve own public IP for geolocation (best-effort, 5s timeout).
	geo.ResolveSelf()
```

- [ ] **Step 2: Remove GeoResolver creation at line 471-474**

Delete:

```go
	var geo *explorerPkg.GeoResolver
	if cfg.EnableGeolocation && geoDB != "" {
		geo = explorerPkg.NewGeoResolver(geoDB)
	}
```

- [ ] **Step 3: Update startGateway call at line 475**

Remove the `geo` parameter:

```go
	gateSrv, err := startGateway(cfg.GatewayAddr, newMemgateAdapter(backend), newExplorerAdapter(backend, cfg.AnchorMode, kr, memnsRes), cfg.GatewayRateLimitPerMin, cfg.GatewayTLS, memnsRes, cfg.DataDir, cfg.LogLevel)
```

- [ ] **Step 4: Update startGateway signature at line 786**

Remove the `geo` parameter:

```go
func startGateway(addr string, b memgate.Backend, exp *explorerAdapter, rateLimitPerMin int, tlsCfg config.TLSConfig, memnsRes *memns.Resolver, dataDir string, logLevel string) (*httpServer, error) {
```

And update line 790 inside the function:

```go
		ExplorerHandler: buildExplorer(exp),
```

- [ ] **Step 5: Update buildExplorer at line 926**

Remove `geo` parameter:

```go
func buildExplorer(exp *explorerAdapter) http.Handler {
	if exp == nil {
		return nil
	}
	h, err := explorerPkg.New(explorerPkg.Config{Backend: exp})
	if err != nil {
		slog.Warn("explorer", "err", err.Error())
		return nil
	}
	return h.Handler()
}
```

- [ ] **Step 6: Remove geo.Close() at line 613-615**

Delete:

```go
	if geo != nil {
		geo.Close()
	}
```

- [ ] **Step 7: Delete ensureGeoIPDatabase function (lines 961-1073)**

Delete the entire `ensureGeoIPDatabase` function.

- [ ] **Step 8: Remove unused imports**

Remove any now-unused imports (`explorerPkg` alias if no longer referenced elsewhere, `os`, `filepath`, `io`, etc.). Check with:

```bash
go build ./cmd/membuss/
```

Fix any import errors.

- [ ] **Step 9: Verify compilation**

```bash
go build ./...
```

Expected: compiles cleanly.

- [ ] **Step 10: Commit**

```bash
git add cmd/membuss/main.go
git commit -m "feat(geo): wire self-resolve into startup, remove MMDB loading"
```

---

## Task 5: Map Geo Fields Through Backend

**Files:**
- Modify: `cmd/membuss/backend.go:443-466` (Peers method)
- Modify: `cmd/membuss/explorer_backend.go:290-305` (explorerAdapter.Peers)

- [ ] **Step 1: Add geo fields to daemonBackend.Peers**

In `cmd/membuss/backend.go`, update the `Peers()` method (line 443-466) to include geo fields:

```go
func (b *daemonBackend) Peers(limit uint32) ([]serverpkg.NodePeerInfo, uint32, error) {
	if b.pex == nil {
		return nil, 0, nil
	}
	anchors := b.getKnownAnchors(context.Background())
	infos := b.pex.Peers()
	out := make([]serverpkg.NodePeerInfo, 0, len(infos))
	for _, p := range infos {
		addrs := make([]string, 0, len(p.Addrs))
		for _, a := range p.Addrs {
			addrs = append(addrs, a)
		}
		_, isAnchor := anchors[p.PeerId]
		out = append(out, serverpkg.NodePeerInfo{
			PeerID:   p.PeerId,
			Addrs:    addrs,
			IsAnchor: isAnchor,
			Country:  p.Country,
			City:     p.City,
			Lat:      p.Lat,
			Lon:      p.Lon,
		})
	}
	if limit > 0 && uint32(len(out)) > limit {
		out = out[:limit]
	}
	return out, uint32(len(infos)), nil
}
```

- [ ] **Step 2: Update server.NodePeerInfo struct**

In `rpc/server/server.go`, add geo fields to the struct (line 108-113):

```go
type NodePeerInfo struct {
	PeerID   string
	Addrs    []string
	IsAnchor bool
	Country  string
	City     string
	Lat      float64
	Lon      float64
}
```

- [ ] **Step 3: Update peerInfoToProto conversion**

In `rpc/server/server.go`, update `peerInfoToProto` (around line 389-395) to include geo fields:

```go
func peerInfoToProto(p NodePeerInfo) *membusspb.NodePeerInfo {
	return &membusspb.NodePeerInfo{
		PeerId:   p.PeerID,
		Addrs:    append([]string(nil), p.Addrs...),
		IsAnchor: p.IsAnchor,
		Country:  p.Country,
		City:     p.City,
		Lat:      p.Lat,
		Lon:      p.Lon,
	}
}
```

- [ ] **Step 4: Update explorerAdapter.Peers**

In `cmd/membuss/explorer_backend.go`, update the `Peers()` method (line 290-305) to map geo fields:

```go
func (a *explorerAdapter) Peers(ctx context.Context, limit int) ([]explorer.PeerInfo, error) {
	infos, _, err := a.b.Peers(uint32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]explorer.PeerInfo, 0, len(infos))
	for _, p := range infos {
		out = append(out, explorer.PeerInfo{
			PeerID:    p.PeerID,
			Addrs:     p.Addrs,
			IsAnchor:  p.IsAnchor,
			Connected: false,
			Country:   p.Country,
			City:      p.City,
			Lat:       p.Lat,
			Lon:       p.Lon,
		})
	}
	return out, nil
}
```

- [ ] **Step 5: Verify compilation**

```bash
go build ./...
```

Expected: compiles cleanly.

- [ ] **Step 6: Commit**

```bash
git add cmd/membuss/backend.go cmd/membuss/explorer_backend.go rpc/server/server.go
git commit -m "feat(geo): propagate geo fields through backend and explorer adapter"
```

---

## Task 6: Remove Server-Side GeoResolver

**Files:**
- Modify: `gateway/explorer/explorer.go:334-336,344,379` (Config, Explorer struct, New)
- Modify: `gateway/explorer/explorer.go:985-1005` (handlePeers)
- Delete: `gateway/explorer/geo.go`

- [ ] **Step 1: Remove GeoResolver from Config and Explorer**

In `gateway/explorer/explorer.go`, remove from Config (line 334-336):

```go
// DELETE these lines:
	// GeoResolver performs IP geolocation. May be nil
	// when geolocation is disabled.
	GeoResolver *GeoResolver
```

Remove from Explorer struct (line 344):

```go
// DELETE this line:
	geo  *GeoResolver
```

Remove from New() return (line 379):

```go
// Change from:
	return &Explorer{cfg: cfg, tpl: tpl, pages: pages, geo: cfg.GeoResolver}, nil
// To:
	return &Explorer{cfg: cfg, tpl: tpl, pages: pages}, nil
```

- [ ] **Step 2: Simplify handlePeers**

In `gateway/explorer/explorer.go`, replace lines 985-1005 with:

```go
func (e *Explorer) handlePeers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peers, _ := e.cfg.Backend.Peers(ctx, e.cfg.PeerLimit)
	e.render(w, r, "peers.html", peersData{
		Title:     "Peers",
		PeerCount: len(peers),
		Peers:     peers,
	})
}
```

- [ ] **Step 3: Delete gateway/explorer/geo.go**

```bash
rm gateway/explorer/geo.go
```

- [ ] **Step 4: Remove firstPublicIP if unused**

Check if `firstPublicIP` (line 1488-1510) is used anywhere else in the file. If only used by the removed geo block, delete it too.

```bash
grep -n "firstPublicIP" gateway/explorer/explorer.go
```

If only the definition remains (no callers), delete lines 1488-1510.

- [ ] **Step 5: Verify compilation**

```bash
go build ./gateway/explorer/
```

Expected: compiles cleanly.

- [ ] **Step 6: Commit**

```bash
git add gateway/explorer/explorer.go gateway/explorer/geo.go
git commit -m "feat(geo): remove server-side GeoResolver and MMDB lookup"
```

---

## Task 7: Clean Up Config

**Files:**
- Modify: `config/config.go:160-170`
- Modify: `config/write.go:186-195`
- Modify: `cmd/membuss-entrypoint/main.go:128`
- Modify: `desktop/daemon.go:705-707`
- Modify: `desktop/app.go:164-168`

- [ ] **Step 1: Remove geolocation config fields**

In `config/config.go`, delete lines 160-170:

```go
// DELETE these lines:
	// --- Phase: geolocation ---

	// EnableGeolocation enables server-side IP geolocation
	// for peer addresses. When true, the explorer enriches
	// each peer with approximate Country, City, Lat, Lon
	// using a local MaxMind GeoLite2-City database.
	EnableGeolocation bool `yaml:"enable_geolocation"`
	// GeolocationDB is an optional path to a custom
	// GeoLite2-City.mmdb file. When empty the resolver
	// looks for GeoLite2-City.mmdb in DataDir.
	GeolocationDB string `yaml:"geolocation_db"`
```

- [ ] **Step 2: Remove geolocation template section**

In `config/write.go`, delete lines 186-195:

```yaml
# DELETE these lines:
# -----------------------------------------------------------------------------
# Geolocation
# -----------------------------------------------------------------------------

# Enable server-side IP geolocation for peer addresses.
enable_geolocation: <<ENABLE_GEOLOCATION>>

# Optional path to a custom GeoLite2-City.mmdb database file.
# If empty, the database is downloaded automatically to the data directory.
geolocation_db: "<<GEOLOCATION_DB>>"
```

- [ ] **Step 3: Remove Docker env var**

In `cmd/membuss-entrypoint/main.go`, delete line 128:

```go
// DELETE this line:
		"enable_geolocation": os.Getenv("MEMBUSS_ENABLE_GEOLOCATION"),
```

- [ ] **Step 4: Remove desktop path normalization**

In `desktop/daemon.go`, delete lines 705-707:

```go
// DELETE these lines:
	if geo, ok := cfg["geolocation_db"].(string); ok {
		cfg["geolocation_db"] = filepath.ToSlash(geo)
	}
```

- [ ] **Step 5: Remove desktop app normalization**

In `desktop/app.go`, remove `"geolocation_db:"` from the condition at line 164:

```go
// Change from:
		if strings.HasPrefix(trimmed, "geolocation_db:") ||
			strings.HasPrefix(trimmed, "data_dir:") ||
// To:
		if strings.HasPrefix(trimmed, "data_dir:") ||
```

- [ ] **Step 6: Verify compilation**

```bash
go build ./...
```

Expected: compiles cleanly.

- [ ] **Step 7: Commit**

```bash
git add config/config.go config/write.go cmd/membuss-entrypoint/main.go desktop/daemon.go desktop/app.go
git commit -m "feat(geo): remove geolocation config fields and template sections"
```

---

## Task 8: Remove maxminddb Dependency

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Tidy go modules**

```bash
go mod tidy
```

This removes `github.com/oschwald/maxminddb-golang` from go.mod and go.sum since it's no longer imported.

- [ ] **Step 2: Verify compilation**

```bash
go build ./...
```

Expected: compiles cleanly without maxminddb.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: remove maxminddb-golang dependency"
```

---

## Task 9: Frontend — Swap Leaflet for D3

**Files:**
- Modify: `explorer-web/package.json`
- Modify: `explorer-web/src/routes/peers/+page.svelte`
- Create: `explorer-web/static/world-110m.json`

- [ ] **Step 1: Update package.json**

In `explorer-web/package.json`, remove leaflet dependencies and add d3:

```json
// REMOVE from dependencies or devDependencies:
    "@types/leaflet": "^1.9.21",
    "leaflet": "^1.9.4",

// ADD to dependencies:
    "d3": "^7.9.0",
    "@types/d3": "^7.4.3",
```

- [ ] **Step 2: Install dependencies**

```bash
cd explorer-web && npm install
```

- [ ] **Step 3: Download world TopoJSON**

```bash
curl -o explorer-web/static/world-110m.json "https://cdn.jsdelivr.net/npm/world-atlas@2/countries-110m.json"
```

- [ ] **Step 4: Rewrite the map section in +page.svelte**

Replace the Leaflet map code in `explorer-web/src/routes/peers/+page.svelte`. The key changes:

1. Remove Leaflet imports and initialization
2. Add D3 imports
3. Replace the map rendering logic with D3 SVG
4. Keep the existing table and polling logic

The new map section should:

```typescript
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import * as d3 from 'd3';
  import { apiFetch } from '$lib/api';

  interface PeerInfo {
    PeerID: string;
    Addrs: string[];
    IsAnchor: boolean;
    Connected: boolean;
    Country: string;
    City: string;
    Lat: number;
    Lon: number;
  }

  let mapEl: HTMLDivElement;
  let peers: PeerInfo[] = [];
  let displayPeers: any[] = [];
  let filterText = '';
  let svg: d3.Selection<SVGSVGElement, unknown, null, undefined>;
  let projection: d3.GeoProjection;
  let zoom: d3.ZoomBehavior<SVGSVGElement, unknown>;
  let pollInterval: ReturnType<typeof setInterval>;

  const worldUrl = '/world-110m.json';

  onMount(async () => {
    await loadPeers();
    await initMap();
    pollInterval = setInterval(loadPeers, 10000);
  });

  onDestroy(() => {
    if (pollInterval) clearInterval(pollInterval);
  });

  async function loadPeers() {
    try {
      const res = await apiFetch('/peers');
      const data = await res.json();
      peers = data.Peers || [];
      displayPeers = peers.map((p) => ({
        ...p,
        location: [p.City, p.Country].filter(Boolean).join(', ') || 'Unknown',
      }));
      updateMarkers();
    } catch (e) {
      console.error('Failed to load peers:', e);
    }
  }

  async function initMap() {
    const width = mapEl.clientWidth;
    const height = 500;

    projection = d3.geoNaturalEarth1()
      .fitSize([width, height], { type: 'Sphere' });

    const path = d3.geoPath().projection(projection);

    svg = d3.select(mapEl)
      .append('svg')
      .attr('width', width)
      .attr('height', height)
      .attr('viewBox', `0 0 ${width} ${height}`);

    // Background sphere (ocean)
    svg.append('path')
      .datum({ type: 'Sphere' })
      .attr('d', path)
      .attr('fill', '#0a0a1a')
      .attr('stroke', '#1a1a3e')
      .attr('stroke-width', 0.5);

    // Load and render countries
    const world = await d3.json(worldUrl);
    if (!world) return;

    const countries = topojsonFeature(world, world.objects.countries);

    svg.append('g')
      .selectAll('path')
      .data(countries.features)
      .join('path')
      .attr('d', path)
      .attr('fill', '#1a1a2e')
      .attr('stroke', '#16213e')
      .attr('stroke-width', 0.3);

    // Peer marker group
    svg.append('g').attr('class', 'peers');

    // Zoom behavior
    zoom = d3.zoom<SVGSVGElement, unknown>()
      .scaleExtent([1, 8])
      .on('zoom', (event) => {
        svg.select('g.peers').attr('transform', event.transform);
        svg.selectAll('path').attr('transform', event.transform);
      });

    svg.call(zoom);

    updateMarkers();
  }

  function topojsonFeature(topology: any, object: any) {
    // Simple topojson to geojson conversion for features
    const arcs = topology.arcs;
    const transform = topology.transform;
    const scale = transform.scale;
    const translate = transform.translate;

    function decodeArc(arc: number[]) {
      const points: [number, number][] = [];
      let x = 0, y = 0;
      for (const delta of arc) {
        x += delta[0];
        y += delta[1];
        points.push([
          x * scale[0] + translate[0],
          y * scale[1] + translate[1]
        ]);
      }
      return points;
    }

    function decodeRing(arcIds: number[]) {
      const coords: [number, number][] = [];
      for (const id of arcIds) {
        const arc = arcs[id < 0 ? ~id : id];
        const points = decodeArc(arc);
        if (id < 0) points.reverse();
        coords.push(...points);
      }
      coords.push(coords[0]); // close ring
      return coords;
    }

    return {
      type: 'FeatureCollection',
      features: object.geometries.map((geom: any) => {
        let coords;
        if (geom.type === 'Polygon') {
          coords = geom.arcs.map(decodeRing);
        } else if (geom.type === 'MultiPolygon') {
          coords = geom.arcs.map((polygon: any) => polygon.map(decodeRing));
        } else {
          return null;
        }
        return {
          type: 'Feature',
          properties: geom.properties || {},
          geometry: { type: geom.type, coordinates: coords }
        };
      }).filter(Boolean)
    };
  }

  function updateMarkers() {
    if (!svg) return;

    const peerData = displayPeers.filter(p => p.Lat !== 0 || p.Lon !== 0);

    const dots = svg.select('g.peers')
      .selectAll<SVGCircleElement, any>('circle')
      .data(peerData, (d: any) => d.PeerID);

    dots.exit().remove();

    const enter = dots.enter()
      .append('circle')
      .attr('r', 4)
      .attr('cursor', 'pointer');

    enter.append('title');

    const merged = enter.merge(dots)
      .attr('cx', (d: any) => projection([d.Lon, d.Lat])?.[0] ?? 0)
      .attr('cy', (d: any) => projection([d.Lon, d.Lat])?.[1] ?? 0)
      .attr('fill', (d: any) => d.IsAnchor ? '#3b82f6' : '#22c55e')
      .attr('stroke', (d: any) => d.IsAnchor ? '#60a5fa' : '#4ade80')
      .attr('stroke-width', 1.5)
      .attr('opacity', 0.85);

    merged.select('title')
      .text((d: any) => `${d.PeerID}\n${d.location}\n${d.IsAnchor ? 'Anchor' : 'Peer'}`);

    merged.on('click', (event: MouseEvent, d: any) => {
      navigator.clipboard.writeText(d.PeerID);
    });
  }
</script>

<div class="peers-page">
  <h1>Active Swarm Map</h1>
  <p class="subtitle">{displayPeers.length} peers connected</p>

  <div class="map-container" bind:this={mapEl}></div>

  <div class="controls">
    <input
      type="text"
      bind:value={filterText}
      placeholder="Filter by country or ID..."
      class="filter-input"
    />
  </div>

  <table>
    <thead>
      <tr>
        <th>Location</th>
        <th>Peer ID</th>
        <th>Transport</th>
        <th>Anchor</th>
      </tr>
    </thead>
    <tbody>
      {#each displayPeers.filter(p =>
        filterText === '' ||
        p.location.toLowerCase().includes(filterText.toLowerCase()) ||
        p.PeerID.toLowerCase().includes(filterText.toLowerCase())
      ) as peer}
        <tr>
          <td>{peer.location}</td>
          <td class="mono">{peer.PeerID.slice(0, 16)}...</td>
          <td>{peer.Addrs?.length > 0 ? 'p2p' : 'unknown'}</td>
          <td>{peer.IsAnchor ? 'Yes' : 'No'}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>

<style>
  .peers-page { padding: 1rem; }
  h1 { margin-bottom: 0.25rem; }
  .subtitle { color: #888; margin-bottom: 1rem; }
  .map-container {
    width: 100%;
    height: 500px;
    background: #0a0a1a;
    border-radius: 8px;
    overflow: hidden;
    margin-bottom: 1rem;
  }
  .controls { margin-bottom: 1rem; }
  .filter-input {
    width: 100%;
    max-width: 400px;
    padding: 0.5rem;
    border: 1px solid #333;
    border-radius: 4px;
    background: #111;
    color: #eee;
  }
  table { width: 100%; border-collapse: collapse; }
  th, td { padding: 0.5rem; text-align: left; border-bottom: 1px solid #222; }
  th { color: #888; font-size: 0.85rem; }
  .mono { font-family: monospace; font-size: 0.85rem; }
</style>
```

- [ ] **Step 5: Verify frontend builds**

```bash
cd explorer-web && npm run build
```

Expected: builds cleanly.

- [ ] **Step 6: Commit**

```bash
git add explorer-web/
git commit -m "feat(geo): replace Leaflet with D3 SVG worldmap for peer display"
```

---

## Task 10: End-to-End Verification

- [ ] **Step 1: Full Go build**

```bash
go build ./...
```

Expected: compiles cleanly.

- [ ] **Step 2: Run all tests**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 3: Run the daemon and verify**

```bash
go run ./cmd/membuss
```

Check logs for:
- `geo: self-resolved country=... city=...` (or `geo: self-resolve failed` if offline)
- No MMDB download messages
- No `geo: loaded` messages
- Gateway starts normally

- [ ] **Step 4: Open explorer peers page**

Navigate to `http://localhost:<gateway_port>/explorer/peers`

Verify:
- D3 SVG map renders with country outlines
- Peer markers appear as green/blue dots
- Hover shows tooltip with peer details
- Click copies peer ID
- Table shows location column
- No Leaflet errors in console

- [ ] **Step 5: Final commit (if any fixups needed)**

```bash
git add -A
git commit -m "fix(geo): end-to-end verification fixes"
```
