package anchor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/nnlgsakib/membuss/core/mid"
)

type mockStream struct {
	network.Stream
	reader      io.Reader
	writer      io.Writer
	resetCalled bool
	closeCalled bool
}

func (m *mockStream) Read(p []byte) (int, error) {
	if m.reader == nil {
		return 0, io.EOF
	}
	return m.reader.Read(p)
}

func (m *mockStream) Write(p []byte) (int, error) {
	if m.writer == nil {
		return 0, errors.New("write error")
	}
	return m.writer.Write(p)
}

func (m *mockStream) Reset() error {
	m.resetCalled = true
	return nil
}

func (m *mockStream) Close() error {
	m.closeCalled = true
	return nil
}

type errStore struct {
	fakeStore
}

func (e *errStore) AllSealed() ([]mid.MID, error) {
	return nil, errors.New("db query error")
}

type errWriter struct{}

func (w *errWriter) Write(p []byte) (int, error) {
	return 0, errors.New("network write failure")
}

func TestHandleStream_Success(t *testing.T) {
	h := newTestHost(t)
	t.Cleanup(func() { _ = h.Close() })

	store := newFakeStore()
	m := mid.FromBytes([]byte("test-data"))
	_ = store.Seal(m, true)

	cp := &ContentPublisher{
		host:  h,
		store: store,
	}
	buf := &bytes.Buffer{}
	stream := &mockStream{writer: buf}

	cp.handleStream(stream)

	if !stream.closeCalled {
		t.Errorf("expected s.Close() to be called on success")
	}
	if stream.resetCalled {
		t.Errorf("expected s.Reset() NOT to be called on success")
	}
}

func TestHandleStream_StoreError(t *testing.T) {
	h := newTestHost(t)
	t.Cleanup(func() { _ = h.Close() })

	cp := &ContentPublisher{
		host:  h,
		store: &errStore{},
	}
	buf := &bytes.Buffer{}
	stream := &mockStream{writer: buf}

	cp.handleStream(stream)

	if !stream.resetCalled {
		t.Errorf("expected s.Reset() to be called on store error")
	}
	if stream.closeCalled {
		t.Errorf("expected s.Close() NOT to be called on store error")
	}
}

func TestHandleStream_WriteError(t *testing.T) {
	h := newTestHost(t)
	t.Cleanup(func() { _ = h.Close() })

	store := newFakeStore()
	cp := &ContentPublisher{
		host:  h,
		store: store,
	}
	stream := &mockStream{writer: &errWriter{}}

	cp.handleStream(stream)

	if !stream.resetCalled {
		t.Errorf("expected s.Reset() to be called on write error")
	}
	if stream.closeCalled {
		t.Errorf("expected s.Close() NOT to be called on write error")
	}
}

func TestFetchPeerSealed_DecodeError(t *testing.T) {
	ctx := context.Background()
	h1 := newTestHost(t)
	h2 := newTestHost(t)
	t.Cleanup(func() {
		_ = h1.Close()
		_ = h2.Close()
	})

	var streamMu sync.Mutex
	var handledStream network.Stream
	h2.SetStreamHandler(ContentExchangeProto, func(s network.Stream) {
		streamMu.Lock()
		handledStream = s
		streamMu.Unlock()
		_, _ = s.Write([]byte("invalid-json{"))
		_ = s.Close()
	})

	_ = h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})

	_, err := fetchPeerSealed(ctx, h1, h2.ID())
	if err == nil {
		t.Fatalf("expected error decoding invalid json, got nil")
	}

	streamMu.Lock()
	_ = handledStream
	streamMu.Unlock()
}

func TestFetchPeerSealed_Success(t *testing.T) {
	ctx := context.Background()
	h1 := newTestHost(t)
	h2 := newTestHost(t)
	t.Cleanup(func() {
		_ = h1.Close()
		_ = h2.Close()
	})

	store := newFakeStore()
	m := mid.FromBytes([]byte("test-data"))
	_ = store.Seal(m, true)
	cp := NewContentPublisher(h2, store)
	cp.Start(ctx)
	t.Cleanup(func() { cp.Stop() })

	_ = h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})

	mids, err := fetchPeerSealed(ctx, h1, h2.ID())
	if err != nil {
		t.Fatalf("fetchPeerSealed failed: %v", err)
	}
	if len(mids) == 0 {
		t.Fatalf("expected sealed MIDs from peer, got 0")
	}
}

func TestFetchPeerSealed_LargePayload(t *testing.T) {
	ctx := context.Background()
	h1 := newTestHost(t)
	h2 := newTestHost(t)
	t.Cleanup(func() {
		_ = h1.Close()
		_ = h2.Close()
	})

	store := newFakeStore()
	// Generate 20,000 unique MIDs (~1.34 MiB payload, exceeding previous 1 MiB limit)
	numMIDs := 20000
	for i := 0; i < numMIDs; i++ {
		m := mid.FromBytes([]byte{
			byte(i), byte(i >> 8), byte(i >> 16), byte(i >> 24),
			0, 0, 0, 0,
		})
		_ = store.Seal(m, true)
	}

	cp := NewContentPublisher(h2, store)
	cp.Start(ctx)
	t.Cleanup(func() { cp.Stop() })

	_ = h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})

	mids, err := fetchPeerSealed(ctx, h1, h2.ID())
	if err != nil {
		t.Fatalf("fetchPeerSealed failed on large payload (%d MIDs): %v", numMIDs, err)
	}
	if len(mids) != numMIDs {
		t.Fatalf("expected %d MIDs, got %d", numMIDs, len(mids))
	}
}

type mockDHTProvider struct {
	mu       sync.Mutex
	provided []mid.MID
}

func (m *mockDHTProvider) Provide(ctx context.Context, id mid.MID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.provided = append(m.provided, id)
	return nil
}

func TestContentPublisher_PublishToDHT(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	m1 := mid.FromBytes([]byte("mid-1"))
	m2 := mid.FromBytes([]byte("mid-2"))
	_ = store.Seal(m1, true)
	_ = store.Seal(m2, true)

	dht := &mockDHTProvider{}
	cp := NewContentPublisher(nil, store, dht)

	cp.publishToDHT(ctx)

	dht.mu.Lock()
	defer dht.mu.Unlock()
	if len(dht.provided) != 2 {
		t.Fatalf("expected 2 MIDs published to DHT, got %d", len(dht.provided))
	}
}

func TestDiscoverContent_Throttling(t *testing.T) {
	ctx := context.Background()
	h1 := newTestHost(t)
	t.Cleanup(func() { _ = h1.Close() })

	numPeers := 20
	for i := 0; i < numPeers; i++ {
		h2 := newTestHost(t)
		t.Cleanup(func() { _ = h2.Close() })
		store := newFakeStore()
		m := mid.FromBytes([]byte{byte(i + 1)})
		_ = store.Seal(m, true)
		cp := NewContentPublisher(h2, store)
		cp.Start(ctx)
		t.Cleanup(func() { cp.Stop() })
		_ = h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})
	}

	known := make(map[string]struct{})
	announcements, err := DiscoverContent(ctx, h1, known)
	if err != nil {
		t.Fatalf("DiscoverContent failed: %v", err)
	}
	if len(announcements) != numPeers {
		t.Fatalf("expected %d announcements, got %d", numPeers, len(announcements))
	}
}



