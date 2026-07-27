package memgate_v2

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/nnlgsakib/membuss/core/mid"
)

type dummyDownloadBackend struct {
	resolveFn func(ctx context.Context, m mid.MID, progressFn func(ProgressUpdate)) (io.ReadCloser, ContentInfo, error)
}

func (d *dummyDownloadBackend) Resolve(ctx context.Context, m mid.MID) (io.ReadCloser, ContentInfo, error) {
	return nil, ContentInfo{}, nil
}

func (d *dummyDownloadBackend) ResolveWithProgress(ctx context.Context, m mid.MID, progressFn func(ProgressUpdate)) (io.ReadCloser, ContentInfo, error) {
	if d.resolveFn != nil {
		return d.resolveFn(ctx, m, progressFn)
	}
	return nil, ContentInfo{Size: 100}, nil
}

func (d *dummyDownloadBackend) RawBlock(ctx context.Context, m mid.MID) ([]byte, error) {
	return nil, nil
}

func (d *dummyDownloadBackend) DAGNodeJSON(ctx context.Context, m mid.MID) ([]byte, error) {
	return nil, nil
}

func (d *dummyDownloadBackend) Stat(ctx context.Context, m mid.MID) (ContentInfo, error) {
	return ContentInfo{}, nil
}

func (d *dummyDownloadBackend) Ping(ctx context.Context) error {
	return nil
}

func (d *dummyDownloadBackend) MemFSInfo(ctx context.Context, m mid.MID) (MemFSInfo, error) {
	return MemFSInfo{}, nil
}

func (d *dummyDownloadBackend) MemFSPathGet(ctx context.Context, m mid.MID, path string) (io.ReadSeekCloser, uint64, string, error) {
	return nil, 0, "", nil
}

func (d *dummyDownloadBackend) MemFSList(ctx context.Context, m mid.MID) ([]MemFSEntry, error) {
	return nil, nil
}

func (d *dummyDownloadBackend) MemFSPathInfo(ctx context.Context, m mid.MID, path string) (MemFSInfo, error) {
	return MemFSInfo{}, nil
}

func (d *dummyDownloadBackend) MemFSPathList(ctx context.Context, m mid.MID, path string) ([]MemFSEntry, error) {
	return nil, nil
}

func (d *dummyDownloadBackend) Descriptor(ctx context.Context, m mid.MID) ([]byte, error) {
	return nil, nil
}

func TestRemoveListener_ClosesChannelWhenNonEmpty(t *testing.T) {
	job := &DownloadJob{
		MID:       "test-mid",
		listeners: make(map[int]chan ProgressUpdate),
	}

	id, ch := job.AddListener()

	// Fill channel with items so it is non-empty
	ch <- ProgressUpdate{BlocksResolved: 1}
	ch <- ProgressUpdate{BlocksResolved: 2}

	// Remove listener
	job.RemoveListener(id)

	// Verify channel is CLOSED (reading until ok == false)
	drainCount := 0
	for range ch {
		drainCount++
	}

	if drainCount != 2 {
		t.Errorf("expected 2 drained items before close, got %d", drainCount)
	}

	// Verify job.listeners no longer holds id
	job.mu.Lock()
	_, exists := job.listeners[id]
	job.mu.Unlock()

	if exists {
		t.Errorf("expected listener id %d to be deleted from map", id)
	}
}

func TestAddListenerWithContext_AutoRemovesOnDisconnect(t *testing.T) {
	jobCtx, cancelJob := context.WithCancel(context.Background())
	job := &DownloadJob{
		MID:       "test-mid",
		listeners: make(map[int]chan ProgressUpdate),
		ctx:       jobCtx,
	}
	defer cancelJob()

	clientCtx, cancelClient := context.WithCancel(context.Background())

	id, ch := job.AddListenerWithContext(clientCtx)

	job.mu.Lock()
	if len(job.listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(job.listeners))
	}
	job.mu.Unlock()

	// Simulate SSE client disconnect
	cancelClient()

	// Wait briefly for monitor goroutine to fire
	deadline := time.Now().Add(1 * time.Second)
	var exists bool
	for time.Now().Before(deadline) {
		job.mu.Lock()
		_, exists = job.listeners[id]
		job.mu.Unlock()
		if !exists {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if exists {
		t.Errorf("expected listener to be auto-removed upon client context cancellation")
	}

	// Verify channel was closed
	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("expected channel to be closed, but received open channel item")
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("timeout waiting for channel close")
	}
}

func TestJobCompletion_ClearsAndClosesAllListeners(t *testing.T) {
	dm := NewDownloadManager()
	testMID := mid.MustParse("membafkreic4pua4hmtr3tkjmgonia6b4pbozkftk6ilg2adyku25mc3hzpus4")

	backend := &dummyDownloadBackend{
		resolveFn: func(ctx context.Context, m mid.MID, progressFn func(ProgressUpdate)) (io.ReadCloser, ContentInfo, error) {
			progressFn(ProgressUpdate{BlocksResolved: 1, BlocksTotal: 1})
			return nil, ContentInfo{Size: 1024}, nil
		},
	}

	job, _ := dm.GetOrCreateJob(testMID, backend)

	id1, ch1 := job.AddListener()
	id2, ch2 := job.AddListener()
	_ = id1
	_ = id2

	// Wait for job to complete
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, _, _, _, _, _, _, _ := job.GetStatus()
		if st == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	st, _, _, _, _, _, _, _ := job.GetStatus()
	if st != "completed" {
		t.Fatalf("job state = %s, want completed", st)
	}

	// Verify both channels were closed
	for name, ch := range map[string]chan ProgressUpdate{"ch1": ch1, "ch2": ch2} {
		closed := false
		for range ch {
		}
		closed = true
		if !closed {
			t.Errorf("%s was not closed on job completion", name)
		}
	}

	job.mu.Lock()
	listenerCount := len(job.listeners)
	job.mu.Unlock()

	if listenerCount != 0 {
		t.Errorf("expected listener map to be empty post-completion, got %d", listenerCount)
	}
}

func TestJobFailure_ClearsAndClosesAllListeners(t *testing.T) {
	dm := NewDownloadManager()
	testMID := mid.MustParse("membafkreic4pua4hmtr3tkjmgonia6b4pbozkftk6ilg2adyku25mc3hzpus4")

	backend := &dummyDownloadBackend{
		resolveFn: func(ctx context.Context, m mid.MID, progressFn func(ProgressUpdate)) (io.ReadCloser, ContentInfo, error) {
			return nil, ContentInfo{}, errors.New("network error")
		},
	}

	job, _ := dm.GetOrCreateJob(testMID, backend)

	_, ch := job.AddListener()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, _, _, _, _, _, _, _ := job.GetStatus()
		if st == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	st, errStr, _, _, _, _, _, _ := job.GetStatus()
	if st != "failed" || errStr != "network error" {
		t.Fatalf("job state = %s, err = %s, want failed/network error", st, errStr)
	}

	// Verify channel closed
	for range ch {
	}

	job.mu.Lock()
	listenerCount := len(job.listeners)
	job.mu.Unlock()

	if listenerCount != 0 {
		t.Errorf("expected listener map to be empty post-failure, got %d", listenerCount)
	}
}

func TestConcurrentAddRemoveListeners(t *testing.T) {
	jobCtx, cancelJob := context.WithCancel(context.Background())
	job := &DownloadJob{
		MID:       "test-mid",
		listeners: make(map[int]chan ProgressUpdate),
		ctx:       jobCtx,
	}
	defer cancelJob()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, ch := job.AddListener()
			time.Sleep(5 * time.Millisecond)
			job.RemoveListener(id)
			_ = ch
		}()
	}
	wg.Wait()

	job.mu.Lock()
	count := len(job.listeners)
	job.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 remaining listeners after concurrent add/remove, got %d", count)
	}
}
