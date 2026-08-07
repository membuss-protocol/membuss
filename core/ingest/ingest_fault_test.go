package ingest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

type failingStore struct {
	store.Store
	failObjectInfo bool
	failSeal       bool
}

func (f *failingStore) PutMeta(key string, value []byte) error {
	if f.failObjectInfo {
		return errors.New("simulated objectinfo db error")
	}
	return f.Store.PutMeta(key, value)
}

func (f *failingStore) Seal(m mid.MID, recursive bool) error {
	if f.failSeal {
		return errors.New("simulated seal db error")
	}
	return f.Store.Seal(m, recursive)
}

func TestIngestFaultInjection(t *testing.T) {
	memStore, err := store.NewMemStore(store.Options{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer memStore.Close()

	payload := []byte("fault injection test payload bytes")

	t.Run("SetObjectInfo Failure Propagates Error", func(t *testing.T) {
		fs := &failingStore{Store: memStore, failObjectInfo: true}
		_, err := IngestFile(context.Background(), fs, bytes.NewReader(payload), Options{
			Name: "fail_obj.txt",
			Seal: true,
		})
		if err == nil {
			t.Fatal("expected error when SetObjectInfo fails, got nil")
		}
	})

	t.Run("Seal Failure Propagates Error", func(t *testing.T) {
		fs := &failingStore{Store: memStore, failSeal: true}
		_, err := IngestFile(context.Background(), fs, bytes.NewReader(payload), Options{
			Name: "fail_seal.txt",
			Seal: true,
		})
		if err == nil {
			t.Fatal("expected error when Seal fails, got nil")
		}
	})
}
