# Production Networking

This document describes the Membuss libp2p connectivity stack and the
operational assumptions behind it. The implementation targets go-libp2p
v0.48.0 and go-libp2p-kad-dht v0.41.0.

## Connectivity Flow

Outbound dialing is delegated to libp2p's `swarm.DefaultDialRanker`. It dials
private and public address groups in parallel, prefers QUIC within each group,
uses IPv4/IPv6 Happy Eyeballs, delays TCP briefly behind QUIC, and delays relay
addresses by 500 ms when a direct public address is available. There is no
separate transport priority list in go-libp2p; the dial ranker is the supported
transport selection mechanism.

The effective path is:

1. Try direct QUIC and TCP addresses, with WebSocket as another TCP fallback.
2. AutoNAT v1 publishes aggregate public/private reachability events.
3. AutoNAT v2 evaluates individual public addresses.
4. UPnP or NAT-PMP mapping is attempted when a compatible gateway exists.
5. A private node obtains Circuit Relay v2 reservations through AutoRelay.
6. An inbound relayed connection activates DCUtR coordination and hole
   punching when both peers have usable observed public addresses.
7. New streams use the direct connection after a successful upgrade. The
   relay connection remains the fallback if the upgrade fails.

`force_relay` forces a private reachability verdict so AutoRelay starts
immediately. It does not disable direct dials or DCUtR. go-libp2p does not expose
a supported option that forces every outbound stream through a relay.

## Bootstrap And Relay Roles

`bootstrap_peers` and `relay_peers` are separate configuration lists.

- Bootstrap peers seed the Membuss Kademlia routing table. They need stable,
  publicly dialable addresses but do not need to run a relay service.
- Relay peers run Circuit Relay v2 and need public inbound reachability. They
  may also be bootstrap peers, but that is an explicit deployment choice.
- An empty bootstrap list is valid for isolated, mDNS-only, or freshly created
  test networks. Membuss no longer ships a volatile third-party tunnel as a
  production default.

Initial bootstrap dials use a 30-second per-peer timeout and run in parallel.
Failures include the peer ID and configured addresses. A cancellable background
supervisor retries failed peers using the configured exponential backoff and
jitter. Duplicate peer IDs are dialed once, while their distinct TCP, QUIC, and
WebSocket addresses are merged so transport fallback is preserved. Entries
without both a peer ID and a dial address fail configuration parsing instead of
being silently ignored.

## Relay Discovery And Failover

Relay nodes advertise as independent providers of the
`/membuss/relays/v1` routing-discovery namespace. Provider records allow
multiple relays to coexist; the previous shared DHT value allowed one record to
overwrite another.

The daemon creates AutoRelay with a dynamic peer source before it constructs
the DHT. Dedicated `relay_peers` are available immediately. Once the DHT is
ready, the same source also queries routing discovery. It deduplicates peer IDs,
obeys AutoRelay's requested limit, closes on cancellation, and never keeps a
discovery goroutine alive after host shutdown.

AutoRelay verifies Circuit Relay v2 support before selecting a candidate. It
maintains two reservations when enough providers exist, refreshes reservations
before expiry, removes failed reservations, protects active relay connections
from connection-manager pruning, applies per-peer failure backoff, and replaces
stale candidates. Candidate choice in go-libp2p v0.48.0 is randomized; there is
no public scoring hook. Latency scoring would require an upstream API or a fork.

Relay advertisements are capped at a three-hour refresh interval to match
routing discovery's TTL. A node does not publish until go-libp2p has activated
its relay hop protocol, which occurs only after public reachability is known.

## Reachability Status

The host subscribes to the stateful `EvtLocalReachabilityChanged` event and
stores the latest typed verdict. `NATStatus` returns lowercase `unknown`,
`private`, or `public`. `WaitForNAT` waits on event notifications rather than
polling. The watcher owns its subscription reference, avoiding a close/start
race when a host is created and immediately shut down.

The daemon connects to bootstrap peers before waiting for a verdict. Waiting
before bootstrap cannot work reliably because AutoNAT needs independent peers
to dial the node from outside its current address path. `unknown` remains a
valid result when no eligible AutoNAT observers exist, such as a single-node
network or peers sharing the same address group.

## Address Advertisement

The address factory preserves libp2p's interface, observed, and NAT-mapped
addresses. Operator-provided `announce_addrs` and an optional tunnel address are
added and deduplicated. This is important because replacing the libp2p address
set would discard later AutoNAT and port-mapping updates.

Only advertise addresses that other peers can dial. A DNS or public IP address
does not open a firewall by itself. Cloud security groups must allow the
configured TCP, UDP/QUIC, and optional WebSocket ports.

## Resource Management

Membuss uses go-libp2p's production defaults for its resource manager and
connection manager. In v0.48.0 these include autoscaled protocol/stream limits
and a 160/192 low/high connection watermark. AutoRelay protects reservation
connections from normal pruning.

The relay advertisement loop is idempotent: repeated `Start` calls cannot
create duplicate ticker goroutines. mDNS discovery also releases its peer-list
mutex before any DHT bootstrap dial, so one slow peer cannot block processing
of other discoveries.

Relay service limits cap reservations and circuits. `relay_bandwidth_mb` is
converted to the byte allowance over libp2p's relay limit duration; Circuit
Relay v2 exposes a byte-and-duration budget, not a token-bucket bandwidth API.
This is an approximate per-circuit average, not an exact global rate limit.

## Compatibility And Tradeoffs

- kad-dht v0.41.0 fixes provider-keystore reset collisions, incomplete
  reprovide coverage for clustered peers, dropped regions when resume is
  disabled, and excessive reprovide peak memory. It also pulls QUIC v0.59.1.
- Existing `bootstrap_peers` remain valid but are no longer assumed to be
  relays. Deployments that relied on that behavior must copy relay-capable
  entries into `relay_peers`.
- Existing configs retain their configured bootstrap entries. Only newly
  generated defaults stop including the unavailable ngrok endpoint.
- AutoNAT v2 supplements v1. Aggregate status and AutoRelay activation still
  use the v1 reachability event because that is the go-libp2p contract.
- WebSocket remains enabled for restrictive HTTP-oriented networks, although
  QUIC or TCP normally wins outbound ranking.
- New configs listen on IPv4 and IPv6. mDNS is opt-in for trusted LANs and
  private clusters rather than enabled on every public node.
- During a rolling upgrade, older nodes that read the former shared relay DHT
  value do not understand provider-based discovery. Keep dedicated
  `relay_peers` configured until all nodes run the provider-discovery release.
- Successful hole punching cannot be guaranteed. Symmetric NAT, CGNAT policy,
  UDP blocking, or missing public observed addresses can make relay fallback
  the only viable path.

## Deployment Checklist

1. Configure at least two stable bootstrap peers with TCP and QUIC addresses.
2. Operate at least two public relay providers in different failure domains.
3. Open the TCP, UDP/QUIC, and optional WebSocket listener ports in host and
   cloud firewalls.
4. Use public DNS or IP `announce_addrs` only when they route to the node.
5. Monitor libp2p AutoNAT, AutoRelay reservation, connection-manager, resource
   manager, QUIC black-hole, DHT routing-table, and bootstrap retry metrics.
6. Treat a persistent `unknown` verdict as a lack of eligible AutoNAT observers,
   then verify bootstrap connectivity before changing reachability settings.
