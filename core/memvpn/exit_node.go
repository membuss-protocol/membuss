package memvpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ExitManager coordinates exit node routing for both client dialing and server egress.
type ExitManager struct {
	host       host.Host
	acl        *ACL
	policy     ExitPolicy
	stats      *TrafficStats
	activeConn atomic.Int64
	mu         sync.RWMutex
}

// NewExitManager initializes an ExitManager.
func NewExitManager(h host.Host, acl *ACL, stats *TrafficStats) *ExitManager {
	return &ExitManager{
		host:  h,
		acl:   acl,
		stats: stats,
		policy: ExitPolicy{
			AllowAll:        true,
			BlockPrivateIPs: true,
		},
	}
}

// SetPolicy updates exit node egress access policies.
func (em *ExitManager) SetPolicy(policy ExitPolicy) {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.policy = policy
}

// GetPolicy retrieves the active exit policy.
func (em *ExitManager) GetPolicy() ExitPolicy {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return em.policy
}

// ActiveConnections returns the number of currently active exit streams.
func (em *ExitManager) ActiveConnections() int64 {
	return em.activeConn.Load()
}

// HandleExitStream processes inbound exit proxy stream requests.
func (em *ExitManager) HandleExitStream(s network.Stream) {
	defer s.Close()

	remotePeer := s.Conn().RemotePeer()
	em.mu.RLock()
	policy := em.policy
	em.mu.RUnlock()

	// 1. Authorize peer for exit routing
	if err := em.acl.CheckExitAuthorization(remotePeer, &policy); err != nil {
		_ = WriteJSON(s, FrameExitAck, ExitAckPayload{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// 2. Read ExitConnectPayload
	frame, err := ReadFrame(s)
	if err != nil || frame.Type != FrameExitConnect {
		_ = WriteJSON(s, FrameExitAck, ExitAckPayload{
			Success: false,
			Error:   "invalid exit connect frame",
		})
		return
	}

	var req ExitConnectPayload
	if err := ReadJSON(frame, &req); err != nil {
		_ = WriteJSON(s, FrameExitAck, ExitAckPayload{
			Success: false,
			Error:   "invalid exit payload json",
		})
		return
	}

	// 3. Egress Security: Block private LAN and loopback IP addresses
	if policy.BlockPrivateIPs {
		if err := isPublicTarget(req.TargetHost); err != nil {
			_ = WriteJSON(s, FrameExitAck, ExitAckPayload{
				Success: false,
				Error:   fmt.Sprintf("egress blocked: %v", err),
			})
			return
		}
	}

	// 4. Dial external internet target
	targetAddr := fmt.Sprintf("%s:%d", req.TargetHost, req.TargetPort)
	outConn, err := net.DialTimeout("tcp", targetAddr, 10*time.Second)
	if err != nil {
		_ = WriteJSON(s, FrameExitAck, ExitAckPayload{
			Success: false,
			Error:   fmt.Sprintf("dial outbound failed: %v", err),
		})
		return
	}
	defer outConn.Close()

	// 5. Send confirmation Ack
	if err := WriteJSON(s, FrameExitAck, ExitAckPayload{
		Success:   true,
		BoundAddr: outConn.LocalAddr().String(),
	}); err != nil {
		return
	}

	em.activeConn.Add(1)
	defer em.activeConn.Add(-1)
	if em.stats != nil {
		atomic.AddInt64(&em.stats.ContributedConns, 1)
	}

	// 6. Bidirectional streaming pipe with contribution tracking
	pipeExitBidirectional(s, outConn, em.stats)
}

// DialTCP dials an outbound target through a designated exit peer ID.
func (em *ExitManager) DialTCP(ctx context.Context, peerID string, targetHost string, targetPort int) (net.Conn, error) {
	pID, err := peer.Decode(peerID)
	if err != nil {
		return nil, fmt.Errorf("decode exit peer id %q: %w", peerID, err)
	}
	return em.DialExit(ctx, pID, targetHost, targetPort)
}

// DialExit opens an encrypted stream to an Exit Node and requests outbound dialing.
func (em *ExitManager) DialExit(ctx context.Context, exitPeer peer.ID, targetHost string, targetPort int) (net.Conn, error) {
	if em.host == nil {
		return nil, errors.New("libp2p host not available")
	}

	stream, err := em.host.NewStream(ctx, exitPeer, ExitProtocolID)
	if err != nil {
		return nil, fmt.Errorf("open exit stream to %s: %w", exitPeer.String(), err)
	}

	req := ExitConnectPayload{
		TargetHost: targetHost,
		TargetPort: targetPort,
	}
	if err := WriteJSON(stream, FrameExitConnect, req); err != nil {
		_ = stream.Reset()
		return nil, fmt.Errorf("write exit connect request: %w", err)
	}

	frame, err := ReadFrame(stream)
	if err != nil {
		_ = stream.Reset()
		return nil, fmt.Errorf("read exit ack: %w", err)
	}

	if frame.Type != FrameExitAck {
		_ = stream.Reset()
		return nil, fmt.Errorf("unexpected frame type 0x%x (expected FrameExitAck)", frame.Type)
	}

	var ack ExitAckPayload
	if err := ReadJSON(frame, &ack); err != nil {
		_ = stream.Reset()
		return nil, fmt.Errorf("parse exit ack: %w", err)
	}

	if !ack.Success {
		_ = stream.Reset()
		return nil, fmt.Errorf("exit node refused connect: %s", ack.Error)
	}

	return newStreamConn(stream), nil
}

// isPublicTarget validates that target does not resolve to loopback or RFC1918 private subnets.
func isPublicTarget(host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return errors.New("loopback destinations are prohibited")
		}
		if ip.IsPrivate() {
			return errors.New("private LAN destinations (RFC1918) are prohibited on exit nodes")
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return errors.New("link-local destinations are prohibited")
		}
		return nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil // If unresolved, allow dialer to return DNS error
	}

	for _, ip := range ips {
		if ip.IsLoopback() {
			return errors.New("loopback destinations are prohibited")
		}
		if ip.IsPrivate() {
			return errors.New("private LAN destinations (RFC1918) are prohibited on exit nodes")
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return errors.New("link-local destinations are prohibited")
		}
	}
	return nil
}

// pipeBidirectional pipes data bidirectionally between stream and TCP connection.
func pipeBidirectional(s io.ReadWriter, c io.ReadWriter, stats *TrafficStats) {
	var once sync.Once
	closeBoth := func() {
		if cs, ok := c.(io.Closer); ok {
			_ = cs.Close()
		}
		if ss, ok := s.(io.Closer); ok {
			_ = ss.Close()
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer func() {
			once.Do(closeBoth)
			wg.Done()
		}()
		buf := make([]byte, 32*1024)
		for {
			n, err := s.Read(buf)
			if n > 0 {
				if stats != nil {
					atomic.AddInt64(&stats.BytesRecv, int64(n))
				}
				if _, wErr := c.Write(buf[:n]); wErr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		defer func() {
			once.Do(closeBoth)
			wg.Done()
		}()
		buf := make([]byte, 32*1024)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				if stats != nil {
					atomic.AddInt64(&stats.BytesSent, int64(n))
				}
				if _, wErr := s.Write(buf[:n]); wErr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	wg.Wait()
}

// pipeExitBidirectional pipes between a remote swarm peer and outbound internet target,
// recording contributed egress and ingress bandwidth.
func pipeExitBidirectional(s io.ReadWriter, c io.ReadWriter, stats *TrafficStats) {
	var once sync.Once
	closeBoth := func() {
		if cs, ok := c.(io.Closer); ok {
			_ = cs.Close()
		}
		if ss, ok := s.(io.Closer); ok {
			_ = ss.Close()
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Stream from peer -> Outbound Internet
	go func() {
		defer func() {
			once.Do(closeBoth)
			wg.Done()
		}()
		buf := make([]byte, 32*1024)
		for {
			n, err := s.Read(buf)
			if n > 0 {
				if stats != nil {
					atomic.AddInt64(&stats.BytesRecv, int64(n))
					atomic.AddInt64(&stats.ContributedBytesRecv, int64(n))
				}
				if _, wErr := c.Write(buf[:n]); wErr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	// Stream from Outbound Internet -> Peer
	go func() {
		defer func() {
			once.Do(closeBoth)
			wg.Done()
		}()
		buf := make([]byte, 32*1024)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				if stats != nil {
					atomic.AddInt64(&stats.BytesSent, int64(n))
					atomic.AddInt64(&stats.ContributedBytesSent, int64(n))
				}
				if _, wErr := s.Write(buf[:n]); wErr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	wg.Wait()
}
