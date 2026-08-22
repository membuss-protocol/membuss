// Command membuss is the Membuss daemon entry point.
//
// Phase 7: the daemon boots the libp2p host, DHT, PEX,
// Memex, Mem-Herald, and (optionally) the Anchor engine,
// then hosts the gRPC service that the CLI dials.
//
// Startup sequence:
//  1. Load config (config.Load).
//  2. Open the BadgerDB block store (core/store.NewMemStore).
//  3. Build the libp2p host with persistent Ed25519 identity.
//  4. Build the DHT and bootstrap to the configured peers.
//  5. Build PEX, Memex, and Mem-Herald; start their loops.
//  6. If AnchorMode is on, build the Anchor engine and start it.
//  7. Start the gRPC server with a daemonBackend that wires
//     every subsystem into the server.Backend interface.
//  8. Wait for SIGINT / SIGTERM and shut everything down.
//
// Phase 17: the host optionally enables libp2p mDNS discovery
// (Config.EnableMDNS / MEMBUSS_MDNS=true), so multiple daemons
// on the same L2 network (e.g. a Docker bridge) auto-discover
// each other without manual peer-ID wiring.
package daemon

import (
	"context"
	cryptoTLS "crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nnlgsakib/membuss/api"
	explorerPkg "github.com/nnlgsakib/membuss/gateway/explorer"
	memgate "github.com/nnlgsakib/membuss/gateway/memgate_v2"

	datastore "github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/nnlgsakib/membuss/core/audit"
	"github.com/nnlgsakib/membuss/core/db"
	"github.com/nnlgsakib/membuss/core/ipc"

	"github.com/nnlgsakib/membuss/anchor"
	"github.com/nnlgsakib/membuss/config"
	"github.com/nnlgsakib/membuss/core/keyring"
	"github.com/nnlgsakib/membuss/core/memedge"
	"github.com/nnlgsakib/membuss/core/memlink"
	"github.com/nnlgsakib/membuss/core/memns"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/shard"
	"github.com/nnlgsakib/membuss/core/store"
	"github.com/nnlgsakib/membuss/core/version"
	"github.com/nnlgsakib/membuss/net/dht"
	"github.com/nnlgsakib/membuss/net/edge_rpc"
	"github.com/nnlgsakib/membuss/net/herald"
	"github.com/nnlgsakib/membuss/net/host"
	memex "github.com/nnlgsakib/membuss/net/memex_v2"
	"github.com/nnlgsakib/membuss/net/pex"
	"github.com/nnlgsakib/membuss/net/tunnel"
	"github.com/nnlgsakib/membuss/obs/logging"
	"github.com/nnlgsakib/membuss/obs/metrics"
	"github.com/nnlgsakib/membuss/pkg/plugin"
	_ "github.com/nnlgsakib/membuss/plugins"
	serverpkg "github.com/nnlgsakib/membuss/rpc/server"
	"golang.org/x/crypto/acme/autocert"
)

// Run boots the Membuss daemon and blocks until it receives
// SIGINT/SIGTERM, then tears every subsystem down in order.
//
// It is the importable entry point shared by the standalone
// daemon dispatch (legacy `membuss -config ...`) and the
// `membuss daemon start` CLI subcommand. args is the argument
// slice *after* the program name (i.e. os.Args[1:] or the
// subcommand's passthrough args).
//
// Fatal boot errors are logged and terminate the process via
// os.Exit(1), preserving the historical standalone behavior;
// the error return is reserved for flag-parsing failures that
// the caller may want to surface through cobra.
func Run(args []string) error {
	fs := flag.NewFlagSet("membuss-daemon", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to YAML config file (overridden by --datadir)")
	datadirFlag := fs.String("datadir", "", "data directory (overrides --config; resolved via MEMBUSS_DATADIR / $HOME/.memdata)")
	build := fs.String("build", "dev", "build identifier reported by Ping")
	inMemory := fs.Bool("in-memory", false, "use an in-memory BadgerDB (no on-disk state)")
	noAnchor := fs.Bool("no-anchor", false, "disable the anchor engine even if config enables it")
	forcePublicFlag := fs.Bool("force-public", false, "force public reachability and keep circuit relay v2 active 24/7")
	forceRelayFlag := fs.Bool("force-relay", false, "force private reachability and obtain relay reservations immediately")
	if err := fs.Parse(args); err != nil {
		return err
	}

	resolvedDatadir := config.ResolveDataDir(*datadirFlag)
	if *datadirFlag != "" {
		*cfgPath = config.DefaultConfigPath(resolvedDatadir)
	} else if *cfgPath == "" {
		*cfgPath = config.DefaultConfigPath(resolvedDatadir)
	}

	// Build identifier flows into Ping responses.
	serverpkg.Build = *build

	// Construct a bootstrap logger at info level until we have
	// the real config. The real logger is built after Load.
	bootLogger := logging.New(os.Stdout, "info")
	if !*inMemory {
		// Phase 16: refuse to start with a clear hint when the
		// operator has not run `membuss init` yet.
		if _, statErr := os.Stat(*cfgPath); statErr != nil {
			dd := config.ResolveDataDir(*datadirFlag)
			fmt.Fprintf(os.Stderr,
				"membuss: node not initialized.\n"+
					"  config: %s\n"+
					"  run:    membuss init --datadir %s\n",
				*cfgPath, dd)
			os.Exit(1)
		}
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		bootLogger.Error("config load failed", "err", err.Error())
		os.Exit(1)
	}
	if *forcePublicFlag {
		cfg.ForcePublic = true
	}
	if *forceRelayFlag {
		cfg.ForceRelay = true
	}
	if os.Getenv("MEMBUSS_FORCE_PUBLIC") == "true" {
		cfg.ForcePublic = true
	}
	if os.Getenv("MEMBUSS_FORCE_RELAY") == "true" {
		cfg.ForceRelay = true
	}
	if cfg.ForcePublic && cfg.ForceRelay {
		bootLogger.Error("force-public and force-relay cannot both be enabled")
		os.Exit(1)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel)
	slog.SetDefault(logger)

	banner(cfg, *cfgPath, *inMemory, *noAnchor)

	// Optional Prometheus instrumentation. Disabled via
	// config (cfg.MetricsEnabled=false) for deployments that
	// do not want the /metrics endpoint.
	var mtrx *metrics.Metrics
	if cfg.MetricsEnabled {
		mtrx = metrics.New()
	}

	// XC-008: append-only audit trail for destructive admin
	// operations (delete / flash / keyring rm), served via the
	// API /audit endpoint.
	aud, err := audit.Open(cfg.DataDir)
	if err != nil {
		logger.Error("audit", "err", err.Error())
		os.Exit(1)
	}
	defer aud.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1) Block store.
	bs, err := openStore(cfg, *inMemory)
	if err != nil {
		logger.Error("open store", "err", err.Error())
		os.Exit(1)
	}
	defer bs.Close()

	// Phase 14: verify the on-disk store is v1-compatible.
	// The MID format changed in Phase 14 (legacy base58 -> CIDv1+base32lower)
	// but the on-disk multihash key layout is format-agnostic, so the
	// migration is a consistency check rather than a rewriter. We also
	// emit a warning if the operator has explicitly asked for the
	// legacy format in config.
	if !*inMemory {
		if ms, ok := bs.(*store.MemStore); ok {
			if res, err := store.MigrateToV1MIDs(ms); err != nil {
				logger.Warn("mid migration scan", "err", err.Error())
			} else if len(res.Legacy) > 0 {
				logger.Warn("legacy MIDs detected in store",
					"count", len(res.Legacy),
					"inspected", res.Inspected)
			} else {
				logger.Info("MID format: v1 (CIDv1 + base32lower)",
					"inspected", res.Inspected)
			}
		}
	}
	if cfg.MIDVersion == "legacy" {
		logger.Warn("MIDVersion=legacy: new content will be emitted in the pre-Phase-14 base58 form")
	}

	// Bootstrap nodes seed the DHT. Relay nodes are a separate role and feed
	// AutoRelay; dynamic relay advertisements supplement the configured set.
	bootstrapPeers, err := parsePeers(cfg.BootstrapPeers)
	if err != nil {
		logger.Error("parse bootstrap peers", "err", err.Error())
		os.Exit(1)
	}
	relayPeers, err := parsePeers(cfg.RelayPeers)
	if err != nil {
		logger.Error("parse relay peers", "err", err.Error())
		os.Exit(1)
	}
	relaySource := host.NewRelayPeerSource(relayPeers)

	// 2) libp2p host.
	// Phase 17: when mDNS discovers a peer, also feed it
	// into the DHT bootstrap list. This makes a private
	// mDNS-only cluster (no static bootstrap peers) form a
	// real DHT, so provider records propagate cross-node
	// and `get` on a non-origin node can find the content.
	// The callback fires after a successful dial; we use
	// a small mutex-guarded dedup so the same peer is
	// only added once even if mDNS rebroadcasts.
	var (
		dhtBootstrapMu sync.Mutex
		dhtBootstrap   []peer.AddrInfo
		dhtSeen        = make(map[peer.ID]struct{})
		heraldPtr      atomic.Value // *herald.MemHerald
	)
	// We use a small pointer indirection so the callback
	// can be wired into hostCfg before mdht is built.
	var dhtPtr atomic.Value // *dht.MemDHT
	addToBootstrap := func(pi peer.AddrInfo) {
		if pi.ID == "" {
			return
		}
		dhtBootstrapMu.Lock()
		if _, ok := dhtSeen[pi.ID]; ok {
			dhtBootstrapMu.Unlock()
			return
		}
		dhtSeen[pi.ID] = struct{}{}
		dhtBootstrap = append(dhtBootstrap, pi)
		dhtBootstrapMu.Unlock()
		logger.Info("mDNS peer discovered", "peer_id", pi.ID.String(), "addrs", pi.Addrs)
		// dhtPtr gets set after mdht is built (see below).
		// Calls that happen before then are replayed once DHT construction
		// completes. Never hold dhtBootstrapMu across network operations.
		if v := dhtPtr.Load(); v != nil {
			if mdht, ok := v.(*dht.MemDHT); ok && mdht != nil {
				bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = mdht.Bootstrap(bg, []peer.AddrInfo{pi})
			}
		}
		// Signal Mem-Herald to re-announce immediately so
		// newly discovered peers see our content right away.
		if v := heraldPtr.Load(); v != nil {
			if hd, ok := v.(*herald.MemHerald); ok && hd != nil {
				hd.Trigger()
			}
		}
	}

	hostCfg := host.Config{
		ListenAddrs:   cfg.ListenAddrs,
		AnnounceAddrs: cfg.AnnounceAddrs,
		DataDir:       cfg.DataDir,
		UserAgent: func() string {
			ua := "membuss/v" + version.Version
			commit := version.GitCommit
			if len(commit) > 7 {
				commit = commit[:7]
			}
			if commit != "" && commit != "unknown" {
				ua += "/" + commit
			} else if *build != "" && *build != "dev" {
				ua += "/" + *build
			}
			return ua
		}(),
		RelayPeerSource: relaySource.PeerSource(),
		MDNS:            cfg.EnableMDNS || os.Getenv("MEMBUSS_MDNS") == "true",
		OnPeerFound:     addToBootstrap,
		// --- Phase 11: NAT traversal ---
		RelayService:         cfg.RelayService,
		RelayMaxConns:        cfg.RelayMaxConns,
		RelayMaxReservations: cfg.RelayMaxReservations,
		RelayBandwidthMB:     cfg.RelayBandwidthMB,
		ForceRelay:           cfg.ForceRelay,
		ForcePublic:          cfg.ForcePublic || os.Getenv("MEMBUSS_FORCE_PUBLIC") == "true",
	}
	h, err := host.NewHost(hostCfg)
	if err != nil {
		logger.Error("host", "err", err.Error())
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "  peer_id:        %s\n", h.ID())
	fmt.Fprintf(os.Stdout, "  listen_addrs:   %v\n", h.Addrs())
	// 3) DHT.
	// Phase 17: provider records propagate cross-node only
	// when (a) the DHT is willing to act as a server, (b)
	// provider records are persisted into a datastore the
	// kad-dht ProviderManager can read from, and (c) the
	// optimistic provide shortcut is on. We use a pebble
	// backed datastore so provider records survive restarts;
	// the reprovide loop in Mem-Herald keeps them fresh.
	dhtDir := filepath.Join(cfg.DataDir, "dht")
	dhtDS, err := db.NewPebbleDatastore(dhtDir, false)
	if err != nil {
		logger.Error("dht datastore", "err", err.Error())
		os.Exit(1)
	}
	mdht, err := dht.New(ctx, dht.Config{
		Host:                    h,
		ModeName:                cfg.DHTMode,
		OptimisticProvide:       cfg.DHTOptimisticProvide,
		Datastore:               dhtDS,
		ProviderRecordTTL:       cfg.DHTProviderRecordTTL,
		ProviderAddrTTL:         cfg.DHTProviderAddrTTL,
		ProviderCleanupInterval: cfg.DHTProviderCleanupInterval,
		ConnectionGater:         h.ConnectionGater(),
		BandwidthCounter:        h.BandwidthCounter(),
	})
	defer func() { _ = dhtDS.Close() }()
	if err != nil {
		logger.Error("dht", "err", err.Error())
		os.Exit(1)
	}
	relaySource.SetFinder(mdht.FindRelays)

	// Filter out local node's own PeerID from bootstrap peer set to avoid dial-to-self warnings.
	var activeBootstrap []peer.AddrInfo
	for _, p := range bootstrapPeers {
		if p.ID != h.ID() {
			activeBootstrap = append(activeBootstrap, p)
		}
	}
	bootstrapPeers = activeBootstrap

	if err := mdht.Bootstrap(ctx, bootstrapPeers); err != nil {
		logger.Debug("dht bootstrap", "err", err.Error())
	}
	if len(bootstrapPeers) > 0 {
		go func() {
			successes, retryErr := mdht.BootstrapWithBackoff(ctx, bootstrapPeers, dht.BootstrapConfig{
				Initial:     cfg.BootstrapBackoff.Initial,
				Max:         cfg.BootstrapBackoff.Max,
				Factor:      cfg.BootstrapBackoff.Factor,
				MaxAttempts: cfg.BootstrapBackoff.MaxAttempts,
				Logger:      logger,
			})
			if retryErr != nil && ctx.Err() == nil {
				logger.Warn("dht bootstrap retries exhausted", "connected", successes, "err", retryErr.Error())
			}
		}()
	}
	logger.Info("dht ready",
		"mode", cfg.DHTMode,
		"optimistic_provide", cfg.DHTOptimisticProvide,
		"bootstrap_peers", len(bootstrapPeers),
	)

	// AutoNAT needs independently reachable peers to probe this host. Waiting
	// before bootstrap guaranteed an Unknown result; observe it only after the
	// first connectivity attempt, while the event subscription remains live.
	wait := time.Duration(cfg.NATWaitSeconds) * time.Second
	natStatus, natErr := h.WaitForNAT(ctx, wait)
	if natErr != nil && !errors.Is(natErr, context.DeadlineExceeded) {
		logger.Warn("nat wait", "err", natErr.Error())
	}
	fmt.Fprintf(os.Stdout, "  nat_status:     %s\n", natStatus)
	if natStatus == "private" {
		logger.Info("node is behind a NAT; relay addresses will be advertised")
	}
	// Phase 17: now that the DHT exists, register it with
	// the mDNS peer-found callback and replay any peers
	// that were discovered while the DHT was still being
	// built. The replay uses the same per-peer dedup so
	// the live callback will not re-add them.
	dhtPtr.Store(mdht)
	dhtBootstrapMu.Lock()
	replay := append([]peer.AddrInfo(nil), dhtBootstrap...)
	dhtBootstrapMu.Unlock()
	if len(replay) > 0 {
		if err := mdht.Bootstrap(ctx, replay); err != nil {
			logger.Debug("dht bootstrap (replay)", "err", err.Error())
		}
	}
	defer mdht.Close()
	defer h.Close()

	// 4) PEX.
	px, err := pex.New(pex.Config{Host: h, PersistPath: filepath.Join(cfg.DataDir, "pex.db")})
	if err != nil {
		logger.Error("pex", "err", err.Error())
		os.Exit(1)
	}
	px.Start(ctx)
	defer px.Stop()

	// 5a) Memex bloom manager (peer filter exchange, Phase 13).
	// Constructed first so we can wire it into the engine.
	bloomMgr, err := memex.NewBloomManager(memex.BloomConfig{
		Host:     h,
		Sealed:   bs,
		Interval: cfg.MemexBloomAnnounceInterval,
	})
	if err != nil {
		logger.Error("memex bloom", "err", err.Error())
		os.Exit(1)
	}
	bloomMgr.Start()
	defer bloomMgr.Stop()

	// 5b) Memex engine.
	mx, err := memex.New(memex.Config{Host: h, Blockstore: bs, Bloom: bloomMgr})
	if err != nil {
		logger.Error("memex", "err", err.Error())
		os.Exit(1)
	}
	mx.Start()
	defer mx.Stop()

	// Phase 18: MemNS and KeyRing setup
	kr := keyring.NewKeyRing(cfg.DataDir)
	pm, err := memns.NewPubSubManager(h)
	if err != nil {
		logger.Warn("gossipsub init failed", "err", err.Error())
	}
	cache := memns.NewRecordCache(1000)
	memnsRes := memns.NewResolver(mdht, pm, cache)
	dnsRes := memlink.NewDNSResolver(func(ctx context.Context, name string) (string, error) {
		return memnsRes.Resolve(ctx, name)
	})
	memnsRes.SetDNSResolver(dnsRes)

	// 6) Mem-Herald & Shard Ring.
	var heraldStrat herald.Strategy
	switch strings.ToLower(strings.TrimSpace(cfg.ReprovideStrategy)) {
	case "all":
		heraldStrat = herald.StrategyAll
	case "shards":
		heraldStrat = herald.StrategyShards
	default:
		heraldStrat = herald.StrategyRoots
	}

	shardStore := dhtStoreWrapper{ds: dhtDS}
	shardRing := shard.NewHashRing()
	shardRing.AddPeer(h.ID().String())
	_ = shard.LoadPeers(shardStore, shardRing)

	h.Network().Notify(&network.NotifyBundle{
		ConnectedF: func(n network.Network, c network.Conn) {
			pID := c.RemotePeer().String()
			shardRing.AddPeer(pID)
			_ = shard.PersistPeers(shardStore, shardRing.Peers())
		},
		DisconnectedF: func(n network.Network, c network.Conn) {
			pID := c.RemotePeer()
			if len(n.ConnsToPeer(pID)) == 0 {
				shardRing.RemovePeer(pID.String())
				_ = shard.PersistPeers(shardStore, shardRing.Peers())
			}
		},
	})

	hd, err := herald.New(herald.Config{
		Store:           heraldStoreWrapper{bs},
		DHT:             mdht,
		Strategy:        heraldStrat,
		ShardRing:       shardRing,
		PeerID:          h.ID().String(),
		Replicas:        cfg.ShardReplicas,
		Interval:        cfg.ReprovideInterval,
		Rate:            100,
		Burst:           8,
		KeyRing:         kr,
		MemDHT:          mdht,
		Metrics:         mtrx,
		ReprovideGroups: cfg.ReprovideGroups,
	})
	if err != nil {
		logger.Error("herald", "err", err.Error())
		os.Exit(1)
	}
	hd.Start(ctx)
	defer hd.Stop()
	heraldPtr.Store(hd)

	// 6c) Content publisher — runs on all nodes and serves
	// sealed MID lists on the content-exchange stream.
	cp := anchor.NewContentPublisher(h, bs, mdht)
	cp.Start(ctx)
	defer cp.Stop()

	// Phase 11: relay announcer. Only wired when the
	// node runs the relay service; other nodes just
	// consume the DHT relay list when they bootstrap.
	var relayAnnouncer *herald.RelayAnnouncer
	if cfg.RelayService {
		relayAnnouncer, err = herald.NewRelayAnnouncer(herald.RelayAnnouncer{
			DHT:      mdht,
			Interval: cfg.ReprovideInterval,
			Logger:   logger,
		})
		if err != nil {
			logger.Error("relay announcer", "err", err.Error())
			os.Exit(1)
		}
		relayAnnouncer.Start(ctx)
		fmt.Fprintf(os.Stdout, "  relay_service:  enabled\n")
	}

	// 6b) Auto GC background loop (mutually exclusive with anchor).
	if cfg.AutoGCInterval > 0 && !cfg.AnchorMode {
		go func() {
			t := time.NewTicker(cfg.AutoGCInterval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					freed, gerr := bs.GCWithMinAge(ctx, cfg.GCMinAge)
					if gerr != nil {
						logger.Warn("auto gc", "err", gerr.Error())
					} else if freed > 0 {
						logger.Info("auto gc", "freed", freed)
					}
				}
			}
		}()
	}

	// 7) Optional Anchor engine.
	var anchorEng *anchor.AnchorEngine
	if cfg.AnchorMode && !*noAnchor {
		fetcher := &memexFetcher{eng: mx, dht: mdht}
		anchorEng, err = anchor.New(anchor.Config{
			Host:              h,
			DHT:               mdht,
			Store:             bs,
			Herald:            hd,
			Fetcher:           fetcher,
			DiscoveryInterval: 30 * time.Second,
			Logger:            &slogAnchorLogger{l: logger},
		})
		if err != nil {
			logger.Error("anchor", "err", err.Error())
			os.Exit(1)
		}
		if err := anchorEng.Start(ctx); err != nil {
			logger.Error("anchor start", "err", err.Error())
			os.Exit(1)
		}
		defer anchorEng.Stop()
		fmt.Fprintf(os.Stdout, "  anchor_mode:    enabled\n")
	} else {
		fmt.Fprintf(os.Stdout, "  anchor_mode:    disabled\n")
	}

	// 8) gRPC server.
	backend := &daemonBackend{
		dataDir:      cfg.DataDir,
		host:         h,
		store:        bs,
		dht:          mdht,
		pex:          px,
		memex:        mx,
		herald:       hd,
		anchor:       anchorEng,
		metrics:      mtrx,
		retryBackoff: cfg.MemexRetryBackoff,
		logger:       logger,
	}

	// 8) IPC Socket & gRPC / Node API multiplexer.
	var ipcPath string
	var ipcLis, grpcIPCLis, httpIPCLis net.Listener
	if cfg.Servers.IPC.Enabled {
		ipcPath = cfg.IPCPath
		if ipcPath == "" {
			ipcPath = ipc.DefaultSocketPath(cfg.DataDir)
		}
		lis, ipcErr := ipc.Listen(ipcPath)
		if ipcErr != nil {
			logger.Warn("ipc listen warning", "path", ipcPath, "err", ipcErr.Error())
		} else {
			ipcLis = lis
			grpcIPCLis, httpIPCLis = ipc.Multiplex(ipcLis)
			defer ipc.Cleanup(ipcPath)
			defer ipcLis.Close()
			fmt.Fprintf(os.Stdout, "  ipc_path:       %s\n", ipcPath)
		}
	} else {
		logger.Info("IPC server disabled by config")
		fmt.Fprintf(os.Stdout, "  ipc_path:       disabled\n")
	}

	var grpcSrv *serverGRPC
	if cfg.Servers.GRPC.Enabled {
		srv, err := startGRPC(cfg.GRPCAddr, grpcIPCLis, backend)
		if err != nil {
			logger.Error("grpc", "err", err.Error())
			os.Exit(1)
		}
		grpcSrv = srv
		fmt.Fprintf(os.Stdout, "  grpc_addr:      %s\n", cfg.GRPCAddr)
	} else {
		logger.Info("gRPC server disabled by config")
		fmt.Fprintf(os.Stdout, "  grpc_addr:      disabled\n")
	}

	// Tunnel: programmatic libp2p port forwarding.
	tunMgr := tunnel.NewManager(*cfgPath)
	if cfg.Tunnel.Enabled {
		logger.Info("starting tunnel client...")
		if err := tunMgr.Start(ctx, cfg); err != nil {
			logger.Warn("failed to start tunnel on boot", "err", err.Error())
		}
	}
	defer tunMgr.Stop()

	// 8b) Plugin Engine Boot & Lifecycle.
	hookBus := plugin.NewHookBus()
	if ms, ok := bs.(*store.MemStore); ok {
		ms.SetHooks(hookBus)
	}
	gateHTTPReg := plugin.NewMapHTTPRegistry()
	nodeHTTPReg := plugin.NewMapHTTPRegistry()
	cliReg := plugin.NewMapCLIRegistry()

	pluginCore := &plugin.Core{
		Config:      cfg,
		Store:       bs,
		Host:        h,
		DHT:         mdht,
		Memex:       mx,
		PEX:         px,
		Herald:      hd,
		Anchor:      anchorEng,
		MemNS:       memnsRes,
		Keyring:     kr,
		Metrics:     mtrx,
		Hooks:       hookBus,
		GatewayHTTP: gateHTTPReg,
		NodeHTTP:    nodeHTTPReg,
		CLIRegistry: cliReg,
		IPCPath:     ipcPath,
		IPCListener: ipcLis,
		Logger:      logger,
	}

	if err := plugin.BootPlugins(pluginCore); err != nil {
		logger.Error("plugin boot failed", "err", err.Error())
	}
	if err := plugin.StartPlugins(ctx); err != nil {
		logger.Error("plugin start failed", "err", err.Error())
	}
	defer plugin.StopPlugins(ctx)

	// 8c) MemEdge: Serverless Edge Compute Engine (Wasm/Go & JS).
	var edgeEngine memedge.Engine
	var edgeSvc *edge_rpc.Service
	if cfg.EdgeCompute.Enabled && cfg.EdgeCompute.Mode != "off" {
		edgeCfg := memedge.Config{
			Enabled: true,
			Mode:    cfg.EdgeCompute.Mode,
			DefaultLimits: memedge.Limits{
				MaxExecutionTime: cfg.EdgeCompute.MaxExecutionTime,
				MaxMemoryBytes:   uint64(cfg.EdgeCompute.MaxMemoryMB) << 20,
				MaxBodySizeBytes: 10 << 20,
			},
			MaxConcurrentTasks: cfg.EdgeCompute.MaxConcurrentTasks,
			CacheCapacity:      256,
		}
		var engErr error
		edgeEngine, engErr = memedge.NewEngine(ctx, edgeCfg)
		if engErr != nil {
			logger.Warn("memedge engine init", "err", engErr.Error())
		} else {
			defer edgeEngine.Close()
			if h != nil {
				edgeSvc = edge_rpc.NewService(h, edgeEngine)
			}
			fmt.Fprintf(os.Stdout, "  edge_compute:   enabled (mode: %s)\n", cfg.EdgeCompute.Mode)
		}
	} else {
		fmt.Fprintf(os.Stdout, "  edge_compute:   disabled\n")
	}

	var gateSrv *httpServer
	if cfg.Servers.Gateway.Enabled {
		srv, err := startGateway(cfg.GatewayAddr, newMemgateAdapter(backend), newExplorerAdapter(backend, cfg.AnchorMode, kr, memnsRes), cfg.GatewayRateLimitPerMin, cfg.GatewayTLS, memnsRes, cfg.DataDir, cfg.LogLevel, aud, tunMgr, gateHTTPReg, cfg.MetricsToken, edgeEngine, edgeSvc)
		if err != nil {
			logger.Error("gateway", "err", err.Error())
			os.Exit(1)
		}
		gateSrv = srv
		fmt.Fprintf(os.Stdout, "  gateway_addr:   %s\n", gateSrv.Addr())
	} else {
		logger.Info("gateway server disabled by config")
		fmt.Fprintf(os.Stdout, "  gateway_addr:   disabled\n")
	}

	// 10) Node API: local control plane over HTTP/JSON.
	var apiSrv *httpServer
	if cfg.Servers.NodeAPI.Enabled {
		srv, err := startNodeAPI(cfg.APIAddr, newAPIAdapter(backend), mtrx, cfg.APIKey, cfg.APITLS, kr, memnsRes, cfg.DataDir, cfg.LogLevel, aud, nodeHTTPReg, edgeEngine, edgeSvc, httpIPCLis)
		if err != nil {
			logger.Error("api", "err", err.Error())
			os.Exit(1)
		}
		apiSrv = srv
		fmt.Fprintf(os.Stdout, "  api_addr:       %s\n", apiSrv.Addr())
	} else {
		logger.Info("node API server disabled by config")
		fmt.Fprintf(os.Stdout, "  api_addr:       disabled\n")
	}

	// Local discovery loop: write our own peer.info, and periodically scan siblings to auto-connect.
	var peerInfoPath string
	if cfg.DataDir != "" && !*inMemory {
		peerInfoPath = filepath.Join(cfg.DataDir, "peer.info")
		pinfo := struct {
			PeerID string   `json:"peer_id"`
			Addrs  []string `json:"addrs"`
		}{
			PeerID: h.ID().String(),
		}
		for _, a := range h.Addrs() {
			pinfo.Addrs = append(pinfo.Addrs, a.String())
		}
		if data, err := json.Marshal(pinfo); err == nil {
			_ = os.WriteFile(peerInfoPath, data, 0644)
		}
		defer func() {
			_ = os.Remove(peerInfoPath)
		}()

		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					parent := filepath.Dir(cfg.DataDir)
					entries, err := os.ReadDir(parent)
					if err != nil {
						continue
					}
					for _, entry := range entries {
						if !entry.IsDir() {
							continue
						}
						siblingPath := filepath.Join(parent, entry.Name())
						if filepath.Clean(siblingPath) == filepath.Clean(cfg.DataDir) {
							continue
						}
						infoPath := filepath.Join(siblingPath, "peer.info")
						data, err := os.ReadFile(infoPath)
						if err != nil {
							continue
						}
						var siblingInfo struct {
							PeerID string   `json:"peer_id"`
							Addrs  []string `json:"addrs"`
						}
						if err := json.Unmarshal(data, &siblingInfo); err != nil {
							continue
						}
						pid, err := peer.Decode(siblingInfo.PeerID)
						if err != nil {
							continue
						}
						var addrs []multiaddr.Multiaddr
						for _, aStr := range siblingInfo.Addrs {
							if ma, err := multiaddr.NewMultiaddr(aStr); err == nil {
								addrs = append(addrs, ma)
							}
						}
						if len(addrs) > 0 {
							ai := peer.AddrInfo{
								ID:    pid,
								Addrs: addrs,
							}
							if h.Host.Network().Connectedness(pid) != network.Connected {
								go func(info peer.AddrInfo) {
									dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
									defer dialCancel()
									if err := h.Host.Connect(dialCtx, info); err == nil {
										logger.Info("connected to sibling local peer via peer.info discovery", "peer_id", info.ID.String())
									}
								}(ai)
							}
						}
					}
				}
			}
		}()
	}

	// Graceful shutdown: wait for SIGINT/SIGTERM, then
	// drain in the order: HTTP servers (gateway, api),
	// gRPC, memex engine, herald, anchor, pex, dht,
	// libp2p host, store.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("shutdown requested", "signal", sig.String())
	cancel() // Stop all background routines using the main context

	// Global watchdog: if the process takes more than 10 seconds to shut down
	// (e.g. because database flushing gets stuck on Windows volume errors),
	// force exit the process.
	go func() {
		time.Sleep(10 * time.Second)
		logger.Error("shutdown watchdog: graceful teardown timed out, force exiting")
		os.Exit(0)
	}()

	shutdownCtx, scancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer scancel()

	if gateSrv != nil {
		if err := gateSrv.ShutdownCtx(shutdownCtx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				logger.Info("gateway shutdown: active connections force-closed")
			} else {
				logger.Warn("gateway shutdown", "err", err.Error())
			}
		}
	}
	if apiSrv != nil {
		if err := apiSrv.ShutdownCtx(shutdownCtx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				logger.Info("api shutdown: active connections force-closed")
			} else {
				logger.Warn("api shutdown", "err", err.Error())
			}
		}
	}

	if grpcSrv != nil {
		// Stop gRPC server with timeout. GracefulStop blocks until all connections
		// are closed, which can hang indefinitely if a client keeps a stream open.
		grpcStopped := make(chan struct{})
		go func() {
			grpcSrv.GracefulStop()
			close(grpcStopped)
		}()

		select {
		case <-grpcStopped:
			logger.Info("grpc shutdown complete")
		case <-shutdownCtx.Done():
			logger.Warn("grpc graceful shutdown timed out; force stopping")
			grpcSrv.Stop()
		}
	}

	if err := mx.StopWait(shutdownCtx); err != nil {
		logger.Warn("memex stop", "err", err.Error())
	}
	logger.Info("shutdown complete")
	return nil
}

// banner writes the startup banner to stdout.
func banner(cfg *config.Config, cfgPath string, inMemory, noAnchor bool) {
	gwStr := cfg.GatewayAddr
	if !cfg.Servers.Gateway.Enabled {
		gwStr = "disabled"
	}
	apiStr := cfg.APIAddr
	if !cfg.Servers.NodeAPI.Enabled {
		apiStr = "disabled"
	}
	fmt.Fprintf(os.Stdout,
		"membuss daemon starting\n"+
			"  config:           %s\n"+
			"  data_dir:         %s\n"+
			"  in_memory:        %t\n"+
			"  no_anchor:        %t\n"+
			"  bootstrap_peers:  %d\n"+
			"  relay_peers:      %d\n"+
			"  http_cfg_addrs:   gateway=%s api=%s\n"+
			"  reprovide:        %s\n",
		cfgPath, cfg.DataDir, inMemory, noAnchor,
		len(cfg.BootstrapPeers),
		len(cfg.RelayPeers),
		gwStr, apiStr,
		cfg.ReprovideInterval,
	)
}

// openStore opens the local block store. When
// inMemory is true, the store is backed by RAM and discards
// its contents on Close. The data dir is still passed through
// to subsystems that need it (host identity, etc.).
func openStore(cfg *config.Config, inMemory bool) (store.Store, error) {
	bloom := store.BloomConfig{
		Capacity: cfg.BloomCapacity,
		FPRate:   cfg.BloomFPRate,
		Disabled: cfg.BloomDisabled,
	}
	if !inMemory && !cfg.BloomDisabled {
		bloom.SnapshotPath = filepath.Join(cfg.DataDir, "bloom.bin")
	}
	if inMemory {
		return store.NewMemStore(store.Options{InMemory: true, Bloom: bloom})
	}

	// Migrate any legacy BadgerDB files from dataDir root to dataDir/datastore
	if err := migrateBadgerFiles(cfg.DataDir); err != nil {
		return nil, fmt.Errorf("store migration: %w", err)
	}

	datastorePath := filepath.Join(cfg.DataDir, "datastore")
	if isBadgerDbDir(datastorePath) {
		slog.Info("Legacy BadgerDB detected. Removing old datastore to initialize Pebble...", "path", datastorePath)
		_ = os.RemoveAll(datastorePath)
	}

	return store.NewMemStore(store.Options{Path: datastorePath, Bloom: bloom})
}

func isBadgerDbDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "DISCARD" || name == "KEYREGISTRY" || filepath.Ext(name) == ".vlog" {
			return true
		}
	}
	return false
}

// migrateBadgerFiles moves BadgerDB files from the root of dataDir to the
// dataDir/datastore subdirectory to keep the root directory clean.
func migrateBadgerFiles(dataDir string) error {
	datastoreDir := filepath.Join(dataDir, "datastore")
	if err := os.MkdirAll(datastoreDir, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}

	badgerNames := map[string]bool{
		"MANIFEST":    true,
		"KEYREGISTRY": true,
		"DISCARD":     true,
		"LOCK":        true,
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		shouldMove := false

		if badgerNames[name] {
			shouldMove = true
		} else {
			ext := filepath.Ext(name)
			if ext == ".sst" || ext == ".vlog" {
				shouldMove = true
			}
		}

		if shouldMove {
			oldPath := filepath.Join(dataDir, name)
			newPath := filepath.Join(datastoreDir, name)
			if err := os.Rename(oldPath, newPath); err != nil {
				return fmt.Errorf("migrate badger file %s: %w", name, err)
			}
		}
	}
	return nil
}

// parsePeers converts a list of "peerID+multiaddr" strings
// (as used in the config file) into peer.AddrInfo values.
func parsePeers(raw []string) ([]peer.AddrInfo, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]peer.AddrInfo, 0, len(raw))
	byID := make(map[peer.ID]int, len(raw))
	seenAddrs := make(map[peer.ID]map[string]struct{}, len(raw))
	for _, s := range raw {
		ai, err := parsePeer(s)
		if err != nil {
			return nil, fmt.Errorf("parse peer %q: %w", s, err)
		}
		if ai.ID == "" {
			return nil, fmt.Errorf("parse peer %q: multiaddr is missing /p2p/<peer-id>", s)
		}
		if len(ai.Addrs) == 0 {
			return nil, fmt.Errorf("parse peer %q: peer ID has no dial address", s)
		}
		idx, ok := byID[ai.ID]
		if !ok {
			idx = len(out)
			byID[ai.ID] = idx
			out = append(out, peer.AddrInfo{ID: ai.ID})
			seenAddrs[ai.ID] = make(map[string]struct{}, len(ai.Addrs))
		}
		for _, addr := range ai.Addrs {
			key := string(addr.Bytes())
			if _, ok := seenAddrs[ai.ID][key]; ok {
				continue
			}
			seenAddrs[ai.ID][key] = struct{}{}
			out[idx].Addrs = append(out[idx].Addrs, addr)
		}
	}
	return out, nil
}

// parsePeer parses a single bootstrap peer string in either of:
//
//	/ip4/1.2.3.4/tcp/4001/p2p/<peerID>   (full multiaddr)
//	<peerID>                              (parsed for compatibility; rejected by parsePeers)
func parsePeer(s string) (peer.AddrInfo, error) {
	if s == "" {
		return peer.AddrInfo{}, fmt.Errorf("empty peer string")
	}
	// Try full multiaddr.
	ma, err := multiaddr.NewMultiaddr(s)
	if err == nil {
		ai, err := peer.AddrInfoFromP2pAddr(ma)
		if err == nil {
			return *ai, nil
		}
		// Fall through: maybe it's a multiaddr without /p2p/ suffix.
		return peer.AddrInfo{Addrs: []multiaddr.Multiaddr{ma}}, nil
	}
	// Try plain peer ID.
	id, err := peer.Decode(s)
	if err != nil {
		return peer.AddrInfo{}, fmt.Errorf("not a valid multiaddr or peer id: %w", err)
	}
	return peer.AddrInfo{ID: id}, nil
}

// startGRPC brings up the gRPC server on TCP addr and optional IPC listener.
func startGRPC(addr string, ipcLis net.Listener, b serverpkg.Backend) (*serverGRPC, error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	s := newServerGRPC()
	serverpkg.NewServer(b).Register(s.gsrv)
	go func() { _ = s.gsrv.Serve(lis) }()
	if ipcLis != nil {
		go func() { _ = s.gsrv.Serve(ipcLis) }()
	}
	return s, nil
}

// serverGRPC is a small wrapper to make the gRPC server
// addressable from main() and to keep the construction in
// one place.
type serverGRPC struct {
	gsrv *serverGRPCServer
}

// newServerGRPC creates a fresh gRPC server with sensible
// defaults. Kept as a separate constructor so it can be
// replaced in tests with a bufconn-based listener.
func newServerGRPC() *serverGRPC {
	return &serverGRPC{gsrv: newGRPCServer()}
}

func (s *serverGRPC) GracefulStop() { s.gsrv.GracefulStop() }
func (s *serverGRPC) Stop()         { s.gsrv.Stop() }

// startGateway brings up the public Mem-Gate HTTP server.
// rateLimitPerMin is the per-IP request budget enforced on
// every public request. tls enables HTTPS when its
// CertFile/KeyFile are set.
func startGateway(addr string, b memgate.Backend, exp *explorerAdapter, rateLimitPerMin int, tlsCfg config.TLSConfig, memnsRes *memns.Resolver, dataDir string, logLevel string, aud *audit.Logger, tunMgr *tunnel.Manager, pluginReg *plugin.MapHTTPRegistry, metricsToken string, edgeEngine memedge.Engine, edgeSvc *edge_rpc.Service) (*httpServer, error) {
	mg, err := memgate.New(memgate.Config{
		Backend:         b,
		MaxCacheBytes:   64 << 20, // 64 MiB LRU
		ExplorerHandler: buildExplorer(exp, aud, tunMgr, edgeEngine, edgeSvc),
		RateLimitPerMin: rateLimitPerMin,
		MemNSResolver:   memnsRes,
		LogLevel:        logLevel,
		MetricsToken:    metricsToken,
		EdgeEngine:      edgeEngine,
		EdgeService:     edgeSvc,
	})
	if err != nil {
		return nil, fmt.Errorf("memgate: %w", err)
	}
	if pluginReg != nil {
		r := mg.ChiRouter()
		for k, h := range pluginReg.Handlers {
			parts := strings.SplitN(k, " ", 2)
			if len(parts) == 2 {
				r.Method(parts[0], parts[1], h)
			}
		}
	}
	hs, err := startHTTP(addr, "membuss-gateway", mg.Handler(), tlsCfg, dataDir)
	if err != nil {
		_ = mg.Close()
		return nil, err
	}
	hs.onClose = mg.Shutdown
	return hs, nil
}

// startNodeAPI brings up the local Node control API. mtrx
// exposes Prometheus at /metrics; apiKey enables X-Membuss-Key
// auth on every /api/v1 endpoint; tls enables HTTPS.
func startNodeAPI(addr string, b api.Backend, mtrx *metrics.Metrics, apiKey string, tlsCfg config.TLSConfig, keyring *keyring.KeyRing, memnsRes *memns.Resolver, dataDir string, logLevel string, aud *audit.Logger, pluginReg *plugin.MapHTTPRegistry, edgeEngine memedge.Engine, edgeSvc *edge_rpc.Service, extraLis ...net.Listener) (*httpServer, error) {
	nodeAPI, err := api.New(api.Config{
		Backend:        b,
		MaxUploadBytes: 1 << 30, // 1 GiB
		APIKey:         apiKey,
		Metrics:        mtrx,
		Audit:          aud,
		KeyRing:        keyring,
		MemNSResolver:  memnsRes,
		LogLevel:       logLevel,
		PluginRoutes:   pluginReg,
		EdgeEngine:     edgeEngine,
		EdgeService:    edgeSvc,
	})
	if err != nil {
		return nil, fmt.Errorf("nodeapi: %w", err)
	}
	return startHTTP(addr, "membuss-api", nodeAPI.Handler(), tlsCfg, dataDir, extraLis...)
}

// startHTTP binds an http.Handler to addr in a goroutine and
// returns a handle whose Close method does a graceful shutdown.
// A non-ErrServerClosed error from Serve is logged. When tls
// is non-empty, the server runs HTTPS with the supplied cert+key.
func startHTTP(addr, name string, h http.Handler, tlsCfg config.TLSConfig, dataDir string, extraLis ...net.Listener) (*httpServer, error) {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       0,
		WriteTimeout:      1 * time.Hour,
		IdleTimeout:       10 * time.Minute,
	}

	var certManager *autocert.Manager
	if name == "membuss-gateway" && os.Getenv("MEMBUSS_GATEWAY_AUTOCERT") == "true" {
		certDir := filepath.Join(dataDir, "certs")
		_ = os.MkdirAll(certDir, 0700)
		certManager = &autocert.Manager{
			Prompt: autocert.AcceptTOS,
			Cache:  autocert.DirCache(certDir),
			HostPolicy: func(ctx context.Context, host string) error {
				_, err := net.LookupTXT("_memlink." + host)
				if err != nil {
					return fmt.Errorf("host %s not authorized: %w", host, err)
				}
				return nil
			},
		}
	}

	if tlsCfg.Enabled() || certManager != nil {
		srv.TLSConfig = &cryptoTLS.Config{
			MinVersion: cryptoTLS.VersionTLS12,
		}

		if tlsCfg.Enabled() {
			cert, err := cryptoTLS.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("%s: load tls: %w", name, err)
			}
			srv.TLSConfig.Certificates = []cryptoTLS.Certificate{cert}
		}

		srv.TLSConfig.GetCertificate = func(hello *cryptoTLS.ClientHelloInfo) (*cryptoTLS.Certificate, error) {
			if certManager != nil {
				cert, err := certManager.GetCertificate(hello)
				if err == nil && cert != nil {
					return cert, nil
				}
			}
			if len(srv.TLSConfig.Certificates) > 0 {
				return &srv.TLSConfig.Certificates[0], nil
			}
			return nil, nil
		}
	}

	for _, extra := range extraLis {
		if extra != nil {
			go func(l net.Listener) {
				extraSrv := &http.Server{
					Handler:           h,
					ReadHeaderTimeout: 30 * time.Second,
					ReadTimeout:       0,
				}
				var err error
				if srv.TLSConfig != nil {
					err = extraSrv.Serve(cryptoTLS.NewListener(l, srv.TLSConfig))
				} else {
					err = extraSrv.Serve(l)
				}
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					slog.Error("http serve extra listener", "name", name, "err", err.Error())
				}
			}(extra)
		}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	hs := &httpServer{name: name, srv: srv, ln: ln, boundAddr: ln.Addr().String()}
	go func() {
		var err error
		if srv.TLSConfig != nil {
			// We need a TLS listener for ServeTLS; rebuild it.
			tlsLn := cryptoTLS.NewListener(ln, srv.TLSConfig)
			err = srv.Serve(tlsLn)
		} else {
			err = srv.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http serve", "name", name, "err", err.Error())
		}
	}()
	return hs, nil
}

// httpServer is a thin wrapper around http.Server that
// remembers its bound listener and a friendly name for
// logs. Close performs a 5s graceful shutdown.
type httpServer struct {
	name string
	srv  *http.Server
	ln   net.Listener

	// boundAddr is the address the OS assigned to the
	// listener. Captured at startHTTP time and stable
	// for the lifetime of the listener (useful when the
	// caller passed ":0" and needs the resolved port).
	boundAddr string
	onClose   func(ctx context.Context) error
}

// Addr returns the address the server is listening on.
// When the configured addr was ":0" the returned
// string contains the OS-assigned port.
func (h *httpServer) Addr() string {
	if h == nil || h.ln == nil {
		return ""
	}
	return h.ln.Addr().String()
}

// buildExplorer constructs the explorer http.Handler.
// It returns nil when exp is nil so the gateway can be
// constructed without an explorer for tests.
func buildExplorer(exp *explorerAdapter, aud *audit.Logger, tunMgr *tunnel.Manager, edgeEngine memedge.Engine, edgeSvc *edge_rpc.Service) http.Handler {
	if exp == nil {
		return nil
	}
	h, err := explorerPkg.New(explorerPkg.Config{
		Backend:       exp,
		Audit:         aud,
		TunnelManager: tunMgr,
		EdgeEngine:    edgeEngine,
		EdgeService:   edgeSvc,
	})
	if err != nil {
		slog.Warn("explorer", "err", err.Error())
		return nil
	}
	return h.Handler()
}

// Close performs a graceful shutdown with a 5s budget.
func (h *httpServer) Close() {
	if h == nil || h.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = h.ShutdownCtx(ctx)
}

// ShutdownCtx performs a graceful shutdown bounded by the
// provided context's deadline. The daemon's main() calls this
// in sequence to drain HTTP traffic before tearing down the
// rest of the subsystems.
func (h *httpServer) ShutdownCtx(ctx context.Context) error {
	if h == nil || h.srv == nil {
		return nil
	}
	if h.onClose != nil {
		_ = h.onClose(ctx)
	}
	// Try graceful shutdown with a short timeout first (e.g. 500ms)
	// to allow normal active requests to complete, then force close
	// hijacked connections (like WebSockets) which never exit on shutdown.
	sdCtx, sdCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer sdCancel()
	err := h.srv.Shutdown(sdCtx)
	if err != nil {
		_ = h.srv.Close()
		return err
	}
	return nil
}

// ensureGeoIPDatabase checks if the GeoLite2-City.mmdb database exists at
// any of the checked paths, and if missing, attempts to download it from the
// public mirror to the data directory.
func ensureGeoIPDatabase(cfg *config.Config, logger *slog.Logger) (string, error) {
	if !cfg.EnableGeolocation {
		return "", nil
	}

	// 1. Check existing locations
	var pathsToCheck []string
	if cfg.GeolocationDB != "" {
		pathsToCheck = append(pathsToCheck, cfg.GeolocationDB)
	}
	// Add platform-specific system paths
	if pd := os.Getenv("ProgramData"); pd != "" {
		pathsToCheck = append(pathsToCheck, filepath.Join(pd, "Membuss", "GeoLite2-City.mmdb"))
	} else {
		pathsToCheck = append(pathsToCheck, filepath.Join("/etc/membuss", "GeoLite2-City.mmdb"))
	}
	pathsToCheck = append(pathsToCheck, filepath.Join(cfg.DataDir, "GeoLite2-City.mmdb"))

	for _, p := range pathsToCheck {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// 2. Database is missing, download it
	destPath := filepath.Join(cfg.DataDir, "GeoLite2-City.mmdb")
	logger.Info("geo: database missing, downloading...", "dest", destPath)

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}

	// Download to a temporary file in the same directory to allow atomic rename
	tmpPath := destPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		out.Close()
		os.Remove(tmpPath) // safe to call if renamed or already closed
	}()

	url := "https://github.com/P3TERX/GeoLite.mmdb/releases/latest/download/GeoLite2-City.mmdb"

	// Create client with timeout
	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read with progress logging
	totalBytes := resp.ContentLength
	var downloaded int64
	buffer := make([]byte, 32*1024)
	lastLog := time.Now()

	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := out.Write(buffer[:n]); writeErr != nil {
				return "", fmt.Errorf("write temp file: %w", writeErr)
			}
			downloaded += int64(n)

			// Log progress at most once every 3 seconds
			if time.Since(lastLog) > 3*time.Second {
				if totalBytes > 0 {
					pct := (float64(downloaded) / float64(totalBytes)) * 100
					logger.Info("geo: downloading database", "progress", fmt.Sprintf("%.1f%%", pct), "downloaded_bytes", downloaded, "total_bytes", totalBytes)
				} else {
					logger.Info("geo: downloading database", "downloaded_bytes", downloaded)
				}
				lastLog = time.Now()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return "", fmt.Errorf("read response body: %w", readErr)
		}
	}

	// Close file explicitly before renaming
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", fmt.Errorf("rename temp file: %w", err)
	}

	logger.Info("geo: database downloaded successfully", "path", destPath)
	return destPath, nil
}

type heraldStoreWrapper struct {
	store.Store
}

func (h heraldStoreWrapper) AllSealed() ([]mid.MID, error) {
	if h.Store == nil {
		return nil, nil
	}
	sealed, err := h.Store.AllSealed()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(sealed))
	out := make([]mid.MID, 0, len(sealed))
	for _, m := range sealed {
		seen[m.String()] = struct{}{}
		out = append(out, m)
	}
	if objMIDs, oerr := h.Store.AllObjectMIDs(); oerr == nil {
		for _, m := range objMIDs {
			if _, ok := seen[m.String()]; !ok {
				seen[m.String()] = struct{}{}
				out = append(out, m)
			}
		}
	}
	return out, nil
}

func (h heraldStoreWrapper) IterateSealed(fn func(mid.MID) error) error {
	mids, err := h.AllSealed()
	if err != nil {
		return err
	}
	for _, m := range mids {
		if err := fn(m); err != nil {
			return err
		}
	}
	return nil
}

type dhtStoreWrapper struct {
	ds *db.PebbleDatastore
}

func (w dhtStoreWrapper) Get(key []byte) ([]byte, error) {
	if w.ds == nil {
		return nil, db.ErrNotFound
	}
	val, err := w.ds.Get(context.Background(), datastore.NewKey(string(key)))
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return val, nil
}

func (w dhtStoreWrapper) Set(key, value []byte) error {
	if w.ds == nil {
		return nil
	}
	return w.ds.Put(context.Background(), datastore.NewKey(string(key)), value)
}
