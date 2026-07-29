package dag

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/nnlgsakib/membuss/core/chunk"
	"github.com/nnlgsakib/membuss/core/store"
)

func TestBuildParallel_ByteIdenticalToSequential(t *testing.T) {
	payload := make([]byte, 2*1024*1024) // 2MB
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	// Sequential build
	bs1 := store.NewMemstore()
	c1, err := chunk.NewFixed(256 * 1024)(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewFixed: %v", err)
	}
	root1, err := NewBuilder(bs1).Build(c1)
	if err != nil {
		t.Fatalf("Sequential Build: %v", err)
	}

	// Parallel build with 4 workers
	bs2 := store.NewMemstore()
	c2, err := chunk.NewFixed(256 * 1024)(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewFixed: %v", err)
	}
	root2, err := NewBuilder(bs2).BuildParallel(c2, 4)
	if err != nil {
		t.Fatalf("Parallel Build: %v", err)
	}

	if !root1.Equal(root2) {
		t.Fatalf("Root mismatch: sequential=%s, parallel=%s", root1.String(), root2.String())
	}
}

func BenchmarkParallelVSSequential(b *testing.B) {
	payload := make([]byte, 10*1024*1024) // 10MB
	_, _ = rand.Read(payload)

	b.Run("Sequential", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			bs := store.NewMemstore()
			c, _ := chunk.NewFixed(256 * 1024)(bytes.NewReader(payload))
			_, _ = NewBuilder(bs).Build(c)
		}
	})

	b.Run("Parallel-4", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			bs := store.NewMemstore()
			c, _ := chunk.NewFixed(256 * 1024)(bytes.NewReader(payload))
			_, _ = NewBuilder(bs).BuildParallel(c, 4)
		}
	})
}
