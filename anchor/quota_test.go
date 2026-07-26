package anchor

import (
	"testing"

	"github.com/nnlgsakib/membuss/core/mid"
)

type mockEvictableStore struct {
	items map[string]uint64
}

func newMockEvictableStore() *mockEvictableStore {
	return &mockEvictableStore{items: make(map[string]uint64)}
}

func (m *mockEvictableStore) Put(id mid.MID, size uint64) {
	m.items[id.String()] = size
}

func (m *mockEvictableStore) Delete(id mid.MID) error {
	delete(m.items, id.String())
	return nil
}

func (m *mockEvictableStore) Size() (uint64, error) {
	var total uint64
	for _, sz := range m.items {
		total += sz
	}
	return total, nil
}

func TestQuotaManager_LRUEviction(t *testing.T) {
	store := newMockEvictableStore()
	m1 := mid.FromBytes([]byte("mid-1"))
	m2 := mid.FromBytes([]byte("mid-2"))
	m3 := mid.FromBytes([]byte("mid-3"))

	store.Put(m1, 100)
	store.Put(m2, 100)
	store.Put(m3, 100)

	// Max quota = 250 bytes (300 bytes total -> needs eviction)
	qm := NewQuotaManager(250)
	qm.Touch(m1, 100, PinTierAutoDiscovered)
	qm.Touch(m2, 100, PinTierAutoDiscovered)
	qm.Touch(m3, 100, PinTierAutoDiscovered)

	evicted, err := qm.EnforceQuota(store)
	if err != nil {
		t.Fatalf("EnforceQuota failed: %v", err)
	}

	if evicted != 1 {
		t.Fatalf("expected 1 item evicted, got %d", evicted)
	}

	// m1 (oldest LRU) should be evicted from store
	if _, exists := store.items[m1.String()]; exists {
		t.Fatalf("expected m1 (oldest LRU item) to be evicted")
	}

	sz, _ := store.Size()
	if sz > 250 {
		t.Fatalf("expected total store size <= 250, got %d", sz)
	}
}

func TestQuotaManager_OperatorPinNeverEvicted(t *testing.T) {
	store := newMockEvictableStore()
	m1 := mid.FromBytes([]byte("mid-operator-pinned"))
	m2 := mid.FromBytes([]byte("mid-auto-discovered"))

	store.Put(m1, 100)
	store.Put(m2, 100)

	qm := NewQuotaManager(150) // Needs 50 bytes evicted
	qm.Touch(m1, 100, PinTierOperator)
	qm.Touch(m2, 100, PinTierAutoDiscovered)

	evicted, err := qm.EnforceQuota(store)
	if err != nil {
		t.Fatalf("EnforceQuota failed: %v", err)
	}

	if evicted != 1 {
		t.Fatalf("expected 1 item evicted, got %d", evicted)
	}

	// Operator pinned item m1 MUST NOT be evicted
	if _, exists := store.items[m1.String()]; !exists {
		t.Fatalf("operator pinned item m1 was incorrectly evicted!")
	}
	// Auto discovered item m2 MUST be evicted
	if _, exists := store.items[m2.String()]; exists {
		t.Fatalf("auto-discovered item m2 was not evicted")
	}
}
