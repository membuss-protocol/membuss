package plugin

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

var (
	globalMu        sync.RWMutex
	registered      = make(map[string]Plugin)
	activeInstances = make([]Plugin, 0)
)

// Register registers a plugin into the global plugin registry.
func Register(p Plugin) {
	globalMu.Lock()
	defer globalMu.Unlock()

	name := p.Name()
	if _, exists := registered[name]; exists {
		panic(fmt.Sprintf("plugin: duplicate plugin registration for %q", name))
	}
	registered[name] = p
}

// GetRegistered returns all registered plugins.
func GetRegistered() map[string]Plugin {
	globalMu.RLock()
	defer globalMu.RUnlock()

	cp := make(map[string]Plugin, len(registered))
	for k, v := range registered {
		cp[k] = v
	}
	return cp
}

// BootPlugins initializes all active plugins defined in system configuration.
func BootPlugins(core *Core) error {
	globalMu.Lock()
	defer globalMu.Unlock()

	activeInstances = nil
	if core.Config == nil || !core.Config.Plugins.Enabled {
		core.Logger.Info("Plugin framework disabled or unconfigured")
		return nil
	}

	activeSet := make(map[string]bool)
	for _, name := range core.Config.Plugins.Active {
		activeSet[name] = true
	}

	for name, p := range registered {
		// Boot plugin if no active filter specified, wildcard "*", exact name match, or legacy "basic" alias.
		isActive := len(activeSet) == 0 || activeSet["*"] || activeSet[name] || (name == "echo-inspector" && activeSet["basic"])
		if !isActive {
			core.Logger.Debug("Plugin registered but not active in config", "plugin", name)
			continue
		}

		// Inject raw config for this plugin if present
		if cfg, ok := core.Config.Plugins.Config[name]; ok {
			core.RawConfig = cfg
		} else {
			core.RawConfig = nil
		}

		core.Logger.Info("Booting plugin", "plugin", name)
		if err := p.Register(core); err != nil {
			return fmt.Errorf("plugin %q failed registration: %w", name, err)
		}
		activeInstances = append(activeInstances, p)
	}

	return nil
}

// StartPlugins launches background routines for all active plugins.
func StartPlugins(ctx context.Context) error {
	globalMu.RLock()
	defer globalMu.RUnlock()

	for _, p := range activeInstances {
		if err := p.Start(ctx); err != nil {
			return fmt.Errorf("plugin %q failed start: %w", p.Name(), err)
		}
	}
	return nil
}

// StopPlugins gracefully terminates active plugins.
func StopPlugins(ctx context.Context) {
	globalMu.RLock()
	defer globalMu.RUnlock()

	for _, p := range activeInstances {
		if err := p.Stop(ctx); err != nil {
			// Log error during teardown but continue stopping remaining plugins
			fmt.Printf("Error stopping plugin %s: %v\n", p.Name(), err)
		}
	}
}

// --- HTTPRegistry Implementation ---

type MapHTTPRegistry struct {
	mu          sync.RWMutex
	Handlers    map[string]http.Handler
	Middlewares []func(http.Handler) http.Handler
}

func NewMapHTTPRegistry() *MapHTTPRegistry {
	return &MapHTTPRegistry{
		Handlers:    make(map[string]http.Handler),
		Middlewares: make([]func(http.Handler) http.Handler, 0),
	}
}

func (r *MapHTTPRegistry) HandleFunc(method, pattern string, handler http.HandlerFunc) {
	r.Handle(method, pattern, handler)
}

func (r *MapHTTPRegistry) Handle(method, pattern string, handler http.Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := method + " " + pattern
	r.Handlers[key] = handler
}

func (r *MapHTTPRegistry) Use(middlewares ...func(http.Handler) http.Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Middlewares = append(r.Middlewares, middlewares...)
}

// --- CLIRegistry Implementation ---

type MapCLIRegistry struct {
	mu       sync.RWMutex
	Commands map[string]CLICommand
}

func NewMapCLIRegistry() *MapCLIRegistry {
	return &MapCLIRegistry{
		Commands: make(map[string]CLICommand),
	}
}

func (r *MapCLIRegistry) RegisterCommand(name, description string, cmd CLICommand) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cmd.Name == "" {
		cmd.Name = name
	}
	if cmd.Description == "" {
		cmd.Description = description
	}
	r.Commands[name] = cmd
}

func (r *MapCLIRegistry) GetCommands() map[string]CLICommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := make(map[string]CLICommand, len(r.Commands))
	for k, v := range r.Commands {
		cp[k] = v
	}
	return cp
}
