# MemPlace — Erasure Shard Placement Design

**Status:** Draft for review · **Branch:** `feature/shard-placement` · **Date:** 2026-08-26

Erasure shards get placed on ring-responsible peers so no node stores both raw
leaves and the shard set; fetchers gather any k-of-n shards from k different
peers and reconstruct. Erasure coding finally *replaces* duplication instead of
stacking on top of it.

---

## 1. Problem

Measured today (`reff.mp4`, 6.34 MiB → 15.86 MiB stored, RS 8+3):

| Layer | Size | Source |
|---|---|---|
| Raw leaf chunks | 6.34 MiB | `core/memfs/builder.go:234` |
| All 11 shards (8 data + 3 parity) | 8.72 MiB | `core/memfs/builder.go:194` |
| DAG nodes + manifests + overhead | ~0.8 MiB | — |

Original content stored **twice**: raw leaves for serving, data shards as a
second copy of identical bytes. Parity is the only part adding new information.
Worse, the redundancy is temporary (see audit B4).

## 2. Goals

1. G1 — Origin stores raw leaves only; shards live on other peers.
2. G2 — Fetchers transfer exactly k shards (= original size) from ≥k distinct peers when raw is unavailable; stop-at-k, never all n.
3. G3 — Redundancy survives GC and peer churn via repair/re-replication.
4. G4 — Mixed-version swarms keep working (old peers unaffected).
5. G5 — Cold tier: origin can drop raw after age threshold; file survives as pure k-of-n across swarm.
6. G6 — No new storage-fill attack surface (gated acceptance of pushed shards).

## 3. Non-goals

- Changing MID derivation or content addressing of shards.
- Payment/incentive layers.
- Rewriting memex flow control or DHT internals beyond policy gates.

## 4. Current-state audit (verified)

Every item below was confirmed against source on this branch. These are the
holes MemPlace must close or exploit.

### Bugs / gaps that block placement

| # | Finding | Location |
|---|---|---|
| B1 | Dual storage: raw leaves AND full shard set persisted at ingest | `core/memfs/builder.go:234,194` |
| B2 | DHT `Provide`/`FindProviders` silently no-op for `CodecRaw` MIDs — shard MIDs are **undiscoverable** via DHT by design | `net/dht/dht.go:227-229,246-248` |
| B3 | Erasure manifests never cross the wire — remote nodes have no manifest, so the EC reconstruction path in `fetchingBlockstore` is dead off-origin (it reads *local* meta) | `cmd/membuss/daemon/fetching_blockstore.go:46`; manifests written only at ingest (`builder.go:199`, `backend.go:169-177`) |
| B4 | Shards are unreachable from sealed roots → **first auto-GC sweep deletes every shard** (~24 h interval + 24 h min-age default). Redundancy silently evaporates ~48 h after ingest | `core/store/seal.go:230` scans only PrefixBlock/DAG; Walk never sees `manifest.ShardMids`; defaults `config/config.go:346-347` |
| B5 | RepairWorker audits sealed ROOTS, but real ShardMid manifests attach to LEAF MIDs; root manifest has k/n but empty `ShardMids` → count-mismatch error → false "unrecoverable" spam per cycle | `core/erasure/repair.go:174-183` vs `backend.go:169-177` vs `builder.go:199` |
| B6 | Reconstruction path fetches ALL n shards before checking `presentCount >= k` | `fetching_blockstore.go:56-71` |
| B7 | `GetManifest` swallows proto-unmarshal errors returning nil,nil | `core/erasure/manifest_store.go:52-55` |
| B8 | `DeleteRecursive` ignores manifests — orphaned shards left behind | `core/store/seal.go:310-460` |
| B9 | Verifier accepts ANY hash-valid unsolicited Blocks frame → storage-fill vector once deliberate pushes exist | `net/memex_v2/memex.go:290-306` |
| B10 | Ring membership mutates on every connect/disconnect with no hysteresis — flapping peer repartitions placement network-wide; persisted list makes departed peers permanent until reboot | `cmd/membuss/daemon/main.go:461-477` |

### Existing assets to reuse

- **HRW hashing, not a ring** (better): `Assign(m, replicas)` top-k ordered,
  deterministic tie-break, failure-domain aware, `IsOwner`,
  `ComputeMigration(newRing, localPeerID, mids)` already exists and is unused —
  built for exactly this rebalance job (`core/shard/hashring.go:108-224`).
- **RepairMID/RepairWorker**: local shard reconstruction loop is sound;
  wrong enumeration target only (`repair.go:26-106`).
- **Unsolicited push acceptance**: verifier pipeline + dbBatchWriter means a
  targeted `PushBlocksTo` needs no receiver-side protocol change for the happy
  path (only policy gating, B9).
- **PEX signed peer records**: candidate deterministic membership source
  (`net/pex/pex.go`, cap 256, gossip rounds).
- **Anchor mode**: already pulls every sealed root; becomes the reference
  implementation consumer of k-of-n gather.
- `midToCID` maps every MID to CIDv1 regardless of codec (`dht.go:627`) — the
  CodecRaw gate at :227 is pure policy, trivially bypassable for a dedicated
  `ProvideShard` method without keyspace collision risk.

## 5. Architecture

```
                    ┌────────────────────────────────────────────┐
                    │                ORIGIN NODE                 │
   add file ──► chunk ► DAG ► store RAW leaves ──► serve reads  │
                    │        │                                   │
                    │        └► erasure encode (k+n shards)      │
                    │             │                               │
                    └─────────────┼───────────────────────────────┘
                                  │ PushBlocksTo(peer, shard)
                                  ▼  per HRW Assign(shardKey(root,i), R)
        ┌──────────────┐   ┌──────────────┐   ┌──────────────┐
        │ PEER A       │   │ PEER B       │   │ PEER C       │
        │ shards 2,7   │   │ shards 0,5,9 │   │ shards 1,4   │
        │ announces    │   │ announces    │   │ announces    │
        │ ShardSetRec  │   │ ShardSetRec  │   │ ShardSetRec  │
        └──────▲───────┘   └──────▲───────┘   └──────▲───────┘
               │   any k of n shards      DHT value records│
               └───────────┴──────────── FETCHER ──────────┘
                  gather stop-at-k ► reconstruct ► cache raw
```

Placement key = `rootMID || uint32be(shardIndex)` hashed through the existing
HRW score function. Assigning per-shard (not per-file) spreads each file's n
shards across up to n distinct owner sets; R (default 3) owners per shard give
durability headroom above k.

## 6. Component design

### 6.1 Ring membership stability (fixes B10)

Problem: membership derived from live connection events makes placement a
node-local opinion that flaps.

Design:
- Membership source becomes the **PEX table snapshot** (signed, gossiped,
  near-consistent across nodes) instead of raw TCP connections.
- Hysteresis: peer enters ring after being present in PEX for ≥
  `placement.ring_grace_enter` (default 10 min); leaves after absent ≥
  `placement.ring_grace_exit` (default 30 min). Connect/disconnect events no
  longer mutate the ring directly.
- Persisted baseline stays (`shard:ring:peers`) as cold-start seed.
- Documented divergence window: two nodes may briefly disagree during grace
  periods; harmless because ownership gates *writes* (pushes) while discovery
  (DHT records) remains authoritative for reads.

### 6.2 Manifest distribution (fixes B3)

Two channels, one format:

1. **On the wire**: extend memex header/ObjectInfo frame with an optional
   `erasure_manifest` field (proto addition, backward compatible — old peers
   ignore unknown fields). Sent by origin alongside root ObjectInfo when a
   manifest exists. Receiving node persists to `erasure/<mid>` meta like local
   ingest does.
2. **DHT value record**: `ShardSetRecord` under the existing namespaced
   `"membuss"` validator:
   ```
   ShardSetRecord {
     root_mid      string
     manifest      bytes   // membusspb.ErasureManifest
     holder        peer.ID // announcer
     published_at  int64
     sig           bytes   // Ed25519 over canonical bytes, key = publisher
   }
   ```
   Validator: signature check + manifest integrity (ShardMids length ==
   k+n) + freshness (sequence monotonic per root). Mirrors MemNS record
   validation patterns already in `net/dht/validator.go`.

### 6.3 Discovery (fixes B2)

- New `MemDHT.ProvideShard(ctx, root, manifest)` / `FindShardSets(ctx, root)`
  pair: publishes/reads `ShardSetRecord`s under a distinct namespace
  (`"membuss/shardset/1"`), leaving the CodecRaw gate untouched for plain
  blocks.
- Per-shard provider records are NOT used (n× provide cost, 20-provider
  lookup cap would truncate). One record per root carries everything.
- Herald gains `StrategyShardsV2`: iterates local shard index (6.6), announces
  ShardSetRecord for roots this node holds ≥1 assigned shard of, rate-limited
  as today.

### 6.4 Targeted delivery

New engine API:

```go
// net/memex_v2
func (e *Engine) PushBlocksTo(ctx context.Context, pid peer.ID,
    blocks []store.Block) error
```

Wraps `GetOrCreateStream` + `writeFrameLocked` (pattern proven at
`want_exchange.go:67-75`). Fire-and-forget with retry-once; no ack protocol
v1 — reconciliation happens via periodic coverage audit (6.7).

Receiver policy (fixes B9):
- Unsolicited Blocks frames accepted **only if** receiver's ring `IsOwner`
  says sender-or-receiver holds responsibility for that block's placement
  key, OR the block completes a wantlisted entry, OR sender is the DHT-known
  publisher of the root. Everything else: stream reset + ban-score increment
  (connection gater already exists, `net/host/host.go`).
- QuotaManager budget check before persisting pushed shards
  (`anchor/quota.go` pattern).

### 6.5 Ingest-time distribution

`core/memfs/builder.go` change behind config flag `placement.enabled`:
1. Encode chunks as today.
2. Store raw leaves locally (unchanged — serving tier).
3. Do NOT PutBatch shards locally. Instead enqueue `(shardKey(root, idx), shard)`
   onto the placement queue.
4. Placement worker: `ring.Assign(key, R)` → for each of top-R owners ≠ self:
   dial + `PushBlocksTo`. Self-owned shards stay local.
5. Write shard index rows (6.6) for every shard incl. self-held.
6. Root-level manifest gains `ShardMids` (currently omitted,
   `backend.go:169-177`) so root-level reconstruction works.

Small files (< adaptive threshold) may skip distribution entirely — single
chunk, redundancy via raw replication across R peers instead (existing herald
behavior covers announcement; push gives them copies).

### 6.6 Shard index & lifecycle (fixes B4, B8)

New meta keyspace in `core/store`:

```
shardidx/<rootMID>/<idx>  →  shardMID string
```

- Written at ingest/distribution; iterated by RepairWorker (correct target —
  fixes B5), herald V2 strategy, DeleteRecursive (removes owned shards — fixes
  B8), and GC reachability: `Walk` gains a manifest-aware branch that treats
  indexed shard MIDs of sealed roots as reachable **when**
  `placement.keep_local_shards` is true (self-owned shards survive GC; others
  get collected).
- Cold-tier transition job (G5): when root unaccessed > `placement.cold_after`
  (default 30 d) and `placement.enabled`, delete local raw leaves, keep
  self-owned shards, rely on network for serving. Serving path transparently
  falls back to gather+reconstruct (6.8). Config knob
  `erasure.keep_raw=false` forces immediate cold state at ingest (archive
  deployments).

### 6.7 Coverage audit, repair & rebalance

- **Coverage audit** (extends RepairWorker): for each sealed root with
  manifest: count reachable shards = local index ∩ (local store ∪
  FindShardSets holders queried lazily). If < k+1 safety margin: trigger
  re-generation — fetch any k shards, reconstruct missing, re-push to current
  ring owners. This is `ComputeMigration`'s first production caller.
- **Rebalance on membership change**: ring diff (grace-exited peers out, new
  peers in) → gained keys: pull from previous owner (still holds copy during
  exit grace) or reconstruct from network; lost keys: nothing (old owner keeps
  until its own audit evicts).
- Rate limits: reuse herald token-bucket pattern; audit interval default 1 h.

### 6.8 Fetch path (fixes B6; Option A folded in)

`fetchingBlockstore.Get` EC path rewrite:

1. Get manifest (now possible remotely via 6.2).
2. Local shards first (index-aware), then `FindShardSets` holders, then DHT
   providers fallback.
3. Gather concurrently from distinct peers, **stop at k valid shards**
   (InlineValidator per shard, `erasure.go:283-299`).
4. Reconstruct, verify against `manifest.OriginalMid`, Put raw leaf.
5. Received shards: persist ONLY ones this node is ring-owner for; discard
   rest (they were fetched into memory buffers, not the batch writer — requires
   routing gather through session-less direct streams rather than the default
   auto-persist Engine path; see Open Questions Q2).

## 7. Config surface (new `placement:` section)

```yaml
placement:
  enabled: false            # master switch; false = exact current behavior
  replicas: 3               # ring owners per shard (1..64, existing validation)
  ring_grace_enter: 10m
  ring_grace_exit: 30m
  cold_after: 720h          # drop raw after idle; 0 = never
  push_concurrency: 4
  audit_interval: 1h
erasure:
  keep_raw: true            # false = archive mode, raw dropped at ingest
```

All defaults preserve current behavior except where B-fixes are strict wins
(B5/B6/B7 ship unconditionally — they fix broken code paths, not behavior
choices).

## 8. Back-compatibility matrix

| Swarm mix | Behavior |
|---|---|
| All old peers | Unchanged (flag off) |
| Old origin, new fetcher | Fetcher uses manifests if wire-delivered (they aren't, old origin) → degrades to today's behavior; no breakage |
| New origin, old peers | Old peers ignore unknown frame fields; they simply don't participate in placement. Raw serving still works |
| Anchor nodes | Anchor Fetcher consumes gather API; anchor keeps full-copy semantics (by design) but now stores shards dedup'd? No — anchor stays raw-complete, unchanged v1 |

## 9. Failure modes

| Scenario | Handling |
|---|---|
| Owner peer dies | Grace-exit timer → rebalance pulls/reconstructs replacement; until then durability = remaining owners + k-of-n math (need ≤ n−k losses before unrecoverable) |
| Churn storm (mass disconnect) | Grace windows damp ring mutation; audit backs off exponentially |
| Split ring views | Writes gated by local opinion may misroute → receiver rejects non-owned pushes (B9 gate); audit reconciles |
| Storage-full owner | QuotaManager reject → next-in-line owner from Assign list receives shard (R > 1 gives failover for free) |
| Malicious mass push | Ownership gate + quota + ban gater |
| Publisher offline forever, all owners churned | Same as today (content lost) — placement doesn't worsen; audit metrics expose coverage decay |

## 10. Security notes

- B9 gate is mandatory before any push feature ships; unsolicited acceptance
  narrows, never widens.
- ShardSetRecord signatures bind records to publisher keys; validators reject
  unsigned/stale (sequence regression) records — mirrors MemNS hardening.
- Push concurrency + per-peer token buckets prevent amplification.

## 11. Observability

New metrics (obs/metrics pattern): `membuss_placement_shards_pushed_total`,
`_push_failures_total`, `_coverage_ratio` (gauge, per-audit mean),
`_gather_stop_at_k_hits_total` vs `_gather_full_fetches_total`,
`_rebalance_gained/_lost`, `_cold_transitions_total`.
Explorer: per-root shard coverage widget (present/total from audit).

## 12. Test plan

- Unit: shardKey assignment determinism; ring grace transitions; manifest
  validator (sig, sequence); PushBlocksTo happy/fail paths; acceptance-gate
  matrix; GC reachability w/ shardidx; cold-transition job.
- Integration: 5-node docker-compose (deploy/ has compose precedent) —
  ingest 50 MB file, assert: origin stores ≈ raw size; each peer ≤ ⌈n·R/peers⌉
  shards; kill k−1 shard holders → fetch succeeds; kill n−k+1 → fails cleanly.
- Chaos: flapping peer (connect/disconnect every 30 s for 1 h) — assert ring
  membership stable within grace bounds, no rebalance storm.
- Mixed-version: old-binary + new-binary swarm matrix (reuse CI release
  artifacts).
- Regression: post-GC storage ratio assertion ≤ 1.5× for placed files.

## 13. Rollout phases

| Phase | Content | Est. |
|---|---|---|
| M0 | Bug batch (no flags): B5 repair-worker targeting, B6 stop-at-k, B7 GetManifest errors, wantlist TTL | 2–3 d |
| M1 | Manifests on wire + remote gather working end-to-end (B3, fetch rewrite) | 3–5 d |
| M2 | ShardSetRecord DHT discovery + ProvideShard/FindShardSets (B2) | 3–4 d |
| M3 | PushBlocksTo + ownership/quota acceptance gates (B9) | 3–4 d |
| M4 | Placement engine: ingest-time distribution, shard index, GC/DeleteRecursive integration (B1, B4, B8) behind `placement.enabled` | 1.5–2 wk |
| M5 | Ring stability (PEX membership + graces) + rebalancer via ComputeMigration + coverage audit (B10) | 1–1.5 wk |
| M6 | Cold tier + `erasure.keep_raw` + explorer widget + docs | 3–5 d |

Total ≈ 5–7 weeks sequential; M1–M3 parallelizable after M0. Each phase lands
independently useful and revertible.

## 14. Open questions (need answers before M4)

- Q1: Default `replicas: 3` — with k=8..10, is R=3 owners-per-shard enough
  durability for typical swarm sizes, or should R scale with n (e.g. R=⌈n/3⌉)?
  Storage cost scales linearly with R.
- Q2: Gather transport: dedicated ephemeral stream mode in memex (clean, more
  protocol work) vs fetch-via-session then delete non-owned received shards
  (ugly, races with shared blocks)? Leans ephemeral mode; cost estimate needed.
- Q3: Should anchor-mode nodes double as universal shard owners (opt-in role)
  instead of raw-full copies? Would make anchors true erasure hosts.
- Q4: Small-file threshold for skipping distribution — 1 chunk (< 256 KiB)?
  2 chunks?
