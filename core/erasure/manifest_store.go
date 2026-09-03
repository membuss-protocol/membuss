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

// ManifestRootKey returns the metadata key shape "erasure_root/<mid>"
// mapping an erasure leaf back to the root it was ingested under.
func ManifestRootKey(leaf mid.MID) string {
	return "erasure_root/" + leaf.String()
}

// SetManifestRoot records that leaf was ingested under root.
func SetManifestRoot(s store.Blockstore, leaf, root mid.MID) error {
	if s == nil {
		return errors.New("erasure: nil store")
	}
	if leaf.IsZero() || root.IsZero() {
		return errors.New("erasure: zero mid")
	}
	return s.PutMeta(ManifestRootKey(leaf), []byte(root.String()))
}

// GetManifestRoot reads the ingest root recorded for leaf. Returns
// (MID{}, nil) when no linkage row exists.
func GetManifestRoot(s store.Blockstore, leaf mid.MID) (mid.MID, error) {
	if s == nil {
		return mid.MID{}, errors.New("erasure: nil store")
	}
	if leaf.IsZero() {
		return mid.MID{}, errors.New("erasure: zero mid")
	}
	raw, err := s.GetMeta(ManifestRootKey(leaf))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return mid.MID{}, nil
		}
		return mid.MID{}, err
	}
	root, perr := mid.Parse(string(raw))
	if perr != nil {
		return mid.MID{}, fmt.Errorf("erasure: parse stored root %q: %w", string(raw), perr)
	}
	return root, nil
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
		return nil, fmt.Errorf("erasure: unmarshal manifest: %w", err)
	}
	return &manifest, nil
}
