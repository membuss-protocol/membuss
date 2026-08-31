package memvpn

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
)

// ACL handles authentication, authorization, and cryptographic token verification.
type ACL struct {
	cfg *MeshConfig
	mu  sync.RWMutex
}

// NewACL constructs a new ACL manager.
func NewACL(cfg *MeshConfig) *ACL {
	return &ACL{cfg: cfg}
}

// GenerateAuthToken generates an HMAC-SHA256 authentication token for a peer.
func GenerateAuthToken(peerID peer.ID, meshID, psk string) string {
	mac := hmac.New(sha256.New, []byte(psk))
	mac.Write([]byte(fmt.Sprintf("%s:%s", peerID.String(), meshID)))
	return hex.EncodeToString(mac.Sum(nil))
}

// ValidateMeshAuth validates credentials for a peer attempting to join the mesh.
func (a *ACL) ValidateMeshAuth(peerID peer.ID, meshID, token string) error {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.cfg.MeshID != "" && meshID != a.cfg.MeshID {
		return fmt.Errorf("mesh ID mismatch: expected %q, got %q", a.cfg.MeshID, meshID)
	}

	if !a.cfg.AllowAllPeers {
		allowed := false
		for _, p := range a.cfg.AllowedPeers {
			if p == peerID.String() {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("peer %s is not in the allowed list", peerID.String())
		}
	}

	if a.cfg.PreSharedKey != "" {
		expectedToken := GenerateAuthToken(peerID, meshID, a.cfg.PreSharedKey)
		if !hmac.Equal([]byte(token), []byte(expectedToken)) {
			return errors.New("invalid preshared key authentication token")
		}
	}

	return nil
}

// AuthorizePeer checks if a peer is permitted to interact with this node.
func (a *ACL) AuthorizePeer(peerID peer.ID) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.cfg.AllowAllPeers {
		return true
	}
	for _, p := range a.cfg.AllowedPeers {
		if p == peerID.String() {
			return true
		}
	}
	return false
}

// CheckExitAuthorization verifies whether a peer is allowed to route exit internet traffic.
func (a *ACL) CheckExitAuthorization(peerID peer.ID, policy *ExitPolicy) error {
	if policy.AllowAll {
		return nil
	}
	for _, p := range policy.AllowedPeers {
		if p == peerID.String() {
			return nil
		}
	}
	return fmt.Errorf("peer %s is not authorized for exit routing", peerID.String())
}
