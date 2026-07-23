package echoinsoector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	"github.com/nnlgsakib/membuss/pkg/plugin"
)

// EventRecord captures detailed metadata about intercepted core events.
type EventRecord struct {
	Type      string `json:"type"`
	MID       string `json:"mid,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	Details   string `json:"details,omitempty"`
	Timestamp string `json:"timestamp"`
}

// InspectorStats summarizes plugin hook counters.
type InspectorStats struct {
	Status              string `json:"status"`
	BlockPuts           int64  `json:"block_puts"`
	BlockGets           int64  `json:"block_gets"`
	BlockDels           int64  `json:"block_dels"`
	AnchorHolds         int64  `json:"anchor_holds"`
	AnchorSeals         int64  `json:"anchor_seals"`
	PeerConnects        int64  `json:"peer_connects"`
	PeerDisconnects     int64  `json:"peer_disconnects"`
	P2PStreamsHandled   int64  `json:"p2p_streams_handled"`
	TotalBytesProcessed int64  `json:"total_bytes_processed"`
	EventsLogged        int    `json:"events_logged"`
}

// EchoInspectorPlugin is a comprehensive reference plugin demonstrating zero-core-modification extensions.
type EchoInspectorPlugin struct {
	mu                 sync.RWMutex
	core               *plugin.Core
	blockPuts          int64
	blockGets          int64
	blockDels          int64
	anchorHolds        int64
	anchorSeals        int64
	peerConnects       int64
	peerDisconnects    int64
	p2pStreamsHandled  int64
	totalBytes         int64
	recentEvents       []EventRecord
	stopChan           chan struct{}
}

func New() plugin.Plugin {
	return &EchoInspectorPlugin{
		recentEvents: make([]EventRecord, 0, 100),
		stopChan:     make(chan struct{}),
	}
}

func (p *EchoInspectorPlugin) Name() string {
	return "echo-inspector"
}

func (p *EchoInspectorPlugin) Register(core *plugin.Core) error {
	p.core = core

	// 1. Universal Subsystem Hooks
	if core.Hooks != nil {
		// BeforeBlockPut Hook: intercepts block before storage write
		core.Hooks.BeforeBlockPut = append(core.Hooks.BeforeBlockPut, func(ctx context.Context, blk *store.Block) (*store.Block, error) {
			p.recordEvent("block.before_put", blk.MID.String(), int64(len(blk.Data)), "intercepted before persistence")
			if core.Logger != nil {
				core.Logger.Info("[Inspector Hook] BeforeBlockPut", "mid", blk.MID.String(), "bytes", len(blk.Data))
			}
			// Demonstrate payload annotation for test blocks
			if strings.HasPrefix(string(blk.Data), "[test-hook]") {
				annotated := append([]byte(nil), blk.Data...)
				annotated = append(annotated, []byte(" [intercepted-by-plugin]")...)
				return &store.Block{MID: blk.MID, Data: annotated}, nil
			}
			return blk, nil
		})

		// AfterBlockPut Hook
		core.Hooks.AfterBlockPut = append(core.Hooks.AfterBlockPut, func(ctx context.Context, m mid.MID, size int64) {
			p.mu.Lock()
			p.blockPuts++
			p.totalBytes += size
			p.mu.Unlock()
			p.recordEvent("block.after_put", m.String(), size, "successfully stored")
		})

		// BeforeBlockGet Hook
		core.Hooks.BeforeBlockGet = append(core.Hooks.BeforeBlockGet, func(ctx context.Context, targetMID mid.MID) (mid.MID, error) {
			p.recordEvent("block.before_get", targetMID.String(), 0, "intercepted before read")
			return targetMID, nil
		})

		// AfterBlockGet Hook
		core.Hooks.AfterBlockGet = append(core.Hooks.AfterBlockGet, func(ctx context.Context, m mid.MID, data []byte) ([]byte, error) {
			p.mu.Lock()
			p.blockGets++
			p.mu.Unlock()
			p.recordEvent("block.after_get", m.String(), int64(len(data)), "successfully retrieved")
			return data, nil
		})

		// AfterBlockDel Hook
		core.Hooks.AfterBlockDel = append(core.Hooks.AfterBlockDel, func(ctx context.Context, targetMID mid.MID) {
			p.mu.Lock()
			p.blockDels++
			p.mu.Unlock()
			p.recordEvent("block.del", targetMID.String(), 0, "deleted block")
		})

		// Anchor Hooks
		core.Hooks.OnAnchorHold = append(core.Hooks.OnAnchorHold, func(ctx context.Context, rootMID mid.MID, bytes int64) error {
			p.mu.Lock()
			p.anchorHolds++
			p.mu.Unlock()
			p.recordEvent("anchor.hold", rootMID.String(), bytes, "anchor hold registered")
			return nil
		})

		core.Hooks.OnAnchorSeal = append(core.Hooks.OnAnchorSeal, func(ctx context.Context, rootMID mid.MID, shards int) error {
			p.mu.Lock()
			p.anchorSeals++
			p.mu.Unlock()
			p.recordEvent("anchor.seal", rootMID.String(), 0, fmt.Sprintf("erasure sealed (%d shards)", shards))
			return nil
		})

		// Network Peer Hooks
		core.Hooks.OnPeerConnected = append(core.Hooks.OnPeerConnected, func(peerID peer.ID) {
			p.mu.Lock()
			p.peerConnects++
			p.mu.Unlock()
			p.recordEvent("peer.connect", "", 0, peerID.String())
		})

		core.Hooks.OnPeerDisconnected = append(core.Hooks.OnPeerDisconnected, func(peerID peer.ID) {
			p.mu.Lock()
			p.peerDisconnects++
			p.mu.Unlock()
			p.recordEvent("peer.disconnect", "", 0, peerID.String())
		})
	}

	// 2. Custom libp2p Stream Protocol Registration (/membuss/inspector/ping/1.0.0)
	if core.Host != nil {
		core.Host.SetStreamHandler("/membuss/inspector/ping/1.0.0", func(s network.Stream) {
			p.mu.Lock()
			p.p2pStreamsHandled++
			p.mu.Unlock()

			defer s.Close()
			buf := make([]byte, 64)
			n, _ := s.Read(buf)
			p.recordEvent("p2p.stream", "", int64(n), string(buf[:n]))
			_, _ = s.Write([]byte("PONG [EchoInspector]\n"))
		})
	}

	// 3. HTTP REST Endpoints (Mounted on Gateway & Local Node API)
	if core.GatewayHTTP != nil {
		core.GatewayHTTP.HandleFunc("GET", "/gateway/inspector/status", p.handleStatus)
		core.GatewayHTTP.HandleFunc("GET", "/gateway/inspector/stats", p.handleStats)
		core.GatewayHTTP.HandleFunc("GET", "/gateway/inspector/events", p.handleEvents)
		core.GatewayHTTP.HandleFunc("GET", "/gateway/inspector/config", p.handleConfig)
	}
	if core.NodeHTTP != nil {
		core.NodeHTTP.HandleFunc("GET", "/api/v1/inspector/status", p.handleStatus)
		core.NodeHTTP.HandleFunc("GET", "/api/v1/inspector/stats", p.handleStats)
		core.NodeHTTP.HandleFunc("GET", "/api/v1/inspector/events", p.handleEvents)
		core.NodeHTTP.HandleFunc("GET", "/api/v1/inspector/config", p.handleConfig)
		core.NodeHTTP.HandleFunc("POST", "/api/v1/inspector/test-hook", p.handleTestHook)
	}

	// 4. CLI Subcommands Suite (Query live daemon over HTTP)
	if core.CLIRegistry != nil {
		core.CLIRegistry.RegisterCommand("inspector", "Inspect plugin system state & live daemon hook telemetry", plugin.CLICommand{
			Name:        "inspector",
			Usage:       "membuss inspector <subcommand>",
			Description: "Displays active plugin system status, hook events, and metrics",
			SubCommands: []plugin.CLICommand{
				{
					Name:        "status",
					Usage:       "membuss inspector status",
					Description: "Displays active plugin health and status from running daemon",
					Run: func(args []string) error {
						var res struct {
							OK     bool   `json:"ok"`
							Plugin string `json:"plugin"`
							Status string `json:"status"`
						}
						if err := p.fetchInspectorAPI("GET", "/api/v1/inspector/status", nil, &res); err != nil {
							fmt.Println("[Plugin System] Inspector Plugin Loaded (Daemon offline or API not reached)")
							fmt.Println("  Plugin:  echo-inspector")
							fmt.Println("  Status:  active (local mode)")
							return nil
						}
						fmt.Println("[Plugin System] Live Daemon Inspector Active")
						fmt.Printf("  Plugin: %s\n", res.Plugin)
						fmt.Printf("  Status: %s (Daemon Online)\n", res.Status)
						return nil
					},
				},
				{
					Name:        "stats",
					Usage:       "membuss inspector stats",
					Description: "Prints live summary of intercepted core events and metrics from running daemon",
					Run: func(args []string) error {
						var res struct {
							OK    bool           `json:"ok"`
							Stats InspectorStats `json:"stats"`
						}
						if err := p.fetchInspectorAPI("GET", "/api/v1/inspector/stats", nil, &res); err != nil {
							fmt.Printf("[Inspector Error] Could not connect to running daemon API: %v\n", err)
							fmt.Println("Ensure the Membuss daemon is running (membuss daemon start)")
							return nil
						}
						stats := res.Stats
						fmt.Println("=== Membuss Plugin Inspector Live Daemon Metrics ===")
						fmt.Printf("  Status:               %s\n", stats.Status)
						fmt.Printf("  Block Puts:           %d\n", stats.BlockPuts)
						fmt.Printf("  Block Gets:           %d\n", stats.BlockGets)
						fmt.Printf("  Block Deletions:      %d\n", stats.BlockDels)
						fmt.Printf("  Anchor Holds:         %d\n", stats.AnchorHolds)
						fmt.Printf("  Anchor Seals:         %d\n", stats.AnchorSeals)
						fmt.Printf("  Peer Connections:     %d\n", stats.PeerConnects)
						fmt.Printf("  P2P Streams Handled:  %d\n", stats.P2PStreamsHandled)
						fmt.Printf("  Bytes Processed:      %d bytes\n", stats.TotalBytesProcessed)
						fmt.Printf("  Logged Event Count:   %d\n", stats.EventsLogged)
						return nil
					},
				},
				{
					Name:        "events",
					Usage:       "membuss inspector events",
					Description: "Displays live log of core events intercepted by daemon plugin hooks",
					Run: func(args []string) error {
						var res struct {
							OK     bool          `json:"ok"`
							Events []EventRecord `json:"events"`
						}
						if err := p.fetchInspectorAPI("GET", "/api/v1/inspector/events", nil, &res); err != nil {
							fmt.Printf("[Inspector Error] Could not connect to running daemon API: %v\n", err)
							fmt.Println("Ensure the Membuss daemon is running (membuss daemon start)")
							return nil
						}
						fmt.Println("=== Live Intercepted Core Events (Daemon) ===")
						if len(res.Events) == 0 {
							fmt.Println("  (No events recorded on daemon yet)")
							return nil
						}
						for i, ev := range res.Events {
							fmt.Printf("  [%d] %s | Type: %-16s | MID: %-58s | Bytes: %d | Details: %s\n",
								i+1, ev.Timestamp, ev.Type, ev.MID, ev.Bytes, ev.Details)
						}
						return nil
					},
				},
				{
					Name:        "config",
					Usage:       "membuss inspector config",
					Description: "Displays plugin-specific raw configuration parsed from membuss.yaml",
					Run: func(args []string) error {
						var res struct {
							OK     bool           `json:"ok"`
							Config map[string]any `json:"config"`
						}
						if err := p.fetchInspectorAPI("GET", "/api/v1/inspector/config", nil, &res); err != nil {
							fmt.Printf("[Inspector Error] Could not connect to running daemon API: %v\n", err)
							return nil
						}
						fmt.Println("=== Plugin Raw Configuration (membuss.yaml) ===")
						data, _ := json.MarshalIndent(res.Config, "  ", "  ")
						fmt.Printf("  %s\n", string(data))
						return nil
					},
				},
				{
					Name:        "test-hook",
					Usage:       "membuss inspector test-hook",
					Description: "Triggers a live end-to-end storage write & read test to execute and verify storage hooks",
					Run: func(args []string) error {
						var res struct {
							OK           bool   `json:"ok"`
							TestMID      string `json:"test_mid"`
							Written      string `json:"written_data"`
							Retrieved    string `json:"retrieved_data"`
							Interception string `json:"interception_status"`
						}
						if err := p.fetchInspectorAPI("POST", "/api/v1/inspector/test-hook", nil, &res); err != nil {
							fmt.Printf("[Test Hook Error] Daemon API failed: %v\n", err)
							return nil
						}
						fmt.Println("=== Live Hook Execution Test Results ===")
						fmt.Printf("  Test MID:             %s\n", res.TestMID)
						fmt.Printf("  Payload Sent:         %s\n", res.Written)
						fmt.Printf("  Retrieved Payload:    %s\n", res.Retrieved)
						fmt.Printf("  Interception Status:  %s\n", res.Interception)
						return nil
					},
				},
			},
		})
	}

	if core.Logger != nil {
		core.Logger.Info("Echo Inspector Plugin registered successfully!")
	}
	return nil
}

func (p *EchoInspectorPlugin) Start(ctx context.Context) error {
	if p.core != nil && p.core.Logger != nil {
		p.core.Logger.Info("Echo Inspector background worker started")
	}

	// Start background ticker loop to showcase worker tasks
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-p.stopChan:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if p.core != nil && p.core.Logger != nil {
					stats := p.getStats()
					p.core.Logger.Info("[Inspector Heartbeat]",
						"block_puts", stats.BlockPuts,
						"block_gets", stats.BlockGets,
						"events_count", stats.EventsLogged,
					)
				}
			}
		}
	}()

	return nil
}

func (p *EchoInspectorPlugin) Stop(ctx context.Context) error {
	close(p.stopChan)
	if p.core != nil && p.core.Logger != nil {
		p.core.Logger.Info("Echo Inspector background worker stopped")
	}
	return nil
}

func (p *EchoInspectorPlugin) recordEvent(eventType, mStr string, size int64, details string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	rec := EventRecord{
		Type:      eventType,
		MID:       mStr,
		Bytes:     size,
		Details:   details,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if len(p.recentEvents) >= 100 {
		p.recentEvents = p.recentEvents[1:]
	}
	p.recentEvents = append(p.recentEvents, rec)
}

func (p *EchoInspectorPlugin) getStats() InspectorStats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return InspectorStats{
		Status:              "active",
		BlockPuts:           p.blockPuts,
		BlockGets:           p.blockGets,
		BlockDels:           p.blockDels,
		AnchorHolds:         p.anchorHolds,
		AnchorSeals:         p.anchorSeals,
		PeerConnects:        p.peerConnects,
		PeerDisconnects:     p.peerDisconnects,
		P2PStreamsHandled:   p.p2pStreamsHandled,
		TotalBytesProcessed: p.totalBytes,
		EventsLogged:        len(p.recentEvents),
	}
}

func (p *EchoInspectorPlugin) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"ok": true, "plugin": "%s", "status": "active"}`, p.Name())
}

func (p *EchoInspectorPlugin) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	stats := p.getStats()
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "stats": stats})
}

func (p *EchoInspectorPlugin) handleEvents(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	events := append([]EventRecord(nil), p.recentEvents...)
	p.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "events": events})
}

func (p *EchoInspectorPlugin) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	cfg := make(map[string]any)
	if p.core != nil && p.core.RawConfig != nil {
		cfg = p.core.RawConfig
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "config": cfg})
}

func (p *EchoInspectorPlugin) handleTestHook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if p.core == nil || p.core.Store == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "store not available"})
		return
	}

	testData := []byte(fmt.Sprintf("[test-hook] Live Hook Verification Payload %d", time.Now().UnixNano()))
	testMID := mid.FromBytes(testData)

	// Execute Store Put (triggers BeforeBlockPut & AfterBlockPut hooks)
	if err := p.core.Store.Put(testMID, testData); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": fmt.Sprintf("put failed: %v", err)})
		return
	}

	// Execute Store Get (triggers BeforeBlockGet & AfterBlockGet hooks)
	retrieved, err := p.core.Store.Get(testMID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": fmt.Sprintf("get failed: %v", err)})
		return
	}

	interceptionStatus := "FAILED"
	if strings.Contains(string(retrieved), "[intercepted-by-plugin]") {
		interceptionStatus = "PASSED - BeforeBlockPut mutated and annotated payload successfully!"
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":                  true,
		"test_mid":            testMID.String(),
		"written_data":        string(testData),
		"retrieved_data":      string(retrieved),
		"interception_status": interceptionStatus,
	})
}

func (p *EchoInspectorPlugin) fetchInspectorAPI(method, endpoint string, body io.Reader, target any) error {
	base := "http://127.0.0.1:5001"
	if p != nil && p.core != nil {
		base = p.core.HTTPBase()
	} else if v := os.Getenv("MEMBUSS_API_ADDR"); v != "" {
		base = v
		if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			base = "http://" + base
		}
	}
	url := fmt.Sprintf("%s%s", base, endpoint)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}
