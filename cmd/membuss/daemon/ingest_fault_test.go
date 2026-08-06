package daemon

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

type failingDaemonStore struct {
	store.Store
	failObjectInfo bool
	failSeal       bool
}

func (f *failingDaemonStore) PutMeta(key string, value []byte) error {
	if f.failObjectInfo {
		return errors.New("simulated objectinfo db failure")
	}
	return f.Store.PutMeta(key, value)
}

func (f *failingDaemonStore) Seal(m mid.MID, recursive bool) error {
	if f.failSeal {
		return errors.New("simulated seal db failure")
	}
	return f.Store.Seal(m, recursive)
}

func TestDaemonAdapterIngestFaultInjection(t *testing.T) {
	memStore, err := store.NewMemStore(store.Options{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer memStore.Close()

	payload := []byte("daemon fault injection test payload")

	t.Run("API Adapter Propagates Ingest Error On ObjectInfo Failure", func(t *testing.T) {
		fs := &failingDaemonStore{Store: memStore, failObjectInfo: true}
		b := &daemonBackend{store: fs}
		apiAdap := newAPIAdapter(b)

		_, err := apiAdap.AddFile(context.Background(), "test.txt", bytes.NewReader(payload), false)
		if err == nil {
			t.Fatal("expected API AddFile to return error when ObjectInfo fails, got nil")
		}
	})

	t.Run("Explorer Adapter Propagates Ingest Error On ObjectInfo Failure", func(t *testing.T) {
		fs := &failingDaemonStore{Store: memStore, failObjectInfo: true}
		b := &daemonBackend{store: fs}
		expAdap := newExplorerAdapter(b, false, nil, nil)

		_, err := expAdap.Add(context.Background(), "test.txt", bytes.NewReader(payload))
		if err == nil {
			t.Fatal("expected Explorer Add to return error when ObjectInfo fails, got nil")
		}
	})

	t.Run("Explorer TrackRootWithMetadata Propagates ObjectInfo Failure", func(t *testing.T) {
		fs := &failingDaemonStore{Store: memStore, failObjectInfo: true}
		b := &daemonBackend{store: fs}
		expAdap := newExplorerAdapter(b, false, nil, nil)

		m, _ := mid.Parse("membafzb4ifssntvtapppeeduiwf7qak6iw7r6l24ew4urmq5p2bwotmzjrssy")
		err := expAdap.TrackRootWithMetadata(m, "test.txt", "text/plain", 100)
		if err == nil {
			t.Fatal("expected TrackRootWithMetadata to return error when ObjectInfo fails, got nil")
		}
	})
}
