package anchor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/nnlgsakib/membuss/core/mid"
)

// fakeStore is a minimal AnchorStore that records seal calls and
// lets a test control what the store already Has. It is deliberately
// tiny; only the methods fetchIfMissing/discovery touch are real.
type fakeStore struct {
	mu     sync.Mutex
	has    map[string]bool
	sealed map[string]bool
	meta   map[string][]byte
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		has:    make(map[string]bool),
		sealed: make(map[string]bool),
		meta:   make(map[string][]byte),
	}
}

func (s *fakeStore) Has(m mid.MID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.has[m.String()], nil
}

func (s *fakeStore) Seal(m mid.MID, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sealed[m.String()] = true
	s.has[m.String()] = true
	return nil
}

func (s *fakeStore) isSealed(m mid.MID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sealed[m.String()]
}

func (s *fakeStore) Put(m mid.MID, _ []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.has[m.String()] = true
	return nil
}

func (s *fakeStore) PutMeta(k string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta[k] = v
	return nil
}

func (s *fakeStore) GetMeta(k string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.meta[k]; ok {
		return v, nil
	}
	return nil, nil
}

func (s *fakeStore) Size() (uint64, error)                   { return 0, nil }
func (s *fakeStore) Close() error                            { return nil }
func (s *fakeStore) AllSealed() ([]mid.MID, error)           { return nil, nil }
func (s *fakeStore) AllBlocks() ([]mid.MID, error)           { return nil, nil }
func (s *fakeStore) Get(mid.MID) ([]byte, error)             { return nil, nil }
func (s *fakeStore) IterateBlocks(func(mid.MID) error) error { return nil }
func (s *fakeStore) IterateSealed(func(mid.MID) error) error { return nil }

// countingFetcher records the providers passed to each Fetch call so
// a test can assert whether the source peer was offered.
type countingFetcher struct {
	mu        sync.Mutex
	calls     int
	lastProvs []peer.AddrInfo
	err       error
}

func (f *countingFetcher) Fetch(_ context.Context, _ mid.MID, provs []peer.AddrInfo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastProvs = provs
	return f.err
}

// staticResolver returns a fixed provider set for any MID.
type staticResolver struct{ provs []peer.AddrInfo }

func (r *staticResolver) Resolve(context.Context, mid.MID) ([]peer.AddrInfo, error) {
	return r.provs, nil
}

func newFixEngine(store AnchorStore, fetcher Fetcher, resolver ProviderResolver) *AnchorEngine {
	return &AnchorEngine{
		cfg:         Config{Store: store, Fetcher: fetcher},
		logger:      nopLogger{},
		anchors:     make(map[peer.ID]peer.AddrInfo),
		attempts:    make(map[string]time.Time),
		sticky:      make(map[peer.ID]struct{}),
		healthFails: make(map[peer.ID]int),
		resolver:    resolver,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
}

// TestFetchIfMissing_HeldButUnsealedGetsSealed is the core bug: a MID
// whose bytes are already present but not sealed must be sealed
// (never re-fetched), so discovery stops reporting it forever.
func TestFetchIfMissing_HeldButUnsealedGetsSealed(t *testing.T) {
	m := mid.FromBytes([]byte("held-but-unsealed"))
	store := newFakeStore()
	store.has[m.String()] = true // present, but not sealed

	fetcher := &countingFetcher{}
	e := newFixEngine(store, fetcher, &staticResolver{})

	e.fetchIfMissing(context.Background(), m, "")

	if !store.isSealed(m) {
		t.Fatalf("held-but-unsealed MID was not sealed")
	}
	if fetcher.calls != 0 {
		t.Errorf("fetch was attempted for an already-held MID: %d calls", fetcher.calls)
	}
}

// TestFetchIfMissing_PrefersSourcePeer verifies the announcing peer is
// offered to the fetcher even when the resolver returns nothing, so a
// young network without DHT provider records can still converge.
func TestFetchIfMissing_PrefersSourcePeer(t *testing.T) {
	m := mid.FromBytes([]byte("from-source"))
	source, _ := peer.Decode("12D3KooWPjceQrSwdWXPyLLeABRXmuqt69Rg3sBYbU1Nft9HyQ6X")

	store := newFakeStore() // does not have it yet
	fetcher := &countingFetcher{}
	// Resolver returns no providers: only the source can supply it.
	e := newFixEngine(store, fetcher, &staticResolver{})
	// mergeSource reads the source's addrs from the host peerstore,
	// so a real host is needed to exercise the production path.
	h := newTestHost(t)
	t.Cleanup(func() { _ = h.Close() })
	e.cfg.Host = h

	e.fetchIfMissing(context.Background(), m, source)

	if fetcher.calls != 1 {
		t.Fatalf("expected exactly one fetch, got %d", fetcher.calls)
	}
	found := false
	for _, p := range fetcher.lastProvs {
		if p.ID == source {
			found = true
		}
	}
	if !found {
		t.Errorf("source peer was not offered to the fetcher")
	}
	if !store.isSealed(m) {
		t.Errorf("successfully fetched MID was not sealed")
	}
}

// TestEnqueueFrom_BacksOffRepeats verifies a MID attempted once is not
// re-queued within the backoff window, stopping the per-round churn
// and log spam, and that sealing clears the record so real progress is
// never suppressed.
func TestEnqueueFrom_BacksOffRepeats(t *testing.T) {
	m := mid.FromBytes([]byte("unreachable"))
	store := newFakeStore()
	e := newFixEngine(store, &countingFetcher{}, &staticResolver{})

	if !e.enqueueFrom(m, "") {
		t.Fatalf("first enqueue should succeed")
	}
	// Simulate the acquisition attempt stamping the attempt time.
	e.recordAttempt(m)

	if e.enqueueFrom(m, "") {
		t.Errorf("second enqueue within backoff should be suppressed")
	}

	// Once sealed/done, the record is cleared and it may queue again.
	e.markDone(m)
	if !e.enqueueFrom(m, "") {
		t.Errorf("enqueue after markDone should succeed")
	}
}
