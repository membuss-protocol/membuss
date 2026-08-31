package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/libp2p/go-libp2p/core/peer"
)

// handleVPNStatus returns aggregate VPN status.
func (a *NodeAPI) handleVPNStatus(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
		return
	}
	ok(w, a.cfg.VPNService.GetStatus())
}

// handleWireGuardProfile returns the WireGuard profile & config for a device.
func (a *NodeAPI) handleWireGuardProfile(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
		return
	}

	device := r.URL.Query().Get("device")
	if device == "" {
		device = "default"
	}

	profile, err := a.cfg.VPNService.GetWireGuardProfile(device)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}

	ok(w, profile)
}

// handleWireGuardDevices returns all registered client devices.
func (a *NodeAPI) handleWireGuardDevices(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
		return
	}

	ok(w, a.cfg.VPNService.ListWireGuardDevices())
}

// handleWireGuardAddDevice registers a new client device.
func (a *NodeAPI) handleWireGuardAddDevice(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	profile, err := a.cfg.VPNService.AddWireGuardDevice(req.Name)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	ok(w, profile)
}

// handleWireGuardDeleteDevice unregisters a client device.
func (a *NodeAPI) handleWireGuardDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
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
		fail(w, http.StatusBadRequest, errors.New("device id or name required"))
		return
	}

	if err := a.cfg.VPNService.DeleteWireGuardDevice(id); err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}

	ok(w, map[string]bool{"deleted": true})
}

// handleWireGuardDownloadConfig serves raw .conf text.
func (a *NodeAPI) handleWireGuardDownloadConfig(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
		return
	}

	device := r.URL.Query().Get("device")
	if device == "" {
		device = "default"
	}

	profile, err := a.cfg.VPNService.GetWireGuardProfile(device)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.conf"`, profile.DeviceName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(profile.ConfigText))
}

// handleVPNSelectExit updates the active exit node.
func (a *NodeAPI) handleVPNSelectExit(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
		return
	}

	var req struct {
		PeerID     string `json:"peer_id"`
		ExitPeerID string `json:"exit_peer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	targetPeer := req.PeerID
	if targetPeer == "" && req.ExitPeerID != "" {
		targetPeer = req.ExitPeerID
	}

	if err := a.cfg.VPNService.SelectExitNode(targetPeer); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}

	ok(w, map[string]any{"selected_exit": targetPeer})
}

// handleVPNToggleExit toggles exit node provider mode.
func (a *NodeAPI) handleVPNToggleExit(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
		return
	}

	var req struct {
		Enabled  bool  `json:"enabled"`
		AllowAll *bool `json:"allow_all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	allowAll := true
	if req.AllowAll != nil {
		allowAll = *req.AllowAll
	}

	if err := a.cfg.VPNService.ToggleExitNode(req.Enabled, allowAll); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	ok(w, map[string]any{"is_exit_node": req.Enabled})
}

// handleVPNExposeService exposes a local service.
func (a *NodeAPI) handleVPNExposeService(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
		return
	}

	var req struct {
		Name         string   `json:"name"`
		TargetAddr   string   `json:"target_addr"`
		Description  string   `json:"description"`
		AllowedPeers []string `json:"allowed_peers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	svc, err := a.cfg.VPNService.ExposeService(req.Name, req.TargetAddr, req.Description, req.AllowedPeers)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}

	ok(w, svc)
}

// handleVPNUnexposeService removes an exposed service.
func (a *NodeAPI) handleVPNUnexposeService(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	if err := a.cfg.VPNService.UnexposeService(req.Name); err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}

	ok(w, map[string]bool{"unexposed": true})
}

// handleVPNForwardService binds a local port to a remote peer's service.
func (a *NodeAPI) handleVPNForwardService(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
		return
	}

	var req struct {
		LocalAddr     string `json:"local_addr"`
		RemotePeerID  string `json:"remote_peer_id"`
		RemoteService string `json:"remote_service"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	targetPeer, err := peer.Decode(req.RemotePeerID)
	if err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("invalid peer ID: %w", err))
		return
	}

	pf, err := a.cfg.VPNService.ForwardService(r.Context(), req.LocalAddr, targetPeer, req.RemoteService)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	ok(w, pf)
}

// handleVPNUnforwardService stops a local port forwarder.
func (a *NodeAPI) handleVPNUnforwardService(w http.ResponseWriter, r *http.Request) {
	if a.cfg.VPNService == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("MemVPN service not available"))
		return
	}

	var req struct {
		LocalAddr string `json:"local_addr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	if err := a.cfg.VPNService.UnforwardService(req.LocalAddr); err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}

	ok(w, map[string]bool{"unforwarded": true})
}
