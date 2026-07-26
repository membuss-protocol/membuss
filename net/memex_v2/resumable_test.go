package memex_v2

import (
	"context"
	"testing"
	"time"
)

func TestActivityContext_ResetsOnTouch(t *testing.T) {
	ctx, cancel, touch := ActivityContext(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Touch every 40ms for 3 rounds (total 120ms > 100ms timeout)
	for i := 0; i < 3; i++ {
		time.Sleep(40 * time.Millisecond)
		touch()
	}

	if ctx.Err() != nil {
		t.Fatalf("expected context to stay alive with touches, got err: %v", ctx.Err())
	}

	// Now stop touching and wait for timeout
	time.Sleep(150 * time.Millisecond)
	if ctx.Err() == nil {
		t.Fatalf("expected context to cancel after idle timeout, got nil")
	}
}
