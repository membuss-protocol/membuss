package chunk_test

import (
	"testing"

	"github.com/nnlgsakib/membuss/core/chunk"
)

func TestAdaptiveBlockSize(t *testing.T) {
	tests := []struct {
		name string
		size int64
		want int
	}{
		{"Small <= 0", 0, chunk.DefaultBlockSize},
		{"Small 10MB", 10 * 1024 * 1024, 256 * 1024},
		{"Medium 100MB", 100 * 1024 * 1024, 1024 * 1024},
		{"Large 1GB", 1000 * 1024 * 1024, 2 * 1024 * 1024},
		{"Massive 5GB", 5000 * 1024 * 1024, 4 * 1024 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chunk.AdaptiveBlockSize(tt.size)
			if got != tt.want {
				t.Errorf("AdaptiveBlockSize(%d) = %d, want %d", tt.size, got, tt.want)
			}
		})
	}
}
