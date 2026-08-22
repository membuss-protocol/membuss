// Fuzz target for keyring PEM import parsing (finding.txt XC-001).
// Import() runs attacker-controlled PEM bytes through pem.Decode
// and libp2p key unmarshalling before any disk IO; this target
// exercises exactly that parse prefix.
package keyring

import (
	"testing"

	"encoding/pem"
	"github.com/libp2p/go-libp2p/core/crypto"
)

func FuzzKeyImportParse(f *testing.F) {
	f.Add([]byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"))
	f.Add([]byte{0x00})
	f.Add([]byte("-----BEGIN CERTIFICATE-----\nZm9v\n-----END CERTIFICATE-----\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		block, _ := pem.Decode(data)
		if block == nil {
			return
		}
		if block.Type != "PRIVATE KEY" {
			return
		}
		_, _ = crypto.UnmarshalPrivateKey(block.Bytes)
	})
}
