package anchor

import (
	"container/list"
	"errors"
	"sync"
	"time"

	"github.com/nnlgsakib/membuss/core/mid"
)

// PinTier specifies whether a MID is explicitly pinned by an operator
// or automatically discovered from peer content exchange.
type PinTier int

const (
	// PinTierAutoDiscovered represents content downloaded via peer discovery.
	// It is eligible for LRU eviction when disk storage quota is exceeded.
	PinTierAutoDiscovered PinTier = iota
	// PinTierOperator represents content explicitly pinned by the node operator.
	// It is NEVER evicted by automated quota enforcement.
	PinTierOperator
)

// EvictableStore is the interface required by QuotaManager to delete evicted items.
type EvictableStore interface {
	Delete(m mid.MID) error
	Size() (uint64, error)
}

type quotaElement struct {
	mid        mid.MID
	tier       PinTier
	size       uint64
	lastAccess time.Time
}

// QuotaManager tracks content sizes, LRU access order, and pin priority tiers.
type QuotaManager struct {
	mu       sync.Mutex
	maxBytes uint64
	items    map[string]*list.Element
	lruList  *list.List
}

// NewQuotaManager initializes a QuotaManager with the specified maximum byte limit.
func NewQuotaManager(maxBytes uint64) *QuotaManager {
	return &QuotaManager{
		maxBytes: maxBytes,
		items:    make(map[string]*list.Element),
		lruList:  list.New(),
	}
}

// SetMaxBytes updates the quota threshold.
func (q *QuotaManager) SetMaxBytes(maxBytes uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.maxBytes = maxBytes
}

// Touch registers or updates a MID in the quota manager with a size and priority tier.
func (q *QuotaManager) Touch(m mid.MID, size uint64, tier PinTier) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	key := m.String()
	if elem, ok := q.items[key]; ok {
		item := elem.Value.(*quotaElement)
		item.lastAccess = time.Now()
		if size > 0 {
			item.size = size
		}
		// Upgrade tier if operator-pinned
		if tier == PinTierOperator {
			item.tier = PinTierOperator
		}
		q.lruList.MoveToFront(elem)
		return
	}

	item := &quotaElement{
		mid:        m,
		tier:       tier,
		size:       size,
		lastAccess: time.Now(),
	}
	elem := q.lruList.PushFront(item)
	q.items[key] = elem
}

// Pin marks a MID as explicitly pinned by an operator (NEVER evicted).
func (q *QuotaManager) Pin(m mid.MID) {
	q.Touch(m, 0, PinTierOperator)
}

// IsOperatorPinned returns whether the given MID is marked as PinTierOperator.
func (q *QuotaManager) IsOperatorPinned(m mid.MID) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	if elem, ok := q.items[m.String()]; ok {
		return elem.Value.(*quotaElement).tier == PinTierOperator
	}
	return false
}

// EnforceQuota checks current store size against maxBytes and evicts unpinned LRU items until compliant.
func (q *QuotaManager) EnforceQuota(store EvictableStore) (int, error) {
	if q == nil || store == nil {
		return 0, nil
	}

	q.mu.Lock()
	maxB := q.maxBytes
	q.mu.Unlock()

	if maxB == 0 {
		return 0, nil // Unlimited storage
	}

	currentSize, err := store.Size()
	if err != nil || currentSize <= maxB {
		return 0, err
	}

	evictedCount := 0
	q.mu.Lock()
	defer q.mu.Unlock()

	// Walk LRU list from back (least recently used) to front
	curr := q.lruList.Back()
	for curr != nil && currentSize > maxB {
		prev := curr.Prev()
		item := curr.Value.(*quotaElement)

		if item.tier == PinTierOperator {
			curr = prev
			continue // Operator pinned items are NEVER evicted
		}

		// Attempt eviction from store
		if err := store.Delete(item.mid); err == nil {
			delete(q.items, item.mid.String())
			q.lruList.Remove(curr)
			evictedCount++
			if currentSize > item.size {
				currentSize -= item.size
			} else {
				// Refresh total size from store
				if s, err := store.Size(); err == nil {
					currentSize = s
				} else {
					break
				}
			}
		}

		curr = prev
	}

	if evictedCount > 0 {
		return evictedCount, nil
	}
	if currentSize > maxB {
		return 0, errors.New("storage quota exceeded but all items are operator pinned")
	}
	return 0, nil
}
