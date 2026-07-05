# 3D Interactive Globe for Swarm Map

**Date**: 2026-07-05
**Status**: Approved
**Scope**: Replace D3 SVG flat map with interactive 3D globe using three.js + three-globe

## Goal

Replace the current 2D D3 SVG world map on the `/peers` page with an interactive 3D globe that:
- Is fully interactive (drag to rotate, scroll to zoom, like Google Earth)
- Shows all peers as glowing nodes on the globe surface
- Renders animated arc connections between all peers showing data packet flow
- Auto-rotates when idle

## Library Choice

**three.js + three-globe** (~80KB gzipped)

Rationale:
- `cobe` (~5KB): Too limited — no particle flow animation on arcs
- `globe.gl` (~40KB): Wrapper around three-globe, adds unnecessary abstraction
- `three.js + three-globe`: Full control, built-in arc particle animation, atmospheric glow, most customizable

## Design

### Globe Component

- **Container**: `<div bind:this={globeEl}>` fills the same 400px height card
- **Globe setup**: `ThreeGlobe()` instance with:
  - Globe material: dark navy (`#0a0a1a`)
  - Atmospheric glow: semi-transparent emerald mesh slightly larger than globe
  - Auto-rotation: 0.5 degrees/frame, resumes after 3s idle
  - Perspective camera positioned so globe fills container
- **Interaction** (`OrbitControls`):
  - Left-drag: rotate globe (free orbit)
  - Scroll: zoom in/out with damping for smooth feel
  - Right-drag: pan
  - Auto-rotation pauses on user interaction
- **Resize handling**: `ResizeObserver` on container div updates renderer size

### Peer Nodes (pointsData)

- Each peer becomes a glowing point on the globe at `[lat, lon]`
- Anchor nodes: blue glow (`#3b82f6`)
- Regular nodes: emerald glow (`#22c55e`)
- `pointAltitude`: 0.02 (dots float slightly above surface)
- `pointRadius`: 0.3-0.5 degrees
- On hover: tooltip with peer ID, location, transport

### Arc Connections (arcsData)

- Full mesh: every peer connects to every other peer
- Each arc represents a P2P gossip channel
- Arc color: emerald gradient (`#22c55e` to transparent)
- `arcDashLength`: 0.4
- `arcDashGap`: 0.2
- `arcDashAnimateTime`: 2000ms — creates "data packet flowing" effect
- Arc altitude scales with distance (farther = higher arc)
- `arcStroke`: 1.5px

### Rendering

- `WebGLRenderer` with `antialias: true`, `alpha: true`
- Procedural star background for immersion
- `requestAnimationFrame` loop for 60fps
- `OrbitControls.enableDamping = true` for smooth rotation

### Visual Polish

- Atmospheric glow via semi-transparent emerald globe mesh
- Peer dots emit subtle glow via emissive material
- Dark background (`#0a0a1a`) blends with card border
- Stars: random points on large sphere

## Integration

### Files Modified
- `explorer-web/src/routes/peers/+page.svelte` — replace D3 SVG map with three-globe

### Files Removed
- D3 SVG map code (projection, pathGen, SVG creation, zoom behavior)
- `d3` and `@types/d3` dependencies from package.json
- `countries.json` from static/ (globe uses its own geometry)

### Dependencies Added
- `three` (~150KB gzipped)
- `three-globe` (~20KB gzipped)
- `@types/three` (dev dependency)

### Unchanged
- Card container, peer counter overlay, peers table
- Auto-refresh every 10s (reactive pointsData/arcsData)
- Connect to peer input
- Search filter
- API fetch logic

## Performance

- `three-globe` handles instanced rendering internally — efficient for 100+ nodes
- Arc dash animation runs on GPU via shader — no per-frame JS overhead
- Lazy init: globe only initializes on mount (SSR-safe via `onMount`)
- Stars rendered once as static geometry

## Testing

- Visual: restart daemon, verify globe renders at `/peers`
- Interaction: drag to rotate, scroll to zoom, verify smooth
- Arcs: verify animated dashed arcs between all peers
- Refresh: verify peer data updates every 10s without full re-render
- Responsive: verify globe resizes with container
