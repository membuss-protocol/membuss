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
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Mesh coordinates the peer-to-peer overlay network and local port forwarding.
type Mesh struct {
	host     host.Host
	cfg      *MeshConfig
	acl      *ACL
	router   *Router
	stats    *TrafficStats
	services map[string]*ExposedService
	forwards map[string]*forwarderInstance
	peers    map[peer.ID]*PeerInfo
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.RWMutex
}

type forwarderInstance struct {
	config   PortForward
	listener net.Listener
	cancel   context.CancelFunc
}

// NewMesh constructs a new Mesh manager.
func NewMesh(h host.Host, cfg *MeshConfig, acl *ACL, router *Router, stats *TrafficStats) *Mesh {
	ctx, cancel := context.WithCancel(context.Background())
	return &Mesh{
		host:     h,
		cfg:      cfg,
		acl:      acl,
		router:   router,
		stats:    stats,
		services: make(map[string]*ExposedService),
		forwards: make(map[string]*forwarderInstance),
		peers:    make(map[peer.ID]*PeerInfo),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start registers stream handlers, network notifiers, and begins peer discovery.
func (m *Mesh) Start() {
	if m.host != nil {
		m.host.SetStreamHandler(MeshProtocolID, m.HandleStream)

		// Register libp2p network connection listener
		bundle := &network.NotifyBundle{
			ConnectedF: func(net network.Network, conn network.Conn) {
				m.onPeerConnected(conn.RemotePeer())
			},
			DisconnectedF: func(net network.Network, conn network.Conn) {
				m.onPeerDisconnected(conn.RemotePeer())
			},
		}
		m.host.Network().Notify(bundle)

		// Start periodic discovery and latency ping loop
		go m.syncLoop()
	}
}

// Stop shuts down all active forwarders and listeners.
func (m *Mesh) Stop() {
	m.cancel()
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, fw := range m.forwards {
		fw.cancel()
		if fw.listener != nil {
			_ = fw.listener.Close()
		}
	}
	m.forwards = make(map[string]*forwarderInstance)
}

// onPeerConnected is called when a libp2p connection is established with a swarm peer.
func (m *Mesh) onPeerConnected(pID peer.ID) {
	if m.host == nil || pID == m.host.ID() {
		return
	}

	vip := deriveVirtualIP(pID)
	m.router.RegisterPeer(pID, vip)

	nodeName := pID.String()
	if len(nodeName) > 8 {
		nodeName = nodeName[:8]
	}

	m.mu.Lock()
	pInfo, exists := m.peers[pID]
	if !exists {
		pInfo = &PeerInfo{
			PeerID:      pID.String(),
			NodeName:    nodeName,
			VirtualIP:   vip,
			Connected:   true,
			DirectRoute: true,
		}
		m.peers[pID] = pInfo
	} else {
		pInfo.Connected = true
	}
	m.mu.Unlock()

	// Perform handshake in background
	go m.queryPeerCapabilities(pID)
}

// onPeerDisconnected is called when a libp2p peer disconnects.
func (m *Mesh) onPeerDisconnected(pID peer.ID) {
	m.mu.Lock()
	if pInfo, exists := m.peers[pID]; exists {
		pInfo.Connected = false
	}
	m.mu.Unlock()
}

// syncLoop continuously scans connected libp2p peers and syncs their status.
func (m *Mesh) syncLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if m.host == nil {
				continue
			}

			connectedPeers := m.host.Network().Peers()
			for _, pID := range connectedPeers {
				if pID == m.host.ID() {
					continue
				}
				m.onPeerConnected(pID)
			}
		}
	}
}

// queryPeerCapabilities sends a lightweight handshake to query remote services and exit status.
func (m *Mesh) queryPeerCapabilities(pID peer.ID) {
	if m.host == nil {
		return
	}

	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()

	start := time.Now()
	stream, err := m.host.NewStream(ctx, pID, MeshProtocolID)
	if err != nil {
		return
	}
	defer stream.Close()

	latency := time.Since(start).Milliseconds()

	// Send handshake payload
	req := HandshakePayload{
		MeshID:     m.cfg.MeshID,
		NodeName:   m.cfg.NodeName,
		VirtualIP:  m.cfg.VirtualIP,
		AuthToken:  GenerateAuthToken(pID, m.cfg.MeshID, m.cfg.PreSharedKey),
		Services:   m.getExposedServiceNames(),
		IsExitNode: m.cfg.IsExitNode,
		Timestamp:  time.Now().UTC(),
	}

	if err := WriteJSON(stream, FrameHandshake, req); err != nil {
		return
	}

	frame, err := ReadFrame(stream)
	if err != nil || frame.Type != FrameHandshakeAck {
		return
	}

	var ack HandshakeAckPayload
	if err := ReadJSON(frame, &ack); err != nil || !ack.Success {
		return
	}

	m.mu.Lock()
	if pInfo, exists := m.peers[pID]; exists {
		pInfo.Connected = true
		pInfo.LatencyMs = latency
		pInfo.IsExitNode = ack.IsExitNode
		if ack.VirtualIP != "" {
			pInfo.VirtualIP = ack.VirtualIP
		}
	}
	m.mu.Unlock()
}

func (m *Mesh) getExposedServiceNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]string, 0, len(m.services))
	for name := range m.services {
		res = append(res, name)
	}
	return res
}

// HandleStream dispatches inbound service requests from peers.
func (m *Mesh) HandleStream(s network.Stream) {
	defer s.Close()

	remotePeer := s.Conn().RemotePeer()
	if !m.acl.AuthorizePeer(remotePeer) {
		return
	}

	frame, err := ReadFrame(s)
	if err != nil {
		return
	}

	switch frame.Type {
	case FrameHandshake:
		var req HandshakePayload
		if err := ReadJSON(frame, &req); err != nil {
			_ = WriteJSON(s, FrameHandshakeAck, HandshakeAckPayload{Success: false, Error: err.Error()})
			return
		}
		if err := m.acl.ValidateMeshAuth(remotePeer, req.MeshID, req.AuthToken); err != nil {
			_ = WriteJSON(s, FrameHandshakeAck, HandshakeAckPayload{Success: false, Error: err.Error()})
			return
		}

		m.router.RegisterPeer(remotePeer, req.VirtualIP)
		m.mu.Lock()
		m.peers[remotePeer] = &PeerInfo{
			PeerID:      remotePeer.String(),
			NodeName:    req.NodeName,
			VirtualIP:   req.VirtualIP,
			Connected:   true,
			Services:    req.Services,
			IsExitNode:  req.IsExitNode,
			DirectRoute: true,
		}
		m.mu.Unlock()

		_ = WriteJSON(s, FrameHandshakeAck, HandshakeAckPayload{
			Success:    true,
			VirtualIP:  m.cfg.VirtualIP,
			IsExitNode: m.cfg.IsExitNode,
			Timestamp:  time.Now().UTC(),
		})

	case FrameDialRequest:
		var req DialRequestPayload
		if err := ReadJSON(frame, &req); err != nil {
			_ = WriteJSON(s, FrameDialResponse, DialResponsePayload{Success: false, Error: err.Error()})
			return
		}

		m.mu.RLock()
		svc, exists := m.services[req.ServiceName]
		m.mu.RUnlock()

		if !exists {
			_ = WriteJSON(s, FrameDialResponse, DialResponsePayload{
				Success: false,
				Error:   fmt.Sprintf("service %q not found on this node", req.ServiceName),
			})
			return
		}

		conn, err := net.DialTimeout("tcp", svc.TargetAddr, 5*time.Second)
		if err != nil {
			_ = WriteJSON(s, FrameDialResponse, DialResponsePayload{
				Success: false,
				Error:   fmt.Sprintf("dial local target %s: %v", svc.TargetAddr, err),
			})
			return
		}
		defer conn.Close()

		if err := WriteJSON(s, FrameDialResponse, DialResponsePayload{Success: true}); err != nil {
			return
		}

		atomic.AddInt64(&svc.ActiveConns, 1)
		defer atomic.AddInt64(&svc.ActiveConns, -1)

		pipeBidirectional(s, conn, m.stats)
	}
}

// ExposeService publishes a local service to the mesh.
func (m *Mesh) ExposeService(name, targetAddr, description string, allowedPeers []string) (*ExposedService, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if name == "" || targetAddr == "" {
		return nil, errors.New("service name and target address are required")
	}

	svc := &ExposedService{
		Name:         name,
		TargetAddr:   targetAddr,
		Description:  description,
		Status:       "active",
		AllowedPeers: allowedPeers,
	}
	m.services[name] = svc
	return svc, nil
}

// UnexposeService stops exposing a local service.
func (m *Mesh) UnexposeService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.services[name]; !ok {
		return fmt.Errorf("service %q not found", name)
	}
	delete(m.services, name)
	return nil
}

// ForwardService creates a local listening port that forwards traffic to a remote peer's service.
func (m *Mesh) ForwardService(ctx context.Context, localAddr string, remotePeer peer.ID, remoteService string) (*PortForward, error) {
	ln, err := net.Listen("tcp", localAddr)
	if err != nil {
		return nil, fmt.Errorf("listen local %s: %w", localAddr, err)
	}

	fwCtx, cancel := context.WithCancel(ctx)
	boundAddr := ln.Addr().String()

	pf := PortForward{
		LocalAddr:     boundAddr,
		RemotePeerID:  remotePeer.String(),
		RemoteService: remoteService,
		Status:        "active",
	}

	inst := &forwarderInstance{
		config:   pf,
		listener: ln,
		cancel:   cancel,
	}

	m.mu.Lock()
	m.forwards[boundAddr] = inst
	m.mu.Unlock()

	go m.serveForwarder(fwCtx, inst, remotePeer, remoteService)
	return &pf, nil
}

// UnforwardService shuts down an active local port forwarder.
func (m *Mesh) UnforwardService(localAddr string) error {
	m.mu.Lock()
	inst, ok := m.forwards[localAddr]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("forwarder on %s not found", localAddr)
	}
	delete(m.forwards, localAddr)
	m.mu.Unlock()

	inst.cancel()
	if inst.listener != nil {
		_ = inst.listener.Close()
	}
	return nil
}

// serveForwarder accepts connections on the local forwarder and dials the remote mesh stream.
func (m *Mesh) serveForwarder(ctx context.Context, inst *forwarderInstance, remotePeer peer.ID, serviceName string) {
	defer inst.listener.Close()

	for {
		conn, err := inst.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}

		go func(c net.Conn) {
			defer c.Close()
			streamConn, err := m.router.DialService(ctx, remotePeer, serviceName)
			if err != nil {
				return
			}
			defer streamConn.Close()

			atomic.AddInt64(&inst.config.ActiveConns, 1)
			defer atomic.AddInt64(&inst.config.ActiveConns, -1)

			pipeBidirectional(streamConn, c, m.stats)
		}(conn)
	}
}

// GetServices returns all exposed services.
func (m *Mesh) GetServices() []ExposedService {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]ExposedService, 0, len(m.services))
	for _, s := range m.services {
		res = append(res, *s)
	}
	return res
}

// GetForwards returns all active port forwarders.
func (m *Mesh) GetForwards() []PortForward {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]PortForward, 0, len(m.forwards))
	for _, f := range m.forwards {
		res = append(res, f.config)
	}
	return res
}

// GetPeers returns connected mesh peers, synchronized with libp2p network state.
func (m *Mesh) GetPeers() []PeerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Ensure all currently connected libp2p peers are represented
	var connectedSet map[peer.ID]bool
	if m.host != nil {
		connectedSet = make(map[peer.ID]bool)
		for _, pID := range m.host.Network().Peers() {
			if pID != m.host.ID() {
				connectedSet[pID] = true
			}
		}
	}

	res := make([]PeerInfo, 0, len(m.peers))
	for pID, p := range m.peers {
		info := *p
		if connectedSet != nil {
			info.Connected = connectedSet[pID]
		}
		res = append(res, info)
	}

	// Add any missing connected libp2p peers
	if connectedSet != nil {
		for pID := range connectedSet {
			if _, exists := m.peers[pID]; !exists {
				nodeName := pID.String()
				if len(nodeName) > 8 {
					nodeName = nodeName[:8]
				}
				res = append(res, PeerInfo{
					PeerID:      pID.String(),
					NodeName:    nodeName,
					VirtualIP:   deriveVirtualIP(pID),
					Connected:   true,
					DirectRoute: true,
				})
			}
		}
	}

	return res
}
