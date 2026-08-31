package memvpn

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Router handles virtual IP to peer ID mapping and stream dispatch.
type Router struct {
	host       host.Host
	ipToPeer   map[string]peer.ID
	peerToIP   map[peer.ID]string
	services   map[string]*ExposedService
	mu         sync.RWMutex
}

// NewRouter constructs a new Router.
func NewRouter(h host.Host) *Router {
	return &Router{
		host:     h,
		ipToPeer: make(map[string]peer.ID),
		peerToIP: make(map[peer.ID]string),
		services: make(map[string]*ExposedService),
	}
}

// RegisterPeer records virtual IP mapping for a peer.
func (r *Router) RegisterPeer(pID peer.ID, virtualIP string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ipToPeer[virtualIP] = pID
	r.peerToIP[pID] = virtualIP
}

// UnregisterPeer removes a peer from the routing table.
func (r *Router) UnregisterPeer(pID peer.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ip, ok := r.peerToIP[pID]; ok {
		delete(r.ipToPeer, ip)
		delete(r.peerToIP, pID)
	}
}

// GetPeerByIP looks up the peer ID associated with a virtual IP.
func (r *Router) GetPeerByIP(virtualIP string) (peer.ID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pID, ok := r.ipToPeer[virtualIP]
	return pID, ok
}

// GetIPByPeer looks up the virtual IP for a peer ID.
func (r *Router) GetIPByPeer(pID peer.ID) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ip, ok := r.peerToIP[pID]
	return ip, ok
}

// DialService opens an encrypted stream to a remote peer's named service.
func (r *Router) DialService(ctx context.Context, pID peer.ID, serviceName string) (net.Conn, error) {
	if r.host == nil {
		return nil, errors.New("libp2p host not initialized")
	}

	stream, err := r.host.NewStream(ctx, pID, MeshProtocolID)
	if err != nil {
		return nil, fmt.Errorf("open mesh stream to %s: %w", pID.String(), err)
	}

	req := DialRequestPayload{ServiceName: serviceName}
	if err := WriteJSON(stream, FrameDialRequest, req); err != nil {
		_ = stream.Reset()
		return nil, fmt.Errorf("write dial request: %w", err)
	}

	frame, err := ReadFrame(stream)
	if err != nil {
		_ = stream.Reset()
		return nil, fmt.Errorf("read dial response: %w", err)
	}

	if frame.Type != FrameDialResponse {
		_ = stream.Reset()
		return nil, fmt.Errorf("unexpected frame type 0x%x (expected FrameDialResponse)", frame.Type)
	}

	var resp DialResponsePayload
	if err := ReadJSON(frame, &resp); err != nil {
		_ = stream.Reset()
		return nil, fmt.Errorf("parse dial response: %w", err)
	}

	if !resp.Success {
		_ = stream.Reset()
		return nil, fmt.Errorf("service error: %s", resp.Error)
	}

	return newStreamConn(stream), nil
}

// deriveVirtualIP deterministically hashes a peer.ID into 10.42.x.y.
func deriveVirtualIP(pID peer.ID) string {
	hash := sha256.Sum256([]byte(pID.String()))
	b1 := hash[0]%254 + 1
	b2 := hash[1]%254 + 1
	return fmt.Sprintf("10.42.%d.%d", b1, b2)
}

// streamConn adapts a libp2p network.Stream into a net.Conn.
type streamConn struct {
	stream network.Stream
}

func newStreamConn(s network.Stream) net.Conn {
	return &streamConn{stream: s}
}

func (c *streamConn) Read(b []byte) (n int, err error)  { return c.stream.Read(b) }
func (c *streamConn) Write(b []byte) (n int, err error) { return c.stream.Write(b) }
func (c *streamConn) Close() error                     { return c.stream.Close() }

func (c *streamConn) LocalAddr() net.Addr {
	return &net.IPAddr{IP: net.ParseIP("10.42.0.1")}
}

func (c *streamConn) RemoteAddr() net.Addr {
	return &net.IPAddr{IP: net.ParseIP("10.42.0.2")}
}

func (c *streamConn) SetDeadline(t time.Time) error      { return c.stream.SetDeadline(t) }
func (c *streamConn) SetReadDeadline(t time.Time) error  { return c.stream.SetReadDeadline(t) }
func (c *streamConn) SetWriteDeadline(t time.Time) error { return c.stream.SetWriteDeadline(t) }
