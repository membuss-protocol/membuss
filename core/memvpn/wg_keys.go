package memvpn

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// Key represents a 32-byte WireGuard cryptographic key.
type Key [32]byte

// String returns the base64-encoded WireGuard key string.
func (k Key) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// ParseKey decodes a base64-encoded WireGuard key string.
func ParseKey(s string) (Key, error) {
	var k Key
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return k, fmt.Errorf("invalid base64 key: %w", err)
	}
	if len(b) != 32 {
		return k, fmt.Errorf("invalid key length %d (expected 32)", len(b))
	}
	copy(k[:], b)
	return k, nil
}

// GeneratePrivateKey generates a new random Curve25519 private key.
func GeneratePrivateKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return k, err
	}
	// Clamp private key according to Curve25519 spec
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return k, nil
}

// PublicKey derives the public key from a private key.
func (k Key) PublicKey() Key {
	var pub Key
	curve25519.ScalarBaseMult((*[32]byte)(&pub), (*[32]byte)(&k))
	return pub
}

// GeneratePreSharedKey generates a 32-byte symmetric pre-shared key.
func GeneratePreSharedKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return k, err
	}
	return k, nil
}

// GenerateKeyPair generates a Curve25519 private and public key pair in standard base64 format.
func GenerateKeyPair() (privateKey string, publicKey string, err error) {
	priv, err := GeneratePrivateKey()
	if err != nil {
		return "", "", err
	}
	pub := priv.PublicKey()
	return priv.String(), pub.String(), nil
}

// GetOutboundIP resolves the host's primary local LAN IP address (e.g. 192.168.1.50)
// so other mobile devices and laptops on the same Wi-Fi network can connect.
func GetOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		// Fallback: iterate local non-loopback network interfaces
		addrs, err := net.InterfaceAddrs()
		if err == nil {
			for _, addr := range addrs {
				if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
					if ipNet.IP.To4() != nil {
						return ipNet.IP.String()
					}
				}
			}
		}
		return "127.0.0.1"
	}
	defer conn.Close()

	localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || localAddr.IP == nil {
		return "127.0.0.1"
	}
	return localAddr.IP.String()
}

// FormatWireGuardConfig produces the standard WireGuard .conf file text for a client device.
func FormatWireGuardConfig(clientPrivKey, clientVirtualIP, serverPubKey, serverEndpoint, dnsServers string) string {
	if dnsServers == "" {
		dnsServers = "1.1.1.1, 8.8.8.8"
	}
	if !strings.Contains(clientVirtualIP, "/") {
		clientVirtualIP += "/24"
	}

	return fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s
DNS = %s

[Peer]
PublicKey = %s
Endpoint = %s
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 15
`, strings.TrimSpace(clientPrivKey), strings.TrimSpace(clientVirtualIP), dnsServers, strings.TrimSpace(serverPubKey), strings.TrimSpace(serverEndpoint))
}
