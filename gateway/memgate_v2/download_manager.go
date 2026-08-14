package memgate_v2

import (
	"context"
	"sync"
	"time"

	"github.com/nnlgsakib/membuss/core/mid"
)

// ProgressUpdate represents a progress snapshot for downloads.
type ProgressUpdate struct {
	BlocksResolved uint64  `json:"blocks_resolved"`
	BlocksTotal    uint64  `json:"blocks_total"`
	BytesDelivered uint64  `json:"bytes_delivered"`
	BytesTotal     uint64  `json:"bytes_total"`
	Throughput     float64 `json:"throughput"`
	ETA            float64 `json:"eta"`
}

// DownloadJob represents a background download task.
type DownloadJob struct {
	MID            string    `json:"mid"`
	State          string    `json:"state"` // "discovering", "fetching", "completed", "failed"
	Error          string    `json:"error,omitempty"`
	BlocksResolved uint64    `json:"blocks_resolved"`
	BlocksTotal    uint64    `json:"blocks_total"`
	BytesDelivered uint64    `json:"bytes_delivered"`
	BytesTotal     uint64    `json:"bytes_total"`
	Throughput     float64   `json:"throughput"`
	ETA            float64   `json:"eta"`

	mu             sync.Mutex
	listeners      map[int]chan ProgressUpdate
	nextListenerID int
	ctx            context.Context
	cancel         context.CancelFunc
}

// GetStatus returns a snapshot of the job status.
func (j *DownloadJob) GetStatus() (string, string, uint64, uint64, uint64, uint64, float64, float64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.State, j.Error, j.BlocksResolved, j.BlocksTotal, j.BytesDelivered, j.BytesTotal, j.Throughput, j.ETA
}

// AddListener registers a channel to receive progress updates.
func (j *DownloadJob) AddListener() (int, chan ProgressUpdate) {
	return j.AddListenerWithContext(nil)
}

// AddListenerWithContext registers a channel to receive progress updates and monitors context cancellation.
func (j *DownloadJob) AddListenerWithContext(ctx context.Context) (int, chan ProgressUpdate) {
	j.mu.Lock()
	id := j.nextListenerID
	j.nextListenerID++
	ch := make(chan ProgressUpdate, 10)
	j.listeners[id] = ch
	j.mu.Unlock()

	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				j.RemoveListener(id)
			case <-j.ctx.Done():
			}
		}()
	}

	return id, ch
}

// RemoveListener unregisters a progress channel and closes it safely.
func (j *DownloadJob) RemoveListener(id int) {
	j.mu.Lock()
	ch, ok := j.listeners[id]
	if ok {
		delete(j.listeners, id)
	}
	j.mu.Unlock()

	if ok {
		close(ch)
	}
}

// DownloadManager coordinates concurrent background downloading of MIDs.
type DownloadManager struct {
	mu   sync.Mutex
	jobs map[string]*DownloadJob
}

// NewDownloadManager creates a new manager.
func NewDownloadManager() *DownloadManager {
	return &DownloadManager{
		jobs: make(map[string]*DownloadJob),
	}
}

// GetOrCreateJob checks if a job is already running for the MID, or spawns a new one.
func (dm *DownloadManager) GetOrCreateJob(m mid.MID, backend Backend) (*DownloadJob, bool) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	midStr := m.String()
	if job, ok := dm.jobs[midStr]; ok {
		return job, false
	}

	jobCtx, cancel := context.WithCancel(context.Background())
	job := &DownloadJob{
		MID:       midStr,
		State:     "discovering",
		listeners: make(map[int]chan ProgressUpdate),
		ctx:       jobCtx,
		cancel:    cancel,
	}
	dm.jobs[midStr] = job

	go func() {
		defer func() {
			// Allow status queries to hit cache for 2 minutes post completion/failure
			time.AfterFunc(2*time.Minute, func() {
				dm.mu.Lock()
				delete(dm.jobs, midStr)
				dm.mu.Unlock()
			})
		}()

		progressFn := func(u ProgressUpdate) {
			job.mu.Lock()
			if u.BlocksResolved > 0 || u.BlocksTotal > 0 {
				job.State = "fetching"
			} else {
				job.State = "discovering"
			}
			job.BlocksResolved = u.BlocksResolved
			job.BlocksTotal = u.BlocksTotal
			job.BytesDelivered = u.BytesDelivered
			job.BytesTotal = u.BytesTotal
			job.Throughput = u.Throughput
			job.ETA = u.ETA

			// Copy listener list to avoid holding lock while broadcasting
			listeners := make([]chan ProgressUpdate, 0, len(job.listeners))
			for _, ch := range job.listeners {
				listeners = append(listeners, ch)
			}
			job.mu.Unlock()

			// Broadcast
			for _, ch := range listeners {
				select {
				case ch <- u:
				default:
				}
			}
		}

		rc, info, err := backend.ResolveWithProgress(job.ctx, m, progressFn)
		if err != nil {
			job.mu.Lock()
			job.State = "failed"
			job.Error = err.Error()
			listeners := job.listeners
			job.listeners = make(map[int]chan ProgressUpdate)
			job.mu.Unlock()

			// Notify failure and close channels
			for _, ch := range listeners {
				select {
				case ch <- ProgressUpdate{}:
				default:
				}
				close(ch)
			}
			job.cancel()
			return
		}
		if rc != nil {
			_ = rc.Close()
		}

		job.mu.Lock()
		job.State = "completed"
		if info.Size > 0 {
			job.BytesTotal = info.Size
			job.BytesDelivered = info.Size
		}
		listeners := job.listeners
		job.listeners = make(map[int]chan ProgressUpdate)
		job.mu.Unlock()

		// Notify completion and close channels
		for _, ch := range listeners {
			select {
			case ch <- ProgressUpdate{}:
			default:
			}
			close(ch)
		}
		job.cancel()
	}()

	return job, true
}

// Close gracefully terminates all background download jobs and cancels their listeners.
func (dm *DownloadManager) Close() {
	if dm == nil {
		return
	}
	dm.mu.Lock()
	defer dm.mu.Unlock()
	for _, job := range dm.jobs {
		job.mu.Lock()
		if job.cancel != nil {
			job.cancel()
		}
		listeners := job.listeners
		job.listeners = make(map[int]chan ProgressUpdate)
		job.mu.Unlock()

		for _, ch := range listeners {
			close(ch)
		}
	}
	dm.jobs = make(map[string]*DownloadJob)
}
