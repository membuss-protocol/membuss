package explorer

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/libp2p/go-libp2p/core/peer"
)

// handleVPNStatus returns aggregate VPN status and telemetry.
func (e *Explorer) handleVPNStatus(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	status := e.cfg.VPNService.GetStatus()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// handleWireGuardProfile returns the WireGuard profile & config text for a client device.
func (e *Explorer) handleWireGuardProfile(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	device := r.URL.Query().Get("device")
	if device == "" {
		device = "default"
	}

	profile, err := e.cfg.VPNService.GetWireGuardProfile(device)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get profile: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(profile)
}

// handleWireGuardDevices lists all registered WireGuard client devices.
func (e *Explorer) handleWireGuardDevices(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	devices := e.cfg.VPNService.ListWireGuardDevices()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(devices)
}

// handleWireGuardAddDevice registers a new WireGuard client device.
func (e *Explorer) handleWireGuardAddDevice(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	profile, err := e.cfg.VPNService.AddWireGuardDevice(req.Name)
	if err != nil {
		http.Error(w, fmt.Sprintf("Add device failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(profile)
}

// handleWireGuardDeleteDevice unregisters a client device.
func (e *Explorer) handleWireGuardDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		id = req.ID
	}

	if id == "" {
		http.Error(w, "device id or name required", http.StatusBadRequest)
		return
	}

	if err := e.cfg.VPNService.DeleteWireGuardDevice(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleWireGuardDownloadConfig exports the raw .conf file for desktop/mobile clients.
func (e *Explorer) handleWireGuardDownloadConfig(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	device := r.URL.Query().Get("device")
	if device == "" {
		device = "default"
	}

	profile, err := e.cfg.VPNService.GetWireGuardProfile(device)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.conf"`, profile.DeviceName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(profile.ConfigText))
}

// handleVPNSelectExit sets the active Exit Node.
func (e *Explorer) handleVPNSelectExit(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		PeerID     string `json:"peer_id"`
		ExitPeerID string `json:"exit_peer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	targetPeer := req.PeerID
	if targetPeer == "" && req.ExitPeerID != "" {
		targetPeer = req.ExitPeerID
	}

	if err := e.cfg.VPNService.SelectExitNode(targetPeer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "selected_exit": targetPeer})
}

// handleVPNToggleExit toggles exit node provider mode on this host.
func (e *Explorer) handleVPNToggleExit(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Enabled  bool  `json:"enabled"`
		AllowAll *bool `json:"allow_all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	allowAll := true
	if req.AllowAll != nil {
		allowAll = *req.AllowAll
	}

	if err := e.cfg.VPNService.ToggleExitNode(req.Enabled, allowAll); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "is_exit_node": req.Enabled})
}

// handleVPNExposeService exposes a local port to the P2P mesh.
func (e *Explorer) handleVPNExposeService(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Name         string   `json:"name"`
		TargetAddr   string   `json:"target_addr"`
		Description  string   `json:"description"`
		AllowedPeers []string `json:"allowed_peers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	svc, err := e.cfg.VPNService.ExposeService(req.Name, req.TargetAddr, req.Description, req.AllowedPeers)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(svc)
}

// handleVPNUnexposeService removes an exposed local service.
func (e *Explorer) handleVPNUnexposeService(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := e.cfg.VPNService.UnexposeService(req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleVPNForwardService binds a local port to a remote peer's service.
func (e *Explorer) handleVPNForwardService(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		LocalAddr     string `json:"local_addr"`
		RemotePeerID  string `json:"remote_peer_id"`
		RemoteService string `json:"remote_service"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	targetPeer, err := peer.Decode(req.RemotePeerID)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid peer ID: %v", err), http.StatusBadRequest)
		return
	}

	pf, err := e.cfg.VPNService.ForwardService(r.Context(), req.LocalAddr, targetPeer, req.RemoteService)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pf)
}

// handleVPNUnforwardService stops a local port forwarder.
func (e *Explorer) handleVPNUnforwardService(w http.ResponseWriter, r *http.Request) {
	if e.cfg.VPNService == nil {
		http.Error(w, "MemVPN service not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		LocalAddr string `json:"local_addr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := e.cfg.VPNService.UnforwardService(req.LocalAddr); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
