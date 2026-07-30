package plugin

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/nnlgsakib/membuss/anchor"
	"github.com/nnlgsakib/membuss/config"
	"github.com/nnlgsakib/membuss/core/keyring"
	"github.com/nnlgsakib/membuss/core/memns"
	"github.com/nnlgsakib/membuss/core/store"
	"github.com/nnlgsakib/membuss/net/dht"
	"github.com/nnlgsakib/membuss/net/herald"
	"github.com/nnlgsakib/membuss/net/host"
	memex "github.com/nnlgsakib/membuss/net/memex_v2"
	"github.com/nnlgsakib/membuss/net/pex"
	"github.com/nnlgsakib/membuss/obs/metrics"
	"google.golang.org/grpc"
)

// Plugin is the universal interface that every Membuss plugin must implement.
type Plugin interface {
	// Name returns the unique string identifier for the plugin (e.g. "echo-inspector").
	Name() string

	// Register is called during daemon boot. The plugin receives the single entrypoint
	// (*Core) granting full access to all system handles, hook buses, API registries, and CLI.
	Register(core *Core) error

	// Start launches any background tasks or workers owned by the plugin.
	Start(ctx context.Context) error

	// Stop gracefully terminates plugin resources during daemon shutdown.
	Stop(ctx context.Context) error
}

// HTTPRegistry defines the interface for plugins to register custom REST API endpoints and middleware.
type HTTPRegistry interface {
	HandleFunc(method, pattern string, handler http.HandlerFunc)
	Handle(method, pattern string, handler http.Handler)
	Use(middlewares ...func(http.Handler) http.Handler)
}

// CLICommand represents a custom command exposed by a plugin.
type CLICommand struct {
	Name        string
	Usage       string
	Description string
	Run         func(args []string) error
	SubCommands []CLICommand
}

// CLIRegistry defines the interface for plugins to register custom CLI subcommands.
type CLIRegistry interface {
	RegisterCommand(name, description string, cmd CLICommand)
	GetCommands() map[string]CLICommand
}

// Core is the single entrypoint object passed to plugins upon registration.
// It provides full, unconstrained access to every core subsystem and extension registry.
type Core struct {
	// --- 1. Core System Handles (Direct Access) ---
	Config   *config.Config
	Store    store.Store
	Host     *host.Host
	DHT      *dht.MemDHT
	Memex    *memex.Engine
	PEX      *pex.PEX
	Herald   *herald.MemHerald
	Anchor   *anchor.AnchorEngine // nil if anchor mode is disabled
	MemNS    *memns.Resolver
	Keyring  *keyring.KeyRing
	Metrics  *metrics.Metrics

	// --- 2. Universal Hook Bus (Event Interceptors) ---
	Hooks *HookBus

	// --- 3. Extension Registries & IPC Handles ---
	GatewayHTTP HTTPRegistry
	NodeHTTP    HTTPRegistry
	GRPCServer  *grpc.Server
	CLIRegistry CLIRegistry
	IPCPath     string
	IPCListener net.Listener

	// --- 4. Context & Logging ---
	Logger    *slog.Logger
	RawConfig map[string]any
}

// HTTPBaseResolver allows the CLI runner to inject flag-aware API address resolution.
var HTTPBaseResolver func() string

// HTTPBase returns the resolved base URL for the local Node API (e.g. "http://127.0.0.1:5001").
func (c *Core) HTTPBase() string {
	if HTTPBaseResolver != nil {
		if base := HTTPBaseResolver(); base != "" {
			if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
				return "http://" + base
			}
			return base
		}
	}
	if c != nil && c.Config != nil && c.Config.APIAddr != "" {
		addr := c.Config.APIAddr
		if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
			return "http://" + addr
		}
		return addr
	}
	if v := os.Getenv("MEMBUSS_API_ADDR"); v != "" {
		if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
			return "http://" + v
		}
		return v
	}
	return "http://127.0.0.1:5001"
}
