package erasure

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

// ManifestMetaKey returns the metadata key shape "erasure/<mid>".
func ManifestMetaKey(m mid.MID) string {
	return "erasure/" + m.String()
}

// SetManifest writes the ErasureManifest protobuf to the store's metadata table.
func SetManifest(s store.Blockstore, m mid.MID, manifest *membusspb.ErasureManifest) error {
	if s == nil {
		return errors.New("erasure: nil store")
	}
	if m.IsZero() {
		return errors.New("erasure: zero mid")
	}
	if manifest == nil {
		return nil
	}
	buf, err := proto.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("erasure: marshal manifest: %w", err)
	}
	return s.PutMeta(ManifestMetaKey(m), buf)
}

// GetManifest reads the ErasureManifest protobuf for a MID from the store's metadata table.
func GetManifest(s store.Blockstore, m mid.MID) (*membusspb.ErasureManifest, error) {
	if s == nil {
		return nil, errors.New("erasure: nil store")
	}
	if m.IsZero() {
		return nil, errors.New("erasure: zero mid")
	}
	raw, err := s.GetMeta(ManifestMetaKey(m))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var manifest membusspb.ErasureManifest
	if err := proto.Unmarshal(raw, &manifest); err != nil {
		return nil, nil
	}
	return &manifest, nil
}
