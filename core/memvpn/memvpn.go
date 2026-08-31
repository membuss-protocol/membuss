package memvpn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Service is the central MemVPN engine.
type Service struct {
	host       host.Host
	cfg        MeshConfig
	acl        *ACL
	router     *Router
	mesh       *Mesh
	wgServer   *WGServer
	natRouter  *NATRouter
	exitMgr    *ExitManager
	stats        *TrafficStats
	startTime    time.Time
	activeExit   string
	speedHistory []SpeedSample
	lastSent     int64
	lastRecv     int64
	lastRateTime time.Time
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.RWMutex
}

// NewService creates a new MemVPN service instance.
func NewService(h host.Host, cfg MeshConfig) *Service {
	if cfg.NodeName == "" && h != nil {
		idStr := h.ID().String()
		if len(idStr) > 8 {
			cfg.NodeName = idStr[:8]
		} else {
			cfg.NodeName = idStr
		}
	}
	if cfg.VirtualIP == "" && h != nil {
		cfg.VirtualIP = deriveVirtualIP(h.ID())
	}
	if cfg.WGListenPort <= 0 {
		cfg.WGListenPort = DefaultWGPort
	}

	acl := NewACL(&cfg)
	stats := &TrafficStats{}

	var r *Router
	var m *Mesh
	var em *ExitManager

	if h != nil {
		r = NewRouter(h)
		m = NewMesh(h, &cfg, acl, r, stats)
		em = NewExitManager(h, acl, stats)
		em.SetPolicy(ExitPolicy{
			AllowAll:        cfg.ExitAllowAll,
			AllowedPeers:    cfg.ExitAllowedPeers,
			BlockPrivateIPs: true,
		})
	}

	wgSrv, _ := NewWGServer(cfg.WGListenPort, stats, cfg.DataDir)
	var natR *NATRouter
	if wgSrv != nil {
		natR = NewNATRouter(wgSrv, stats, nil)
		natR.SetActiveExit(cfg.SelectedExit)
		wgSrv.SetNATRouter(natR)
	}

	ctx, cancel := context.WithCancel(context.Background())

	svc := &Service{
		host:         h,
		cfg:          cfg,
		acl:          acl,
		router:       r,
		mesh:         m,
		wgServer:     wgSrv,
		natRouter:    natR,
		exitMgr:      em,
		stats:        stats,
		activeExit:   cfg.SelectedExit,
		speedHistory: make([]SpeedSample, 0, 30),
		lastRateTime: time.Now(),
		startTime:    time.Now(),
		ctx:          ctx,
		cancel:       cancel,
	}

	if natR != nil {
		natR.SetDialer(svc)
	}

	return svc
}

// Start activates WireGuard server, mesh stream listeners, and exit node handlers.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.mesh != nil {
		s.mesh.Start()
	}

	if s.host != nil && s.exitMgr != nil {
		s.host.SetStreamHandler(ExitProtocolID, s.exitMgr.HandleExitStream)
	}

	if s.wgServer != nil {
		if err := s.wgServer.Start(); err != nil {
			return fmt.Errorf("start wireguard server: %w", err)
		}
		// Create default client device profile
		_, _ = s.wgServer.GetProfile("default")
	}

	go s.rateTrackerLoop()

	return nil
}

func (s *Service) rateTrackerLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			s.mu.Lock()
			currentSent := atomic.LoadInt64(&s.stats.ClientBytesSent) + atomic.LoadInt64(&s.stats.ContributedBytesSent) + atomic.LoadInt64(&s.stats.BytesSent)
			currentRecv := atomic.LoadInt64(&s.stats.ClientBytesRecv) + atomic.LoadInt64(&s.stats.ContributedBytesRecv) + atomic.LoadInt64(&s.stats.BytesRecv)

			dt := now.Sub(s.lastRateTime).Seconds()
			if dt <= 0 {
				dt = 1
			}

			upBps := int64(float64(currentSent-s.lastSent) / dt)
			if upBps < 0 {
				upBps = 0
			}
			downBps := int64(float64(currentRecv-s.lastRecv) / dt)
			if downBps < 0 {
				downBps = 0
			}

			s.lastSent = currentSent
			s.lastRecv = currentRecv
			s.lastRateTime = now

			s.stats.CurrentUploadBps = upBps
			s.stats.CurrentDownloadBps = downBps

			totalContrib := atomic.LoadInt64(&s.stats.ContributedBytesSent) + atomic.LoadInt64(&s.stats.ContributedBytesRecv)
			totalConsumed := atomic.LoadInt64(&s.stats.ClientBytesSent) + atomic.LoadInt64(&s.stats.ClientBytesRecv)
			if totalConsumed > 0 {
				s.stats.ContributionRatio = float64(totalContrib) / float64(totalConsumed)
			} else if totalContrib > 0 {
				s.stats.ContributionRatio = 1.0
			} else {
				s.stats.ContributionRatio = 0.0
			}

			sample := SpeedSample{
				Timestamp:   now,
				UploadBps:   upBps,
				DownloadBps: downBps,
			}

			if len(s.speedHistory) >= 30 {
				s.speedHistory = append(s.speedHistory[1:], sample)
			} else {
				s.speedHistory = append(s.speedHistory, sample)
			}
			s.mu.Unlock()
		}
	}
}

// Stop shuts down the WireGuard server, NAT router, and mesh forwarders.
func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.natRouter != nil {
		s.natRouter.Stop()
	}
	if s.mesh != nil {
		s.mesh.Stop()
	}
	if s.wgServer != nil {
		s.wgServer.Stop()
	}
	return nil
}

// AddWireGuardDevice registers a new device and returns its .conf and QR profile.
func (s *Service) AddWireGuardDevice(name string) (*WGProfile, error) {
	s.mu.RLock()
	wg := s.wgServer
	s.mu.RUnlock()

	if wg == nil {
		return nil, errors.New("wireguard server not available")
	}
	return wg.AddDevice(name)
}

// GetWireGuardProfile retrieves configuration for an existing device.
func (s *Service) GetWireGuardProfile(name string) (*WGProfile, error) {
	s.mu.RLock()
	wg := s.wgServer
	s.mu.RUnlock()

	if wg == nil {
		return nil, errors.New("wireguard server not available")
	}
	return wg.GetProfile(name)
}

// ListWireGuardDevices lists all registered client devices.
func (s *Service) ListWireGuardDevices() []WGDevice {
	s.mu.RLock()
	wg := s.wgServer
	s.mu.RUnlock()

	if wg == nil {
		return nil
	}
	return wg.ListDevices()
}

// DeleteWireGuardDevice removes a device by ID, name, or public key.
func (s *Service) DeleteWireGuardDevice(idOrPubKey string) error {
	s.mu.RLock()
	wg := s.wgServer
	s.mu.RUnlock()

	if wg == nil {
		return errors.New("wireguard server not available")
	}
	return wg.DeleteDevice(idOrPubKey)
}

// ToggleExitNode enables or disables exit node provider mode on this host.
func (s *Service) ToggleExitNode(enabled, allowAll bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cfg.IsExitNode = enabled
	s.cfg.ExitAllowAll = allowAll
	if s.mesh != nil {
		s.mesh.cfg.IsExitNode = enabled
		s.mesh.cfg.ExitAllowAll = allowAll
	}
	if s.exitMgr != nil {
		s.exitMgr.SetPolicy(ExitPolicy{
			AllowAll:        allowAll,
			AllowedPeers:    s.cfg.ExitAllowedPeers,
			BlockPrivateIPs: true,
		})
	}
	return nil
}

// SelectExitNode sets a designated peer as the exit gateway or enables 'auto' swarm mode.
func (s *Service) SelectExitNode(peerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeExit = peerID
	if s.natRouter != nil {
		s.natRouter.SetActiveExit(peerID)
	}
	return nil
}

// GetExitNodes returns telemetry for all known exit nodes in the mesh.
func (s *Service) GetExitNodes() []ExitNodeInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	exitNodes := make([]ExitNodeInfo, 0)
	if s.mesh == nil {
		return exitNodes
	}

	peers := s.mesh.GetPeers()
	for _, p := range peers {
		if p.IsExitNode {
			exitNodes = append(exitNodes, ExitNodeInfo{
				PeerID:      p.PeerID,
				NodeName:    p.NodeName,
				VirtualIP:   p.VirtualIP,
				LatencyMs:   p.LatencyMs,
				Available:   p.Connected,
				Selected:    s.activeExit == p.PeerID || (s.activeExit == "auto" && p.Connected),
			})
		}
	}
	return exitNodes
}

// ExposeService publishes a local port to the mesh.
func (s *Service) ExposeService(name, targetAddr, description string, allowedPeers []string) (*ExposedService, error) {
	if s.mesh == nil {
		return nil, errors.New("mesh not initialized")
	}
	return s.mesh.ExposeService(name, targetAddr, description, allowedPeers)
}

// UnexposeService stops exposing a service.
func (s *Service) UnexposeService(name string) error {
	if s.mesh == nil {
		return errors.New("mesh not initialized")
	}
	return s.mesh.UnexposeService(name)
}

// ForwardService binds a local port to a remote peer's service.
func (s *Service) ForwardService(ctx context.Context, localAddr string, remotePeer peer.ID, remoteService string) (*PortForward, error) {
	if s.mesh == nil {
		return nil, errors.New("mesh not initialized")
	}
	return s.mesh.ForwardService(ctx, localAddr, remotePeer, remoteService)
}

// UnforwardService removes a local port forward.
func (s *Service) UnforwardService(localAddr string) error {
	if s.mesh == nil {
		return errors.New("mesh not initialized")
	}
	return s.mesh.UnforwardService(localAddr)
}

// DialServiceStream connects directly to a remote peer's service.
func (s *Service) DialServiceStream(ctx context.Context, remotePeer peer.ID, serviceName string) (net.Conn, error) {
	if s.router == nil {
		return nil, errors.New("router not initialized")
	}
	return s.router.DialService(ctx, remotePeer, serviceName)
}

// DialExitTarget dials an outbound target host:port via the active Exit Node or direct outbound.
func (s *Service) DialExitTarget(ctx context.Context, targetHost string, targetPort int) (net.Conn, error) {
	s.mu.RLock()
	exitTarget := s.activeExit
	em := s.exitMgr
	s.mu.RUnlock()

	// If no exit target configured, dial directly
	if exitTarget == "" {
		return net.DialTimeout("tcp", fmt.Sprintf("%s:%d", targetHost, targetPort), 10*time.Second)
	}

	if em == nil {
		return nil, errors.New("exit manager not initialized")
	}

	// Auto-select lowest latency exit node if set to "auto"
	var selectedPeer peer.ID
	if exitTarget == "auto" {
		exitNodes := s.GetExitNodes()
		if len(exitNodes) == 0 {
			// Fallback to direct dial if no exit nodes online
			return net.DialTimeout("tcp", fmt.Sprintf("%s:%d", targetHost, targetPort), 10*time.Second)
		}
		pID, err := peer.Decode(exitNodes[0].PeerID)
		if err != nil {
			return nil, fmt.Errorf("invalid exit node peer id: %w", err)
		}
		selectedPeer = pID
	} else {
		pID, err := peer.Decode(exitTarget)
		if err != nil {
			return nil, fmt.Errorf("invalid designated exit peer id %q: %w", exitTarget, err)
		}
		selectedPeer = pID
	}

	return em.DialExit(ctx, selectedPeer, targetHost, targetPort)
}

// GetStatus compiles complete telemetry and WireGuard state.
func (s *Service) GetStatus() MeshStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var wgPort int
	var wgPubKey string
	var wgEndpoint string
	var wgDeviceCount int

	if s.wgServer != nil {
		wgPort = s.wgServer.Port()
		wgPubKey = s.wgServer.PublicKey()
		wgEndpoint = s.wgServer.Endpoint()
		wgDeviceCount = len(s.wgServer.ListDevices())
	}

	var peerList []PeerInfo
	var svcList []ExposedService
	var fwdList []PortForward

	if s.mesh != nil {
		peerList = s.mesh.GetPeers()
		svcList = s.mesh.GetServices()
		fwdList = s.mesh.GetForwards()
	}

	return MeshStatus{
		Enabled:          true,
		MeshID:           s.cfg.MeshID,
		NodeName:         s.cfg.NodeName,
		VirtualIP:        s.cfg.VirtualIP,
		WGServerPort:     wgPort,
		WGServerPubKey:   wgPubKey,
		WGServerEndpoint: wgEndpoint,
		WGDevicesCount:   wgDeviceCount,
		IsExitNode:       s.cfg.IsExitNode,
		SelectedExitNode: s.activeExit,
		RoutingEnabled:   s.activeExit != "",
		PeerCount:        len(peerList),
		Peers:            peerList,
		ExitNodes:        s.GetExitNodes(),
		Services:         svcList,
		Forwards:         fwdList,
		Stats: TrafficStats{
			BytesSent:            atomic.LoadInt64(&s.stats.BytesSent),
			BytesRecv:            atomic.LoadInt64(&s.stats.BytesRecv),
			ContributedBytesSent: atomic.LoadInt64(&s.stats.ContributedBytesSent),
			ContributedBytesRecv: atomic.LoadInt64(&s.stats.ContributedBytesRecv),
			ContributedConns:     atomic.LoadInt64(&s.stats.ContributedConns),
			ClientBytesSent:      atomic.LoadInt64(&s.stats.ClientBytesSent),
			ClientBytesRecv:      atomic.LoadInt64(&s.stats.ClientBytesRecv),
			ClientConns:          atomic.LoadInt64(&s.stats.ClientConns),
			DNSQueriesCount:      atomic.LoadInt64(&s.stats.DNSQueriesCount),
			TCPConnsCount:        atomic.LoadInt64(&s.stats.TCPConnsCount),
			UDPFlowsCount:        atomic.LoadInt64(&s.stats.UDPFlowsCount),
			CurrentUploadBps:     s.stats.CurrentUploadBps,
			CurrentDownloadBps:   s.stats.CurrentDownloadBps,
			ContributionRatio:    s.stats.ContributionRatio,
			SpeedHistory:         s.speedHistory,
		},
		UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
	}
}
