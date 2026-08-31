package memvpn

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// userspaceTUN implements tun.Device to intercept decrypted WireGuard IP packets.
type userspaceTUN struct {
	inboundCh  chan []byte
	outboundCh chan []byte
	events     chan tun.Event
	closed     atomic.Bool
	closeOnce  sync.Once
	mtu        int
}

func newUserspaceTUN(mtu int) *userspaceTUN {
	if mtu <= 0 {
		mtu = 1420
	}
	t := &userspaceTUN{
		inboundCh:  make(chan []byte, 2048),
		outboundCh: make(chan []byte, 2048),
		events:     make(chan tun.Event, 10),
		mtu:        mtu,
	}
	t.events <- tun.EventUp
	return t
}

func (t *userspaceTUN) File() *os.File { return nil }

func (t *userspaceTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	if t.closed.Load() {
		return 0, os.ErrClosed
	}
	select {
	case pkt, ok := <-t.outboundCh:
		if !ok {
			return 0, os.ErrClosed
		}
		if len(bufs) == 0 || len(sizes) == 0 {
			return 0, nil
		}
		n := copy(bufs[0][offset:], pkt)
		sizes[0] = n
		return 1, nil
	}
}

func (t *userspaceTUN) Write(bufs [][]byte, offset int) (int, error) {
	if t.closed.Load() {
		return 0, os.ErrClosed
	}
	for _, buf := range bufs {
		if len(buf) <= offset {
			continue
		}
		packet := make([]byte, len(buf)-offset)
		copy(packet, buf[offset:])
		select {
		case t.inboundCh <- packet:
		default:
		}
	}
	return len(bufs), nil
}

func (t *userspaceTUN) MTU() (int, error)        { return t.mtu, nil }
func (t *userspaceTUN) Name() (string, error)   { return "membuss-vpn", nil }
func (t *userspaceTUN) Events() <-chan tun.Event { return t.events }
func (t *userspaceTUN) BatchSize() int           { return 1 }
func (t *userspaceTUN) Close() error {
	t.closeOnce.Do(func() {
		t.closed.Store(true)
		close(t.events)
		close(t.outboundCh)
	})
	return nil
}

func (t *userspaceTUN) InjectPacket(pkt []byte) {
	if t.closed.Load() {
		return
	}
	select {
	case t.outboundCh <- pkt:
	default:
	}
}

// ClientPeer represents a registered client device.
type ClientPeer struct {
	Device        WGDevice
	PubKeyBytes   [32]byte
	PrivKeyBytes  [32]byte
	RemoteUDPAddr *net.UDPAddr
	mu            sync.RWMutex
}

// WGState is the persistent disk state for the WireGuard subsystem.
type WGState struct {
	ServerPrivateKey string     `json:"server_private_key"`
	ServerListenPort int        `json:"server_listen_port"`
	Devices          []WGDevice `json:"devices"`
}

// WGServer manages the WireGuard userspace server and peer state.
type WGServer struct {
	serverPrivKey Key
	serverPubKey  Key
	listenPort    int
	statePath     string
	dev           *device.Device
	tunDev        *userspaceTUN
	uapiBind      conn.Bind
	peers         map[[32]byte]*ClientPeer // map by client public key
	peersByIP     map[string]*ClientPeer   // map by virtual IP
	nextIPSuffix  int
	stats         *TrafficStats
	natRouter     *NATRouter
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.RWMutex
}

// NewWGServer initializes a new userspace WireGuard engine with state persistence.
func NewWGServer(port int, stats *TrafficStats, stateDir string) (*WGServer, error) {
	if port <= 0 {
		port = DefaultWGPort
	}

	var statePath string
	if stateDir != "" {
		statePath = filepath.Join(stateDir, "memvpn", "wg_state.json")
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &WGServer{
		listenPort:   port,
		statePath:    statePath,
		peers:        make(map[[32]byte]*ClientPeer),
		peersByIP:    make(map[string]*ClientPeer),
		nextIPSuffix: 2,
		stats:        stats,
		ctx:          ctx,
		cancel:       cancel,
	}

	loaded := s.loadState()
	if !loaded {
		privKey, err := GeneratePrivateKey()
		if err != nil {
			cancel()
			return nil, fmt.Errorf("generate server private key: %w", err)
		}
		s.serverPrivKey = privKey
		s.serverPubKey = privKey.PublicKey()
		s.saveStateLocked()
	}

	return s, nil
}

func (s *WGServer) saveStateLocked() {
	if s.statePath == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.statePath), 0755)

	devs := make([]WGDevice, 0, len(s.peers))
	for _, p := range s.peers {
		devs = append(devs, p.Device)
	}

	state := WGState{
		ServerPrivateKey: s.serverPrivKey.String(),
		ServerListenPort: s.listenPort,
		Devices:          devs,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err == nil {
		_ = os.WriteFile(s.statePath, data, 0600)
	}
}

func (s *WGServer) loadState() bool {
	if s.statePath == "" {
		return false
	}
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return false
	}
	var state WGState
	if err := json.Unmarshal(data, &state); err != nil {
		return false
	}

	privKey, err := ParseKey(state.ServerPrivateKey)
	if err != nil {
		return false
	}
	s.serverPrivKey = privKey
	s.serverPubKey = privKey.PublicKey()
	if state.ServerListenPort > 0 {
		s.listenPort = state.ServerListenPort
	}

	maxSuffix := 1
	for _, dev := range state.Devices {
		clientPriv, err := ParseKey(dev.PrivateKey)
		if err != nil {
			continue
		}
		clientPub := clientPriv.PublicKey()

		var pubBytes, privBytes [32]byte
		copy(pubBytes[:], clientPub[:])
		copy(privBytes[:], clientPriv[:])

		peer := &ClientPeer{
			Device:       dev,
			PubKeyBytes:  pubBytes,
			PrivKeyBytes: privBytes,
		}
		s.peers[pubBytes] = peer
		s.peersByIP[dev.VirtualIP] = peer

		parts := strings.Split(dev.VirtualIP, ".")
		if len(parts) == 4 {
			if suf, err := strconv.Atoi(parts[3]); err == nil && suf > maxSuffix {
				maxSuffix = suf
			}
		}
	}
	s.nextIPSuffix = maxSuffix + 1
	return true
}

// SetNATRouter configures the userspace TCP/UDP NAT session router.
func (s *WGServer) SetNATRouter(r *NATRouter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.natRouter = r
}

// Start binds to the UDP port and begins listening for WireGuard client handshakes.
func (s *WGServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tunDev := newUserspaceTUN(1420)
	bind := conn.NewDefaultBind()
	dev := device.NewDevice(tunDev, bind, device.NewLogger(device.LogLevelSilent, "[wireguard] "))

	var boundPort int
	var lastErr error

	// 1. Try binding to the exact configured port with retry backoff
	targetPort := s.listenPort
	if targetPort <= 0 {
		targetPort = DefaultWGPort
	}

	for attempt := 0; attempt < 8; attempt++ {
		var uapiConf strings.Builder
		uapiConf.WriteString(fmt.Sprintf("private_key=%s\n", hex.EncodeToString(s.serverPrivKey[:])))
		uapiConf.WriteString(fmt.Sprintf("listen_port=%d\n", targetPort))

		for _, p := range s.peers {
			uapiConf.WriteString(fmt.Sprintf("public_key=%s\n", hex.EncodeToString(p.PubKeyBytes[:])))
			uapiConf.WriteString(fmt.Sprintf("allowed_ip=%s/32\n", p.Device.VirtualIP))
		}

		err := dev.IpcSetOperation(bufio.NewReader(strings.NewReader(uapiConf.String())))
		if err == nil {
			boundPort = targetPort
			break
		}
		lastErr = err
		time.Sleep(300 * time.Millisecond)
	}

	// 2. Emergency fallback only if port is persistently claimed by external process
	if boundPort == 0 {
		for offset := 1; offset <= 5; offset++ {
			tryPort := targetPort + offset
			var uapiConf strings.Builder
			uapiConf.WriteString(fmt.Sprintf("private_key=%s\n", hex.EncodeToString(s.serverPrivKey[:])))
			uapiConf.WriteString(fmt.Sprintf("listen_port=%d\n", tryPort))

			for _, p := range s.peers {
				uapiConf.WriteString(fmt.Sprintf("public_key=%s\n", hex.EncodeToString(p.PubKeyBytes[:])))
				uapiConf.WriteString(fmt.Sprintf("allowed_ip=%s/32\n", p.Device.VirtualIP))
			}

			err := dev.IpcSetOperation(bufio.NewReader(strings.NewReader(uapiConf.String())))
			if err == nil {
				boundPort = tryPort
				break
			}
			lastErr = err
		}
	}

	if boundPort == 0 {
		dev.Close()
		tunDev.Close()
		return fmt.Errorf("bind wireguard device on port %d: %w", targetPort, lastErr)
	}

	s.listenPort = boundPort

	s.dev = dev
	s.tunDev = tunDev
	s.uapiBind = bind

	go s.dispatchInboundLoop()
	return nil
}

// Stop shuts down the WireGuard server.
func (s *WGServer) Stop() {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dev != nil {
		s.dev.Close()
		s.dev = nil
	}
	if s.tunDev != nil {
		_ = s.tunDev.Close()
		s.tunDev = nil
	}
}

// syncDeviceStatsFromEngine queries live UAPI telemetry from the WireGuard engine.
func (s *WGServer) syncDeviceStatsFromEngine() {
	if s.dev == nil {
		return
	}

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	if err := s.dev.IpcGetOperation(w); err != nil {
		return
	}
	_ = w.Flush()

	scanner := bufio.NewScanner(&buf)
	var currentPeer *ClientPeer

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]

		switch key {
		case "public_key":
			pubHex := val
			pubBytes, err := hex.DecodeString(pubHex)
			if err == nil && len(pubBytes) == 32 {
				var keyArr [32]byte
				copy(keyArr[:], pubBytes)
				currentPeer = s.peers[keyArr]
			} else {
				currentPeer = nil
			}
		case "endpoint":
			if currentPeer != nil && val != "" {
				currentPeer.Device.Endpoint = val
			}
		case "last_handshake_time_sec":
			if currentPeer != nil {
				sec, _ := strconv.ParseInt(val, 10, 64)
				if sec > 0 {
					currentPeer.Device.LastHandshakeTime = time.Unix(sec, 0)
					if time.Since(currentPeer.Device.LastHandshakeTime) < 3*time.Minute {
						currentPeer.Device.Connected = true
					} else {
						currentPeer.Device.Connected = false
					}
				}
			}
		case "rx_bytes":
			if currentPeer != nil {
				rx, _ := strconv.ParseInt(val, 10, 64)
				if rx > 0 {
					currentPeer.Device.BytesRecv = rx
				}
			}
		case "tx_bytes":
			if currentPeer != nil {
				tx, _ := strconv.ParseInt(val, 10, 64)
				if tx > 0 {
					currentPeer.Device.BytesSent = tx
				}
			}
		}
	}
}

// dispatchInboundLoop processes decrypted IP packets from clients and dispatches to NATRouter.
func (s *WGServer) dispatchInboundLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		s.mu.RLock()
		tunDev := s.tunDev
		router := s.natRouter
		s.mu.RUnlock()

		if tunDev == nil {
			return
		}

		select {
		case <-s.ctx.Done():
			return
		case pkt, ok := <-tunDev.inboundCh:
			if !ok {
				return
			}
			if len(pkt) < 20 {
				continue
			}

			srcIP := net.IP(pkt[12:16]).String()

			s.mu.RLock()
			peer := s.peersByIP[srcIP]
			s.mu.RUnlock()

			if peer != nil {
				atomic.AddInt64(&peer.Device.BytesRecv, int64(len(pkt)))
				peer.Device.Connected = true
				peer.Device.LastHandshakeTime = time.Now()
			}
			if s.stats != nil {
				atomic.AddInt64(&s.stats.BytesRecv, int64(len(pkt)))
			}

			if router != nil && peer != nil {
				router.RoutePacket(pkt, peer)
			}
		}
	}
}

// sendTunneledPacket encrypts an IP packet into WireGuard and sends to peer.
func (s *WGServer) sendTunneledPacket(plainIP []byte, peer *ClientPeer) {
	s.mu.RLock()
	tunDev := s.tunDev
	s.mu.RUnlock()

	if tunDev == nil || peer == nil {
		return
	}

	tunDev.InjectPacket(plainIP)
	atomic.AddInt64(&peer.Device.BytesSent, int64(len(plainIP)))
	if s.stats != nil {
		atomic.AddInt64(&s.stats.BytesSent, int64(len(plainIP)))
	}
}

// portLocked returns the listen port assuming s.mu is held.
func (s *WGServer) portLocked() int {
	return s.listenPort
}

// endpointLocked returns the server endpoint assuming s.mu is held.
func (s *WGServer) endpointLocked() string {
	lanIP := GetOutboundIP()
	return fmt.Sprintf("%s:%d", lanIP, s.listenPort)
}

// Port returns the bound UDP port.
func (s *WGServer) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.portLocked()
}

// PublicKey returns the server's public key string.
func (s *WGServer) PublicKey() string {
	return s.serverPubKey.String()
}

// Endpoint returns the host's reachable server endpoint for clients (e.g. 192.168.1.50:51820).
func (s *WGServer) Endpoint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.endpointLocked()
}

// AddDevice registers a new client device and returns its complete WireGuard configuration profile.
func (s *WGServer) AddDevice(name string) (*WGProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if name == "" {
		name = fmt.Sprintf("device-%d", len(s.peers)+1)
	}

	clientPriv, err := GeneratePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generate client key: %w", err)
	}
	clientPub := clientPriv.PublicKey()

	// Assign next available virtual IP
	vip := fmt.Sprintf("%s%d", DefaultVirtualSubnet, s.nextIPSuffix)
	s.nextIPSuffix++

	var pubBytes, privBytes [32]byte
	copy(pubBytes[:], clientPub[:])
	copy(privBytes[:], clientPriv[:])

	dev := WGDevice{
		ID:         fmt.Sprintf("dev-%d", time.Now().UnixNano()),
		Name:       name,
		VirtualIP:  vip,
		PublicKey:  clientPub.String(),
		PrivateKey: clientPriv.String(),
		AllowedIPs: fmt.Sprintf("%s/32", vip),
		CreatedAt:  time.Now(),
	}

	peer := &ClientPeer{
		Device:       dev,
		PubKeyBytes:  pubBytes,
		PrivKeyBytes: privBytes,
	}

	s.peers[pubBytes] = peer
	s.peersByIP[vip] = peer
	s.saveStateLocked()

	// Sync with running WireGuard engine
	if s.dev != nil {
		uapiConf := fmt.Sprintf("public_key=%s\nallowed_ip=%s/32\n",
			hex.EncodeToString(pubBytes[:]),
			vip,
		)
		_ = s.dev.IpcSetOperation(bufio.NewReader(strings.NewReader(uapiConf)))
	}

	endpoint := s.endpointLocked()
	configText := FormatWireGuardConfig(clientPriv.String(), vip, s.serverPubKey.String(), endpoint, "1.1.1.1, 8.8.8.8")

	return &WGProfile{
		DeviceName:     name,
		VirtualIP:      vip,
		ClientPrivKey:  clientPriv.String(),
		ClientPubKey:   clientPub.String(),
		ServerPubKey:   s.serverPubKey.String(),
		ServerEndpoint: endpoint,
		DNS:            "1.1.1.1, 8.8.8.8",
		AllowedIPs:     "0.0.0.0/0, ::/0",
		ConfigText:     configText,
		DownloadURL:    fmt.Sprintf("/api/v1/vpn/wg/config?device=%s", name),
	}, nil
}

// GetProfile retrieves or generates a profile for an existing device.
func (s *WGServer) GetProfile(deviceName string) (*WGProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var target *ClientPeer
	for _, p := range s.peers {
		if p.Device.Name == deviceName || p.Device.ID == deviceName || p.Device.PublicKey == deviceName {
			target = p
			break
		}
	}

	if target == nil && (deviceName == "default" || deviceName == "") {
		for _, p := range s.peers {
			target = p
			break
		}
	}

	if target == nil {
		if deviceName == "default" || deviceName == "" {
			s.mu.RUnlock()
			p, err := s.AddDevice("default-device")
			s.mu.RLock()
			return p, err
		}
		return nil, fmt.Errorf("device %q not found", deviceName)
	}

	endpoint := s.endpointLocked()
	configText := FormatWireGuardConfig(target.Device.PrivateKey, target.Device.VirtualIP, s.serverPubKey.String(), endpoint, "1.1.1.1, 8.8.8.8")

	return &WGProfile{
		DeviceName:     target.Device.Name,
		VirtualIP:      target.Device.VirtualIP,
		ClientPrivKey:  target.Device.PrivateKey,
		ClientPubKey:   target.Device.PublicKey,
		ServerPubKey:   s.serverPubKey.String(),
		ServerEndpoint: endpoint,
		DNS:            "1.1.1.1, 8.8.8.8",
		AllowedIPs:     "0.0.0.0/0, ::/0",
		ConfigText:     configText,
		DownloadURL:    fmt.Sprintf("/api/v1/vpn/wg/config?device=%s", target.Device.Name),
	}, nil
}

// ListDevices returns all registered client devices with live engine telemetry.
func (s *WGServer) ListDevices() []WGDevice {
	s.mu.Lock()
	s.syncDeviceStatsFromEngine()
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]WGDevice, 0, len(s.peers))
	for _, p := range s.peers {
		p.mu.RLock()
		dev := p.Device
		if p.RemoteUDPAddr != nil && dev.Endpoint == "" {
			dev.Endpoint = p.RemoteUDPAddr.String()
		}
		p.mu.RUnlock()
		res = append(res, dev)
	}
	return res
}

// DeleteDevice unregisters a client device.
func (s *WGServer) DeleteDevice(idOrPubKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, p := range s.peers {
		if p.Device.ID == idOrPubKey || p.Device.PublicKey == idOrPubKey || p.Device.Name == idOrPubKey {
			if s.dev != nil {
				uapiConf := fmt.Sprintf("public_key=%s\nremove=true\n", hex.EncodeToString(p.PubKeyBytes[:]))
				_ = s.dev.IpcSetOperation(bufio.NewReader(strings.NewReader(uapiConf)))
			}
			delete(s.peers, k)
			delete(s.peersByIP, p.Device.VirtualIP)
			s.saveStateLocked()
			return nil
		}
	}
	return errors.New("device not found")
}
