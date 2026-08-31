package memvpn

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestWireGuardKeyGeneration(t *testing.T) {
	priv, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}
	if priv == "" || pub == "" {
		t.Fatal("empty key generated")
	}

	parsedPriv, err := ParseKey(priv)
	if err != nil {
		t.Fatalf("ParseKey private failed: %v", err)
	}
	derivedPub := parsedPriv.PublicKey().String()
	if derivedPub != pub {
		t.Fatalf("derived public key mismatch: got %s, want %s", derivedPub, pub)
	}

	conf := FormatWireGuardConfig(priv, "10.42.0.2", pub, "192.168.1.50:51820", "10.42.0.1")
	if !strings.Contains(conf, "[Interface]") || !strings.Contains(conf, "[Peer]") {
		t.Fatalf("invalid WireGuard config formatting:\n%s", conf)
	}
}

func TestWireGuardServerDeviceManagement(t *testing.T) {
	stats := &TrafficStats{}
	srv, err := NewWGServer(51890, stats, t.TempDir())
	if err != nil {
		t.Fatalf("NewWGServer failed: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop()

	// 1. Add device
	profile, err := srv.AddDevice("my-iphone")
	if err != nil {
		t.Fatalf("AddDevice failed: %v", err)
	}
	if profile.DeviceName != "my-iphone" || !strings.HasPrefix(profile.VirtualIP, "10.42.0.") {
		t.Fatalf("unexpected profile: %+v", profile)
	}

	// 2. List devices
	devices := srv.ListDevices()
	if len(devices) != 1 || devices[0].Name != "my-iphone" {
		t.Fatalf("unexpected devices list: %+v", devices)
	}

	// 3. Get profile
	retrieved, err := srv.GetProfile("my-iphone")
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}
	if retrieved.ConfigText != profile.ConfigText {
		t.Fatalf("retrieved config mismatch: %s vs %s", retrieved.ConfigText, profile.ConfigText)
	}

	// 4. Delete device
	if err := srv.DeleteDevice("my-iphone"); err != nil {
		t.Fatalf("DeleteDevice failed: %v", err)
	}
	if len(srv.ListDevices()) != 0 {
		t.Fatalf("expected 0 devices after deletion")
	}
}

func TestFrameSerialization(t *testing.T) {
	var buf bytes.Buffer

	origPayload := HandshakePayload{
		MeshID:    "test-mesh",
		NodeName:  "node-alpha",
		VirtualIP: "10.42.0.1",
		AuthToken: "token-12345",
		Services:  []string{"web", "ssh"},
		Timestamp: time.Now().UTC().Truncate(time.Second),
	}

	if err := WriteJSON(&buf, FrameHandshake, origPayload); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	frame, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame failed: %v", err)
	}

	if frame.Type != FrameHandshake {
		t.Fatalf("expected FrameHandshake, got %v", frame.Type)
	}

	var decoded HandshakePayload
	if err := ReadJSON(frame, &decoded); err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}

	if decoded.MeshID != origPayload.MeshID || decoded.NodeName != origPayload.NodeName {
		t.Fatalf("decoded payload mismatch: %+v vs %+v", decoded, origPayload)
	}
}

func TestACLValidation(t *testing.T) {
	allowedPeer := peer.ID("12D3KooWPeerAllowed")
	deniedPeer := peer.ID("12D3KooWPeerDenied")

	cfg := &MeshConfig{
		MeshID:        "my-secret-mesh",
		PreSharedKey:  "super-secret-key",
		AllowAllPeers: false,
		AllowedPeers:  []string{allowedPeer.String()},
	}

	acl := NewACL(cfg)

	validToken := GenerateAuthToken(allowedPeer, "my-secret-mesh", "super-secret-key")
	invalidToken := "bad-token"

	// 1. Allowed peer + valid token = OK
	if err := acl.ValidateMeshAuth(allowedPeer, "my-secret-mesh", validToken); err != nil {
		t.Errorf("expected auth success, got %v", err)
	}

	// 2. Allowed peer + bad token = Error
	if err := acl.ValidateMeshAuth(allowedPeer, "my-secret-mesh", invalidToken); err == nil {
		t.Error("expected auth failure for invalid token")
	}

	// 3. Denied peer + valid token = Error
	deniedValidToken := GenerateAuthToken(deniedPeer, "my-secret-mesh", "super-secret-key")
	if err := acl.ValidateMeshAuth(deniedPeer, "my-secret-mesh", deniedValidToken); err == nil {
		t.Error("expected auth failure for non-whitelisted peer")
	}
}

func TestEndToEndMeshServiceForwarding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. Create Host A and Host B
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host1: %v", err)
	}
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host2: %v", err)
	}
	defer h2.Close()

	// Connect Host 2 to Host 1
	h2.Peerstore().AddAddrs(h1.ID(), h1.Addrs(), time.Hour)
	if err := h2.Connect(ctx, peer.AddrInfo{ID: h1.ID(), Addrs: h1.Addrs()}); err != nil {
		t.Fatalf("connect hosts: %v", err)
	}

	// 2. Start mock backend HTTP service on Host 1
	expectedBody := "Hello from MemVPN secure WireGuard & P2P mesh!"
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, expectedBody)
	}))
	defer backendServer.Close()

	backendAddr := backendServer.Listener.Addr().String()

	// 3. Start MemVPN Service on Node 1 & Expose "webapp"
	meshConfig := MeshConfig{
		MeshID:        "test-mesh",
		PreSharedKey:  "mesh-psk-12345",
		AllowAllPeers: true,
		WGListenPort:  51892,
	}

	vpn1 := NewService(h1, meshConfig)
	if err := vpn1.Start(ctx); err != nil {
		t.Fatalf("start vpn1: %v", err)
	}
	defer vpn1.Stop()

	svc, err := vpn1.ExposeService("webapp", backendAddr, "Test Web Application", nil)
	if err != nil {
		t.Fatalf("expose service on vpn1: %v", err)
	}
	if svc.Name != "webapp" {
		t.Fatalf("unexpected service name %s", svc.Name)
	}

	// 4. Start MemVPN Service on Node 2 & Forward "webapp"
	meshConfig2 := meshConfig
	meshConfig2.WGListenPort = 51893
	vpn2 := NewService(h2, meshConfig2)
	if err := vpn2.Start(ctx); err != nil {
		t.Fatalf("start vpn2: %v", err)
	}
	defer vpn2.Stop()

	// Allocate free local port for forwarder
	forwarder, err := vpn2.ForwardService(ctx, "127.0.0.1:0", h1.ID(), "webapp")
	if err != nil {
		t.Fatalf("forward service on vpn2: %v", err)
	}
	defer vpn2.UnforwardService(forwarder.LocalAddr)

	// 5. Test HTTP request through Node 2's local forwarder port
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/", forwarder.LocalAddr))
	if err != nil {
		t.Fatalf("HTTP GET via MemVPN forwarder failed: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	if string(bodyBytes) != expectedBody {
		t.Fatalf("response body mismatch: got %q, want %q", string(bodyBytes), expectedBody)
	}
}

func TestNATRouterPacketConstructionAndChecksums(t *testing.T) {
	srcIP := net.ParseIP("10.42.0.2")
	dstIP := net.ParseIP("1.1.1.1")
	payload := []byte("hello-vpn-traffic")

	// 1. Build and verify UDP packet
	udpPkt := buildIPv4UDPPacket(srcIP, dstIP, 54321, 53, payload)
	if len(udpPkt) != 20+8+len(payload) {
		t.Fatalf("unexpected UDP packet length: %d", len(udpPkt))
	}
	if udpPkt[0] != 0x45 || udpPkt[9] != 17 {
		t.Fatalf("invalid IPv4 header in UDP packet")
	}

	// 2. Build and verify TCP packet
	tcpPkt := buildIPv4TCPPacket(srcIP, dstIP, 54321, 443, 100, 200, 0x18, payload)
	if len(tcpPkt) != 20+20+len(payload) {
		t.Fatalf("unexpected TCP packet length: %d", len(tcpPkt))
	}
	if tcpPkt[0] != 0x45 || tcpPkt[9] != 6 {
		t.Fatalf("invalid IPv4 header in TCP packet")
	}

	// Verify Checksums are computed and non-zero
	ipChecksum := binary.BigEndian.Uint16(tcpPkt[10:12])
	if ipChecksum == 0 {
		t.Errorf("expected non-zero IP checksum")
	}
	tcpChecksum := binary.BigEndian.Uint16(tcpPkt[36:38])
	if tcpChecksum == 0 {
		t.Errorf("expected non-zero TCP checksum")
	}
}

func TestWireGuardStatePersistenceAcrossRestarts(t *testing.T) {
	tempDir := t.TempDir()
	stats := &TrafficStats{}

	// 1. First run: create server and add a device
	srv1, err := NewWGServer(51895, stats, tempDir)
	if err != nil {
		t.Fatalf("first NewWGServer failed: %v", err)
	}
	serverPubKey1 := srv1.PublicKey()

	prof1, err := srv1.AddDevice("test-phone")
	if err != nil {
		t.Fatalf("AddDevice failed: %v", err)
	}
	srv1.Stop()

	// 2. Second run: recreate server with same state directory
	srv2, err := NewWGServer(51895, stats, tempDir)
	if err != nil {
		t.Fatalf("second NewWGServer failed: %v", err)
	}
	defer srv2.Stop()

	// Server public key must match exactly across restarts!
	if srv2.PublicKey() != serverPubKey1 {
		t.Fatalf("server public key did not persist: got %s, want %s", srv2.PublicKey(), serverPubKey1)
	}

	// Device profile must match and not require new key generation!
	prof2, err := srv2.GetProfile("test-phone")
	if err != nil {
		t.Fatalf("GetProfile on restored device failed: %v", err)
	}
	if prof2.VirtualIP != prof1.VirtualIP {
		t.Errorf("virtual IP mismatch: got %s, want %s", prof2.VirtualIP, prof1.VirtualIP)
	}
	if prof2.ClientPubKey != prof1.ClientPubKey {
		t.Errorf("client public key mismatch: got %s, want %s", prof2.ClientPubKey, prof1.ClientPubKey)
	}
}

func TestExitNodeSwarmContributionTracking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. External target server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("HELLO_FROM_EXTERNAL_INTERNET"))
	}))
	defer ts.Close()

	tsAddr := ts.Listener.Addr().(*net.TCPAddr)

	// 2. Spin up Host A (Exit Provider)
	hA, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("hA failed: %v", err)
	}
	defer hA.Close()

	svcA := NewService(hA, MeshConfig{
		MeshID:       "test-mesh",
		NodeName:     "provider-a",
		VirtualIP:    "10.42.0.1",
		IsExitNode:   true,
		ExitAllowAll: true,
	})
	if err := svcA.Start(ctx); err != nil {
		t.Fatalf("svcA start failed: %v", err)
	}
	defer svcA.Stop()
	svcA.exitMgr.SetPolicy(ExitPolicy{
		AllowAll:        true,
		BlockPrivateIPs: false,
	})

	// 3. Spin up Host B (Client Node)
	hB, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("hB failed: %v", err)
	}
	defer hB.Close()

	svcB := NewService(hB, MeshConfig{
		MeshID:       "test-mesh",
		NodeName:     "client-b",
		VirtualIP:    "10.42.0.2",
		SelectedExit: hA.ID().String(),
	})
	if err := svcB.Start(ctx); err != nil {
		t.Fatalf("svcB start failed: %v", err)
	}
	defer svcB.Stop()

	// Connect peers
	hB.Peerstore().AddAddrs(hA.ID(), hA.Addrs(), time.Hour)

	// 4. Dial target through Node A's exit stream from Node B
	conn, err := svcB.DialExitTarget(ctx, tsAddr.IP.String(), tsAddr.Port)
	if err != nil {
		t.Fatalf("DialExitTarget failed: %v", err)
	}
	defer conn.Close()

	// Send HTTP request through the tunnel
	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", tsAddr.String())
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	respBytes, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(string(respBytes), "HELLO_FROM_EXTERNAL_INTERNET") {
		t.Fatalf("unexpected response: %s", string(respBytes))
	}

	// 5. Verify Node A's swarm contribution telemetry is incremented!
	statusA := svcA.GetStatus()
	if statusA.Stats.ContributedConns < 1 {
		t.Errorf("expected ContributedConns >= 1, got %d", statusA.Stats.ContributedConns)
	}
	if statusA.Stats.ContributedBytesSent <= 0 || statusA.Stats.ContributedBytesRecv <= 0 {
		t.Errorf("expected non-zero contributed bytes: sent=%d recv=%d", statusA.Stats.ContributedBytesSent, statusA.Stats.ContributedBytesRecv)
	}
}

