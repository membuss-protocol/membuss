package shard

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nnlgsakib/membuss/core/db"
)

var keyRingPeers = []byte("shard:ring:peers")

// Store represents a key-value storage interface suitable for persisting ring metadata.
type Store interface {
	Get(key []byte) ([]byte, error)
	Set(key, value []byte) error
}

// PersistPeers serializes and saves the active ring peer list into persistent storage.
func PersistPeers(store Store, peers []string) error {
	if store == nil {
		return nil
	}
	data, err := json.Marshal(peers)
	if err != nil {
		return fmt.Errorf("shard: marshal ring peers: %w", err)
	}
	return store.Set(keyRingPeers, data)
}

// LoadPeers loads the persisted ring peer list from storage into the HashRing.
func LoadPeers(store Store, ring *HashRing) error {
	if store == nil || ring == nil {
		return nil
	}
	data, err := store.Get(keyRingPeers)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil
		}
		return err
	}
	var peers []string
	if err := json.Unmarshal(data, &peers); err != nil {
		return fmt.Errorf("shard: unmarshal ring peers: %w", err)
	}
	for _, p := range peers {
		ring.AddPeer(p)
	}
	return nil
}
