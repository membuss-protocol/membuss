package memgate_v2

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/libp2p/go-libp2p/core/peer"
)

// handleWireGuardConf serves raw WireGuard .conf file for 1-click device download.
func (g *MemGate) handleWireGuardConf(w http.ResponseWriter, r *http.Request) {
	if g.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	deviceName := r.URL.Query().Get("device")
	if deviceName == "" {
		deviceName = "default"
	}

	profile, err := g.cfg.VPNService.GetWireGuardProfile(deviceName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get profile: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.conf"`, profile.DeviceName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(profile.ConfigText))
}

// handleVPNProxy reverse proxies HTTP requests to a MemVPN exposed service.
func (g *MemGate) handleVPNProxy(w http.ResponseWriter, r *http.Request) {
	if g.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	peerParam := chi.URLParam(r, "peer")
	serviceName := chi.URLParam(r, "service")
	restPath := chi.URLParam(r, "*")

	if peerParam == "" || serviceName == "" {
		http.Error(w, "Peer ID and Service Name are required", http.StatusBadRequest)
		return
	}

	// 1. Local service route
	if peerParam == "local" || peerParam == "self" {
		services := g.cfg.VPNService.GetStatus().Services
		for _, s := range services {
			if s.Name == serviceName {
				g.proxyToTCP(w, r, s.TargetAddr, restPath)
				return
			}
		}
		http.Error(w, fmt.Sprintf("Local service %q not found", serviceName), http.StatusNotFound)
		return
	}

	// 2. Remote peer ID resolution
	targetPeer, err := peer.Decode(peerParam)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid peer ID %q: %v", peerParam, err), http.StatusBadRequest)
		return
	}

	// 3. Dial remote service over libp2p mesh
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	conn, err := g.cfg.VPNService.DialServiceStream(ctx, targetPeer, serviceName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to connect to mesh service %s on %s: %v", serviceName, targetPeer.String(), err), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	// 4. Forward HTTP request
	outReq := r.Clone(ctx)
	if restPath != "" {
		if !strings.HasPrefix(restPath, "/") {
			restPath = "/" + restPath
		}
		outReq.URL.Path = restPath
	} else {
		outReq.URL.Path = "/"
	}
	outReq.URL.RawPath = ""
	outReq.RequestURI = ""

	if err := outReq.Write(conn); err != nil {
		http.Error(w, fmt.Sprintf("Error writing to VPN stream: %v", err), http.StatusBadGateway)
		return
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), outReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading from VPN stream: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Membuss-VPN-Peer", targetPeer.String())
	w.Header().Set("X-Membuss-VPN-Service", serviceName)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (g *MemGate) proxyToTCP(w http.ResponseWriter, r *http.Request, targetAddr, restPath string) {
	conn, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to dial local target %s: %v", targetAddr, err), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	outReq := r.Clone(r.Context())
	if restPath != "" {
		if !strings.HasPrefix(restPath, "/") {
			restPath = "/" + restPath
		}
		outReq.URL.Path = restPath
	} else {
		outReq.URL.Path = "/"
	}
	outReq.URL.RawPath = ""
	outReq.RequestURI = ""

	if err := outReq.Write(conn); err != nil {
		http.Error(w, fmt.Sprintf("Error writing to local target: %v", err), http.StatusBadGateway)
		return
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), outReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading from local target: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
