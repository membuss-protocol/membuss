package memvpn

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// OutboundDialer is an interface to dial TCP outbound, optionally via decentralized exit nodes.
type OutboundDialer interface {
	DialExitTarget(ctx context.Context, targetHost string, targetPort int) (net.Conn, error)
}

// NATRouter provides pure-Go userspace TCP and UDP packet routing for WireGuard peers.
type NATRouter struct {
	server      *WGServer
	dialer      OutboundDialer
	activeExit  string
	tcpSessions map[string]*tcpSession
	udpSessions map[string]*udpSession
	stats       *TrafficStats
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.RWMutex
}

type tcpSession struct {
	key        string
	clientIP   net.IP
	dstIP      net.IP
	clientPort uint16
	dstPort    uint16
	peer       *ClientPeer
	conn       net.Conn
	clientSeq  uint32
	serverSeq  uint32
	lastActive time.Time
	closed     atomic.Bool
	mu         sync.Mutex
}

type udpSession struct {
	key        string
	clientIP   net.IP
	dstIP      net.IP
	clientPort uint16
	dstPort    uint16
	peer       *ClientPeer
	conn       *net.UDPConn
	dstAddr    *net.UDPAddr
	lastActive time.Time
	closed     atomic.Bool
}

// NewNATRouter constructs a new userspace NAT router.
func NewNATRouter(server *WGServer, stats *TrafficStats, dialer OutboundDialer) *NATRouter {
	ctx, cancel := context.WithCancel(context.Background())
	r := &NATRouter{
		server:      server,
		dialer:      dialer,
		tcpSessions: make(map[string]*tcpSession),
		udpSessions: make(map[string]*udpSession),
		stats:       stats,
		ctx:         ctx,
		cancel:      cancel,
	}
	go r.cleanerLoop()
	return r
}

// SetDialer updates the outbound dialer.
func (r *NATRouter) SetDialer(dialer OutboundDialer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dialer = dialer
}

// SetActiveExit sets or clears the current exit node peer ID for egress traffic.
func (r *NATRouter) SetActiveExit(peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activeExit = peerID
}

// Stop shuts down all active sessions.
func (r *NATRouter) Stop() {
	r.cancel()
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, s := range r.tcpSessions {
		if s.conn != nil {
			_ = s.conn.Close()
		}
	}
	for _, u := range r.udpSessions {
		if u.conn != nil {
			_ = u.conn.Close()
		}
	}
	r.tcpSessions = make(map[string]*tcpSession)
	r.udpSessions = make(map[string]*udpSession)
}

func (r *NATRouter) cleanerLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.mu.Lock()
			now := time.Now()
			for k, s := range r.tcpSessions {
				if s.closed.Load() || now.Sub(s.lastActive) > 2*time.Minute {
					if s.conn != nil {
						_ = s.conn.Close()
					}
					delete(r.tcpSessions, k)
				}
			}
			for k, u := range r.udpSessions {
				if u.closed.Load() || now.Sub(u.lastActive) > 45*time.Second {
					if u.conn != nil {
						_ = u.conn.Close()
					}
					delete(r.udpSessions, k)
				}
			}
			r.mu.Unlock()
		}
	}
}

// RoutePacket handles an IPv4 packet received from a WireGuard peer.
func (r *NATRouter) RoutePacket(ipPacket []byte, peer *ClientPeer) {
	if len(ipPacket) < 20 {
		return
	}

	version := ipPacket[0] >> 4
	if version != 4 {
		return
	}

	protocol := ipPacket[9]
	srcIP := net.IP(ipPacket[12:16])
	dstIP := net.IP(ipPacket[16:20])

	// 1. ICMP Echo Request
	if protocol == 1 {
		r.handleICMP(ipPacket, peer)
		return
	}

	// 2. UDP
	if protocol == 17 && len(ipPacket) >= 28 {
		srcPort := binary.BigEndian.Uint16(ipPacket[20:22])
		dstPort := binary.BigEndian.Uint16(ipPacket[22:24])
		udpPayload := ipPacket[28:]

		// DNS queries (port 53 or directed to 10.42.0.1)
		if dstPort == 53 || dstIP.String() == "10.42.0.1" {
			go r.handleDNS(srcIP, dstIP, srcPort, dstPort, udpPayload, peer)
			return
		}

		r.mu.RLock()
		activeExit := r.activeExit
		r.mu.RUnlock()

		// When swarm egress is active, drop QUIC UDP 443 so clients immediately fallback to TCP through the exit swarm
		if activeExit != "" && dstPort == 443 {
			return
		}

		go r.handleUDP(srcIP, dstIP, srcPort, dstPort, udpPayload, peer)
		return
	}

	// 3. TCP
	if protocol == 6 && len(ipPacket) >= 40 {
		srcPort := binary.BigEndian.Uint16(ipPacket[20:22])
		dstPort := binary.BigEndian.Uint16(ipPacket[22:24])
		seq := binary.BigEndian.Uint32(ipPacket[24:28])
		ack := binary.BigEndian.Uint32(ipPacket[28:32])
		dataOffset := (ipPacket[32] >> 4) * 4
		flags := ipPacket[33]

		var tcpPayload []byte
		if int(dataOffset) <= len(ipPacket)-20 {
			tcpPayload = ipPacket[20+dataOffset:]
		}

		go r.handleTCP(srcIP, dstIP, srcPort, dstPort, seq, ack, flags, tcpPayload, peer)
		return
	}
}

// handleICMP replies to Echo Requests for any IP to ensure ping works.
func (r *NATRouter) handleICMP(ipPacket []byte, peer *ClientPeer) {
	if len(ipPacket) < 28 || ipPacket[20] != 8 { // 8 = Echo Request
		return
	}

	resp := make([]byte, len(ipPacket))
	copy(resp, ipPacket)

	// Swap IP source and dest
	copy(resp[12:16], ipPacket[16:20])
	copy(resp[16:20], ipPacket[12:16])

	// Change ICMP type to 0 (Echo Reply)
	resp[20] = 0
	resp[22] = 0 // Checksum reset
	resp[23] = 0

	icmpChecksum := computeChecksum(resp[20:])
	binary.BigEndian.PutUint16(resp[22:24], icmpChecksum)

	// Recalculate IP Checksum
	resp[10] = 0
	resp[11] = 0
	ipChecksum := computeChecksum(resp[:20])
	binary.BigEndian.PutUint16(resp[10:12], ipChecksum)

	r.server.sendTunneledPacket(resp, peer)
}

// handleDNS handles UDP DNS queries and returns valid responses fast.
func (r *NATRouter) handleDNS(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte, peer *ClientPeer) {
	if r.stats != nil {
		atomic.AddInt64(&r.stats.DNSQueriesCount, 1)
		atomic.AddInt64(&r.stats.ClientBytesSent, int64(len(payload)))
	}

	type dnsResult struct {
		data []byte
		err  error
	}

	resolvers := []string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:53"}
	resCh := make(chan dnsResult, len(resolvers))

	queryResolver := func(server string) {
		conn, err := net.DialTimeout("udp", server, 2500*time.Millisecond)
		if err != nil {
			resCh <- dnsResult{err: err}
			return
		}
		defer conn.Close()

		_ = conn.SetDeadline(time.Now().Add(3000 * time.Millisecond))
		if _, err := conn.Write(payload); err != nil {
			resCh <- dnsResult{err: err}
			return
		}

		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			resCh <- dnsResult{err: err}
			return
		}
		resCh <- dnsResult{data: buf[:n]}
	}

	for _, srv := range resolvers {
		go queryResolver(srv)
	}

	var responseData []byte
	for i := 0; i < len(resolvers); i++ {
		res := <-resCh
		if res.err == nil && len(res.data) > 0 {
			responseData = res.data
			break
		}
	}

	if len(responseData) == 0 {
		return
	}

	if r.stats != nil {
		atomic.AddInt64(&r.stats.ClientBytesRecv, int64(len(responseData)))
	}

	replyPacket := buildIPv4UDPPacket(dstIP, srcIP, dstPort, srcPort, responseData)
	r.server.sendTunneledPacket(replyPacket, peer)
}

// handleUDP forwards general UDP packets with session affinity.
func (r *NATRouter) handleUDP(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte, peer *ClientPeer) {
	key := fmt.Sprintf("%s:%d->%s:%d", srcIP, srcPort, dstIP, dstPort)

	r.mu.RLock()
	sess, exists := r.udpSessions[key]
	r.mu.RUnlock()

	if !exists {
		if r.stats != nil {
			atomic.AddInt64(&r.stats.UDPFlowsCount, 1)
		}

		dstAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", dstIP, dstPort))
		if err != nil {
			return
		}

		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			return
		}

		sess = &udpSession{
			key:        key,
			clientIP:   srcIP,
			dstIP:      dstIP,
			clientPort: srcPort,
			dstPort:    dstPort,
			peer:       peer,
			conn:       conn,
			dstAddr:    dstAddr,
			lastActive: time.Now(),
		}

		r.mu.Lock()
		r.udpSessions[key] = sess
		r.mu.Unlock()

		go r.udpReadLoop(sess)
	}

	sess.lastActive = time.Now()
	if r.stats != nil {
		atomic.AddInt64(&r.stats.ClientBytesSent, int64(len(payload)))
	}
	_, _ = sess.conn.WriteToUDP(payload, sess.dstAddr)
}

func (r *NATRouter) udpReadLoop(sess *udpSession) {
	defer func() {
		sess.closed.Store(true)
		if sess.conn != nil {
			_ = sess.conn.Close()
		}
	}()

	buf := make([]byte, 65535)
	for {
		select {
		case <-r.ctx.Done():
			return
		default:
		}

		_ = sess.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, _, err := sess.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}

		sess.lastActive = time.Now()
		if r.stats != nil {
			atomic.AddInt64(&r.stats.ClientBytesRecv, int64(n))
		}
		replyPacket := buildIPv4UDPPacket(sess.dstIP, sess.clientIP, sess.dstPort, sess.clientPort, buf[:n])
		r.server.sendTunneledPacket(replyPacket, sess.peer)
	}
}

// handleTCP implements a userspace TCP session translator with full MSS negotiation.
func (r *NATRouter) handleTCP(srcIP, dstIP net.IP, srcPort, dstPort uint16, seq, ack uint32, flags byte, payload []byte, peer *ClientPeer) {
	key := fmt.Sprintf("%s:%d->%s:%d", srcIP, srcPort, dstIP, dstPort)

	// TCP SYN (Connection initiation)
	if flags&0x02 != 0 { // SYN
		r.mu.Lock()
		if old, exists := r.tcpSessions[key]; exists {
			old.closed.Store(true)
			if old.conn != nil {
				_ = old.conn.Close()
			}
			delete(r.tcpSessions, key)
		}
		activeExit := r.activeExit
		dialer := r.dialer
		r.mu.Unlock()

		// Dial target outside mutex
		dialTarget := fmt.Sprintf("%s:%d", dstIP, dstPort)
		var conn net.Conn
		var err error

		if activeExit != "" && dialer != nil {
			ctx, cancel := context.WithTimeout(r.ctx, 12*time.Second)
			conn, err = dialer.DialExitTarget(ctx, dstIP.String(), int(dstPort))
			cancel()
		} else {
			conn, err = net.DialTimeout("tcp", dialTarget, 5*time.Second)
		}

		if err != nil {
			rstPacket := buildIPv4TCPPacket(dstIP, srcIP, dstPort, srcPort, 0, seq+1, 0x14, nil) // RST|ACK
			r.server.sendTunneledPacket(rstPacket, peer)
			return
		}

		nBig, _ := rand.Int(rand.Reader, big.NewInt(0x7fffffff))
		serverInitSeq := uint32(nBig.Uint64())

		sess := &tcpSession{
			key:        key,
			clientIP:   srcIP,
			dstIP:      dstIP,
			clientPort: srcPort,
			dstPort:    dstPort,
			peer:       peer,
			conn:       conn,
			clientSeq:  seq + 1,
			serverSeq:  serverInitSeq + 1,
			lastActive: time.Now(),
		}

		r.mu.Lock()
		r.tcpSessions[key] = sess
		r.mu.Unlock()

		if r.stats != nil {
			atomic.AddInt64(&r.stats.TCPConnsCount, 1)
			atomic.AddInt64(&r.stats.ClientConns, 1)
		}

		// Send SYN-ACK with MSS (1360) and SACK options
		synAck := buildIPv4TCPSynAckPacket(dstIP, srcIP, dstPort, srcPort, serverInitSeq, sess.clientSeq)
		r.server.sendTunneledPacket(synAck, peer)

		// Start fast reader pump from target
		go r.tcpReadLoop(sess)
		return
	}

	r.mu.RLock()
	sess, exists := r.tcpSessions[key]
	r.mu.RUnlock()

	if !exists || sess.closed.Load() {
		if flags&0x04 == 0 { // Not RST
			rstPacket := buildIPv4TCPPacket(dstIP, srcIP, dstPort, srcPort, ack, seq+1, 0x14, nil)
			r.server.sendTunneledPacket(rstPacket, peer)
		}
		return
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.lastActive = time.Now()

	// Handle Client RST
	if flags&0x04 != 0 { // RST
		sess.closed.Store(true)
		if sess.conn != nil {
			_ = sess.conn.Close()
		}
		return
	}

	// Handle Client FIN
	if flags&0x01 != 0 { // FIN
		sess.closed.Store(true)
		if sess.conn != nil {
			_ = sess.conn.Close()
		}
		// Send FIN-ACK back
		finAck := buildIPv4TCPPacket(dstIP, srcIP, dstPort, srcPort, sess.serverSeq, seq+1, 0x11, nil)
		r.server.sendTunneledPacket(finAck, peer)
		return
	}

	// Forward client TCP payload to remote connection
	if len(payload) > 0 {
		_, err := sess.conn.Write(payload)
		if err != nil {
			sess.closed.Store(true)
			_ = sess.conn.Close()
			return
		}
		if r.stats != nil {
			atomic.AddInt64(&r.stats.ClientBytesSent, int64(len(payload)))
		}
		sess.clientSeq = seq + uint32(len(payload))

		// Send TCP ACK back to client
		ackPacket := buildIPv4TCPPacket(dstIP, srcIP, dstPort, srcPort, sess.serverSeq, sess.clientSeq, 0x10, nil) // ACK
		r.server.sendTunneledPacket(ackPacket, peer)
	}
}

func (r *NATRouter) tcpReadLoop(sess *tcpSession) {
	defer func() {
		sess.closed.Store(true)
		if sess.conn != nil {
			_ = sess.conn.Close()
		}
		r.mu.Lock()
		delete(r.tcpSessions, sess.key)
		r.mu.Unlock()
	}()

	buf := make([]byte, 1360) // Exact WireGuard MSS payload size
	for {
		select {
		case <-r.ctx.Done():
			return
		default:
		}

		_ = sess.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := sess.conn.Read(buf)
		if err != nil {
			if err != io.EOF && !sess.closed.Load() {
				sess.mu.Lock()
				rstPacket := buildIPv4TCPPacket(sess.dstIP, sess.clientIP, sess.dstPort, sess.clientPort, sess.serverSeq, sess.clientSeq, 0x14, nil)
				sess.mu.Unlock()
				r.server.sendTunneledPacket(rstPacket, sess.peer)
			} else if err == io.EOF && !sess.closed.Load() {
				sess.mu.Lock()
				finPacket := buildIPv4TCPPacket(sess.dstIP, sess.clientIP, sess.dstPort, sess.clientPort, sess.serverSeq, sess.clientSeq, 0x11, nil)
				sess.mu.Unlock()
				r.server.sendTunneledPacket(finPacket, sess.peer)
			}
			return
		}

		sess.mu.Lock()
		dataPacket := buildIPv4TCPPacket(sess.dstIP, sess.clientIP, sess.dstPort, sess.clientPort, sess.serverSeq, sess.clientSeq, 0x18, buf[:n]) // PSH|ACK
		sess.serverSeq += uint32(n)
		sess.lastActive = time.Now()
		sess.mu.Unlock()

		if r.stats != nil {
			atomic.AddInt64(&r.stats.ClientBytesRecv, int64(n))
		}

		r.server.sendTunneledPacket(dataPacket, sess.peer)
	}
}

// Helper: buildIPv4UDPPacket constructs an IPv4 + UDP packet with checksums.
func buildIPv4UDPPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	totalLen := 20 + 8 + len(payload)
	packet := make([]byte, totalLen)

	// IPv4 Header
	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(totalLen))
	packet[8] = 64  // TTL
	packet[9] = 17  // UDP
	copy(packet[12:16], srcIP.To4())
	copy(packet[16:20], dstIP.To4())

	ipChecksum := computeChecksum(packet[:20])
	binary.BigEndian.PutUint16(packet[10:12], ipChecksum)

	// UDP Header
	binary.BigEndian.PutUint16(packet[20:22], srcPort)
	binary.BigEndian.PutUint16(packet[22:24], dstPort)
	binary.BigEndian.PutUint16(packet[24:26], uint16(8+len(payload)))

	copy(packet[28:], payload)
	return packet
}

// Helper: buildIPv4TCPSynAckPacket constructs an IPv4 TCP SYN-ACK packet with MSS and Window Scale options.
func buildIPv4TCPSynAckPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, seq, ack uint32) []byte {
	// TCP Options: MSS 1360 (4 bytes) + SACK Permitted (2 bytes) + 2 NOPs (2 bytes) = 8 bytes
	tcpOptions := []byte{
		0x02, 0x04, 0x05, 0x50, // MSS = 1360
		0x04, 0x02,             // SACK Permitted
		0x01, 0x01,             // NOP, NOP padding
	}

	totalLen := 20 + 20 + len(tcpOptions)
	packet := make([]byte, totalLen)

	// IPv4 Header
	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(totalLen))
	packet[8] = 64
	packet[9] = 6
	copy(packet[12:16], srcIP.To4())
	copy(packet[16:20], dstIP.To4())

	ipChecksum := computeChecksum(packet[:20])
	binary.BigEndian.PutUint16(packet[10:12], ipChecksum)

	// TCP Header
	tcpSeg := packet[20:]
	binary.BigEndian.PutUint16(tcpSeg[0:2], srcPort)
	binary.BigEndian.PutUint16(tcpSeg[2:4], dstPort)
	binary.BigEndian.PutUint32(tcpSeg[4:8], seq)
	binary.BigEndian.PutUint32(tcpSeg[8:12], ack)
	tcpSeg[12] = 0x70 // Data Offset 7 words = 28 bytes
	tcpSeg[13] = 0x12 // SYN | ACK
	binary.BigEndian.PutUint16(tcpSeg[14:16], 65535)

	copy(tcpSeg[20:], tcpOptions)

	tcpChecksum := computeTCPChecksum(srcIP, dstIP, tcpSeg)
	binary.BigEndian.PutUint16(tcpSeg[16:18], tcpChecksum)

	return packet
}

// Helper: buildIPv4TCPPacket constructs an IPv4 + TCP packet with valid checksums.
func buildIPv4TCPPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, seq, ack uint32, flags byte, payload []byte) []byte {
	totalLen := 20 + 20 + len(payload)
	packet := make([]byte, totalLen)

	// IPv4 Header
	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(totalLen))
	packet[8] = 64 // TTL
	packet[9] = 6  // TCP
	copy(packet[12:16], srcIP.To4())
	copy(packet[16:20], dstIP.To4())

	ipChecksum := computeChecksum(packet[:20])
	binary.BigEndian.PutUint16(packet[10:12], ipChecksum)

	// TCP Header
	tcpSeg := packet[20:]
	binary.BigEndian.PutUint16(tcpSeg[0:2], srcPort)
	binary.BigEndian.PutUint16(tcpSeg[2:4], dstPort)
	binary.BigEndian.PutUint32(tcpSeg[4:8], seq)
	binary.BigEndian.PutUint32(tcpSeg[8:12], ack)
	tcpSeg[12] = 0x50 // Data offset 5 (20 bytes)
	tcpSeg[13] = flags
	binary.BigEndian.PutUint16(tcpSeg[14:16], 65535) // Window size

	if len(payload) > 0 {
		copy(tcpSeg[20:], payload)
	}

	tcpChecksum := computeTCPChecksum(srcIP, dstIP, tcpSeg)
	binary.BigEndian.PutUint16(tcpSeg[16:18], tcpChecksum)

	return packet
}

// computeChecksum computes standard RFC 1071 internet checksum.
func computeChecksum(data []byte) uint16 {
	var sum uint32
	for i := 0; i < len(data)-1; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
	}
	if len(data)%2 == 1 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for (sum >> 16) > 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	c := ^uint16(sum)
	if c == 0 {
		return 0xffff
	}
	return c
}

func computeTCPChecksum(srcIP, dstIP net.IP, tcpSegment []byte) uint16 {
	src := srcIP.To4()
	dst := dstIP.To4()
	if src == nil || dst == nil {
		return 0
	}
	pseudo := make([]byte, 12+len(tcpSegment))
	copy(pseudo[0:4], src)
	copy(pseudo[4:8], dst)
	pseudo[8] = 0
	pseudo[9] = 6 // TCP
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(tcpSegment)))
	copy(pseudo[12:], tcpSegment)
	pseudo[12+16] = 0
	pseudo[12+17] = 0
	return computeChecksum(pseudo)
}
