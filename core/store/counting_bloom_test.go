package store

import (
	"testing"
)

func TestCountingBloomFilter_BasicAddTestRemove(t *testing.T) {
	cbf := NewCountingBloomFilter(1000, 0.001)

	key1 := []byte("item-1")
	key2 := []byte("item-2")
	key3 := []byte("item-3")

	// Initially all absent
	if cbf.Test(key1) {
		t.Fatalf("key1 should be absent initially")
	}
	if cbf.Test(key2) {
		t.Fatalf("key2 should be absent initially")
	}

	// Add key1 and key2
	cbf.Add(key1)
	cbf.Add(key2)

	if !cbf.Test(key1) {
		t.Fatalf("key1 should be present after Add")
	}
	if !cbf.Test(key2) {
		t.Fatalf("key2 should be present after Add")
	}
	if cbf.Test(key3) {
		t.Fatalf("key3 should be absent")
	}

	// O(1) Remove key1
	cbf.Remove(key1)

	if cbf.Test(key1) {
		t.Fatalf("key1 should be absent after Remove")
	}
	if !cbf.Test(key2) {
		t.Fatalf("key2 should still be present after key1 Remove")
	}
}

func TestCountingBloomFilter_MarshalUnmarshal(t *testing.T) {
	cbf1 := NewCountingBloomFilter(500, 0.01)
	cbf1.Add([]byte("alpha"))
	cbf1.Add([]byte("beta"))

	data, err := cbf1.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	cbf2 := &CountingBloomFilter{}
	if err := cbf2.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}

	if !cbf2.Test([]byte("alpha")) {
		t.Fatalf("alpha missing after UnmarshalBinary")
	}
	if !cbf2.Test([]byte("beta")) {
		t.Fatalf("beta missing after UnmarshalBinary")
	}
	if cbf2.Test([]byte("gamma")) {
		t.Fatalf("gamma present unexpectedly after UnmarshalBinary")
	}
}
