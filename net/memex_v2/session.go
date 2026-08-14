package memex_v2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/dag"
	"github.com/nnlgsakib/membuss/core/memfs"
	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

func tryParseDescriptor(data []byte) (*membusspb.DescriptorPayload, error) {
	var desc membusspb.DescriptorPayload
	if err := proto.Unmarshal(data, &desc); err == nil && desc.RootMid != "" {
		return &desc, nil
	}
	if len(data) < 5+32 {
		return nil, errors.New("descriptor: too short")
	}
	if !bytes.Equal(data[:4], []byte{'M', 'E', 'M', 'B'}) {
		return nil, errors.New("descriptor: invalid magic")
	}
	if data[4] != 1 {
		return nil, errors.New("descriptor: unsupported version")
	}
	payload := data[5 : len(data)-32]
	var descWrapped membusspb.DescriptorPayload
	if err := proto.Unmarshal(payload, &descWrapped); err == nil && descWrapped.RootMid != "" {
		return &descWrapped, nil
	}
	return nil, errors.New("descriptor: failed to parse wrapped")
}

type ProgressUpdate struct {
	BlocksResolved uint64
	BlocksTotal    uint64
	BytesDelivered uint64
	BytesTotal     uint64
	Throughput     float64
	ETA            float64
}

type SessionConfig struct {
	Engine         *Engine
	Root           mid.MID
	Providers      []peer.AddrInfo
	ParallelPeers  int
	Timeout        time.Duration
	ProgressFn     func(update ProgressUpdate)
	ProviderFinder func(ctx context.Context, m mid.MID) ([]peer.AddrInfo, error)

	PipelineDepth      int
	StreamsPerProvider int
}

type sessionEvent struct {
	isCancel bool
	mid      mid.MID
}

type wantState struct {
	mid             mid.MID
	attempts        int
	triedProviders  map[peer.ID]struct{}
	currentProvider peer.ID
	lastSent        time.Time
}

type Session struct {
	cfg          SessionConfig
	ctx          context.Context
	cancel       context.CancelFunc
	touchFn      func()
	findingProvs uint32

	mu              sync.Mutex
	enqueued        map[string]struct{}
	resolved        map[string]struct{}
	wantlist        map[string]mid.MID
	wantStates      map[string]*wantState
	schedulerWakeCh chan struct{}

	startTime       time.Time
	bytesDelivered  uint64
	bytesTotal      uint64

	provMu          sync.Mutex
	liveProviders   []peer.AddrInfo
	activeProviders map[peer.ID]*pooledStream
	failedProviders map[peer.ID]struct{}
	managerWakeCh   chan struct{}

	resolvedCh chan struct{}
	walkerDone chan struct{}
	doneWg     sync.WaitGroup
}

func (s *Session) emitProgress() {
	if s == nil || s.cfg.ProgressFn == nil {
		return
	}
	s.mu.Lock()
	resolved := uint64(len(s.resolved))
	total := uint64(len(s.enqueued))
	delivered := s.bytesDelivered
	totalBytes := s.bytesTotal
	start := s.startTime
	s.mu.Unlock()

	var throughput float64
	var eta float64
	elapsed := time.Since(start).Seconds()
	if elapsed > 0 && delivered > 0 {
		throughput = float64(delivered) / elapsed
		if totalBytes > delivered && throughput > 0 {
			eta = float64(totalBytes-delivered) / throughput
		}
	}

	s.cfg.ProgressFn(ProgressUpdate{
		BlocksResolved: resolved,
		BlocksTotal:    total,
		BytesDelivered: delivered,
		BytesTotal:     totalBytes,
		Throughput:     throughput,
		ETA:            eta,
	})
}

func NewSession(cfg SessionConfig) (*Session, error) {
	if cfg.Engine == nil {
		return nil, errors.New("memex session: nil engine")
	}
	if cfg.Root.IsZero() {
		return nil, errors.New("memex session: zero root")
	}
	if len(cfg.Providers) == 0 {
		return nil, errors.New("memex session: no providers")
	}
	sess := &Session{
		cfg:             cfg,
		startTime:       time.Now(),
		enqueued:        make(map[string]struct{}),
		resolved:        make(map[string]struct{}),
		wantlist:        make(map[string]mid.MID),
		wantStates:      make(map[string]*wantState),
		schedulerWakeCh: make(chan struct{}, 1),
		managerWakeCh:   make(chan struct{}, 1),
		resolvedCh:      make(chan struct{}, 1),
		walkerDone:      make(chan struct{}),
	}
	cfg.Engine.RegisterSession(sess)
	return sess, nil
}

type countingWriter struct {
	w     io.Writer
	start time.Time
	mu    sync.Mutex
	bytes uint64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.mu.Lock()
		c.bytes += uint64(n)
		c.mu.Unlock()
	}
	return n, err
}

func (c *countingWriter) Progress() (uint64, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes, time.Since(c.start)
}

func (s *Session) Fetch(ctx context.Context) (io.Reader, error) {
	var (
		actCtx context.Context
		cancel context.CancelFunc
		touch  func()
	)
	if s.cfg.Timeout > 0 {
		actCtx, cancel = context.WithTimeout(ctx, s.cfg.Timeout)
	} else {
		actCtx, cancel, touch = ActivityContext(ctx, DefaultIdleTimeout)
	}

	s.ctx = actCtx
	s.cancel = cancel
	s.touchFn = touch
	defer s.cfg.Engine.UnregisterSession(s)

	fanout := s.cfg.ParallelPeers
	if fanout <= 0 {
		fanout = MaxParallelPeers
	}

	s.mu.Lock()
	s.enqueued = make(map[string]struct{})
	s.resolved = make(map[string]struct{})
	s.wantlist = make(map[string]mid.MID)
	s.wantStates = make(map[string]*wantState)
	s.mu.Unlock()

	filtered := s.selectPeersForMID(s.cfg.Root)
	if len(filtered) == 0 {
		filtered = s.cfg.Providers
	}

	s.provMu.Lock()
	s.liveProviders = filtered
	s.activeProviders = make(map[peer.ID]*pooledStream)
	s.failedProviders = make(map[peer.ID]struct{})
	s.provMu.Unlock()

	s.checkAndEnqueue(ctx, s.cfg.Root)

	s.doneWg.Add(1)
	go func() {
		defer s.doneWg.Done()
		s.schedulerLoop(ctx)
	}()

	s.doneWg.Add(1)
	go func() {
		defer s.doneWg.Done()
		s.providerManagerLoop(ctx, fanout)
	}()

	// Progress reporting. Fetch resolves the whole DAG before
	// returning a reader, emitting live block and byte telemetry.
	progressStop := make(chan struct{})
	if s.cfg.ProgressFn != nil {
		s.emitProgress()
		go func() {
			t := time.NewTicker(50 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-progressStop:
					return
				case <-ctx.Done():
					return
				case <-t.C:
					s.emitProgress()
				}
			}
		}()
	}

	// Wait for resolution loop
	seenWalked := make(map[string]struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var toWalk []string
			s.mu.Lock()
			for k := range s.resolved {
				if _, seen := seenWalked[k]; seen {
					continue
				}
				seenWalked[k] = struct{}{}
				toWalk = append(toWalk, k)
			}
			s.mu.Unlock()

			for _, midStr := range toWalk {
				if err := s.enqueueChildren(ctx, midStr); err != nil {
					return
				}
			}

			s.mu.Lock()
			hasUnwalked := false
			for k := range s.resolved {
				if _, seen := seenWalked[k]; !seen {
					hasUnwalked = true
					break
				}
			}
			allRes := len(s.enqueued) == len(s.resolved)
			s.mu.Unlock()

			if allRes && !hasUnwalked {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-s.resolvedCh:
			}
		}
	}()

	select {
	case <-done:
	case <-ctx.Done():
		close(progressStop)
		return nil, ctx.Err()
	}
	close(progressStop)

	if !s.allResolved() {
		cancel()
		return nil, errors.New("memex session: not all blocks resolved")
	}

	// Emit a terminal update so the caller sees 100% of blocks
	// resolved before the reader is drained
	s.emitProgress()

	if s.cfg.Root.Codec() == mid.CodecMemFS {
		res := memfs.NewResolver(asBlockstore(s.cfg.Engine.bs, s))
		rc, err := res.Open(ctx, s.cfg.Root)
		if err == nil {
			return &cancelOnCloseReader{r: rc, cancel: cancel}, nil
		}
	}

	resolver := dag.NewResolver(asBlockstore(s.cfg.Engine.bs, s))
	rc, err := resolver.Resolve(s.cfg.Root, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("memex session: resolve: %w", err)
	}
	return &cancelOnCloseReader{r: rc, cancel: cancel}, nil
}

type cancelOnCloseReader struct {
	r      io.Reader
	cancel context.CancelFunc
}

func (c *cancelOnCloseReader) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func (c *cancelOnCloseReader) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	if closer, ok := c.r.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (s *Session) FetchStream(ctx context.Context) (io.Reader, error) {
	var (
		actCtx context.Context
		cancel context.CancelFunc
		touch  func()
	)
	if s.cfg.Timeout > 0 {
		actCtx, cancel = context.WithTimeout(ctx, s.cfg.Timeout)
	} else {
		actCtx, cancel, touch = ActivityContext(ctx, DefaultIdleTimeout)
	}

	s.ctx = actCtx
	s.cancel = cancel
	s.touchFn = touch

	fanout := s.cfg.ParallelPeers
	if fanout <= 0 {
		fanout = MaxParallelPeers
	}

	s.mu.Lock()
	s.enqueued = make(map[string]struct{})
	s.resolved = make(map[string]struct{})
	s.wantlist = make(map[string]mid.MID)
	s.wantStates = make(map[string]*wantState)
	s.mu.Unlock()

	filtered := s.selectPeersForMID(s.cfg.Root)
	if len(filtered) == 0 {
		filtered = s.cfg.Providers
	}

	s.provMu.Lock()
	s.liveProviders = filtered
	s.activeProviders = make(map[peer.ID]*pooledStream)
	s.failedProviders = make(map[peer.ID]struct{})
	s.provMu.Unlock()

	s.checkAndEnqueue(ctx, s.cfg.Root)

	// Start scheduler in background
	s.doneWg.Add(1)
	go func() {
		defer s.doneWg.Done()
		s.schedulerLoop(ctx)
	}()

	// Start provider manager in background
	s.doneWg.Add(1)
	go func() {
		defer s.doneWg.Done()
		s.providerManagerLoop(ctx, fanout)
	}()

	// Start recursive walk trigger loop in background
	seenWalked := make(map[string]struct{})
	go func() {
		for {
			var toWalk []string
			s.mu.Lock()
			for k := range s.resolved {
				if _, seen := seenWalked[k]; seen {
					continue
				}
				seenWalked[k] = struct{}{}
				toWalk = append(toWalk, k)
			}
			s.mu.Unlock()

			for _, midStr := range toWalk {
				if err := s.enqueueChildren(ctx, midStr); err != nil {
					return
				}
			}

			s.mu.Lock()
			hasUnwalked := false
			for k := range s.resolved {
				if _, seen := seenWalked[k]; !seen {
					hasUnwalked = true
					break
				}
			}
			allRes := len(s.enqueued) == len(s.resolved)
			s.mu.Unlock()

			if allRes && !hasUnwalked {
				select {
				case <-ctx.Done():
					return
				case <-s.walkerDone:
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-s.walkerDone:
				return
			case <-s.resolvedCh:
			}
		}
	}()

	pipeReader, pipeWriter := io.Pipe()
	cw := &countingWriter{w: pipeWriter, start: time.Now()}

	// Start Progress updates if requested
	if s.cfg.ProgressFn != nil {
		go func() {
			t := time.NewTicker(100 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-s.walkerDone:
					return
				case <-t.C:
					bytes, elapsed := cw.Progress()
					s.mu.Lock()
					resolved := uint64(len(s.resolved))
					total := uint64(len(s.enqueued))
					s.mu.Unlock()
					var throughput float64
					if elapsed.Seconds() > 0 {
						throughput = float64(bytes) / elapsed.Seconds()
					}
					s.cfg.ProgressFn(ProgressUpdate{
						BlocksResolved: resolved,
						BlocksTotal:    total,
						BytesDelivered: bytes,
						Throughput:     throughput,
					})
				}
			}
		}()
	}

	// Resolve and copy data to pipeWriter asynchronously
	go func() {
		defer func() {
			s.cfg.Engine.UnregisterSession(s)
			cancel()
			s.doneWg.Wait()
			close(s.walkerDone)
		}()

		resolver := dag.NewResolver(asBlockstore(s.cfg.Engine.bs, s))
		rc, err := resolver.Resolve(s.cfg.Root, nil)
		if err != nil {
			_ = pipeWriter.CloseWithError(err)
			return
		}
		if c, ok := rc.(io.Closer); ok {
			defer c.Close()
		}

		_, err = io.Copy(cw, rc)
		if err != nil {
			_ = pipeWriter.CloseWithError(err)
		} else {
			_ = pipeWriter.Close()
		}
	}()

	return pipeReader, nil
}

func (s *Session) checkAndEnqueue(ctx context.Context, id mid.MID) {
	s.mu.Lock()
	midStr := id.String()
	if _, ok := s.enqueued[midStr]; ok {
		s.mu.Unlock()
		return
	}
	s.enqueued[midStr] = struct{}{}

	has, err := s.cfg.Engine.bs.Has(id)
	if err == nil && has {
		s.resolved[midStr] = struct{}{}
		if data, derr := s.cfg.Engine.bs.Get(id); derr == nil {
			s.bytesDelivered += uint64(len(data))
		}
		s.mu.Unlock()
		s.emitProgress()
		select {
		case s.resolvedCh <- struct{}{}:
		default:
		}
	} else {
		s.wantlist[midStr] = id
		s.wantStates[midStr] = &wantState{
			mid:            id,
			triedProviders: make(map[peer.ID]struct{}),
		}
		s.mu.Unlock()
		s.emitProgress()
		s.wakeScheduler()
	}
}

func (s *Session) markResolved(id mid.MID) {
	if s.touchFn != nil {
		s.touchFn()
	}
	s.mu.Lock()
	midStr := id.String()
	ws, ok := s.wantStates[midStr]
	if ok && ws.currentProvider != "" {
		s.cfg.Engine.RecordPeerSuccess(ws.currentProvider, time.Since(ws.lastSent))
	}

	s.resolved[midStr] = struct{}{}
	delete(s.wantlist, midStr)
	delete(s.wantStates, midStr)
	if data, derr := s.cfg.Engine.bs.Get(id); derr == nil {
		s.bytesDelivered += uint64(len(data))
	}
	s.mu.Unlock()

	s.emitProgress()

	// Notify active streams to cancel want
	s.provMu.Lock()
	for _, stream := range s.activeProviders {
		stream.PushEvent(sessionEvent{isCancel: true, mid: id})
	}
	s.provMu.Unlock()

	select {
	case s.resolvedCh <- struct{}{}:
	default:
	}
}

func (s *Session) markPeerHasBlock(id mid.MID, peerID peer.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	midStr := id.String()
	ws, ok := s.wantStates[midStr]
	if ok && ws.currentProvider == "" {
		ws.currentProvider = peerID
		ws.lastSent = time.Now()
		s.wakeScheduler()
	}
}

func (s *Session) markFailed(id mid.MID, peerID peer.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	midStr := id.String()
	ws, ok := s.wantStates[midStr]
	if ok && ws.currentProvider == peerID {
		s.cfg.Engine.RecordPeerFailure(peerID)
		ws.triedProviders[peerID] = struct{}{}
		ws.currentProvider = ""

		s.provMu.Lock()
		if stream, exists := s.activeProviders[peerID]; exists {
			stream.PushEvent(sessionEvent{isCancel: true, mid: id})
		}
		s.provMu.Unlock()
		s.wakeScheduler()
	}
}

func (s *Session) markPeerFailed(peerID peer.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	woke := false
	for _, ws := range s.wantStates {
		if ws.currentProvider == peerID {
			ws.triedProviders[peerID] = struct{}{}
			ws.currentProvider = ""
			woke = true
		}
	}
	if woke {
		s.wakeScheduler()
	}
}

func (s *Session) wakeScheduler() {
	select {
	case s.schedulerWakeCh <- struct{}{}:
	default:
	}
}

func (s *Session) wakeProviderManager() {
	select {
	case s.managerWakeCh <- struct{}{}:
	default:
	}
}

func (s *Session) allResolved() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.enqueued) != len(s.resolved) {
		return false
	}
	for k := range s.enqueued {
		if _, ok := s.resolved[k]; !ok {
			return false
		}
	}
	return true
}

func (s *Session) selectPeersForMID(m mid.MID) []peer.AddrInfo {
	if s.cfg.Engine.bloom == nil {
		return s.cfg.Providers
	}
	return s.cfg.Engine.bloom.FilteredProviders(m, s.cfg.Providers)
}

func (s *Session) enqueueChildren(ctx context.Context, midStr string) error {
	id, err := mid.Parse(midStr)
	if err != nil {
		return nil
	}
	data, err := s.cfg.Engine.bs.Get(id)
	if err != nil {
		return nil
	}

	var childMIDs []mid.MID

	if desc, uerr := tryParseDescriptor(data); uerr == nil && desc.RootMid != "" && len(desc.Blocks) > 0 {
		s.mu.Lock()
		if desc.TotalSize > 0 {
			s.bytesTotal = desc.TotalSize
		}
		s.mu.Unlock()
		if rMID, err := mid.Parse(desc.RootMid); err == nil {
			childMIDs = append(childMIDs, rMID)
		}
		for _, b := range desc.Blocks {
			if m, err := mid.Parse(b.Mid); err == nil {
				childMIDs = append(childMIDs, m)
			}
		}
	} else if id.Codec() == mid.CodecMemFS {
		var node membusspb.MemFSNode
		if uerr := proto.Unmarshal(data, &node); uerr == nil {
			s.mu.Lock()
			if node.GetFileSize() > 0 {
				s.bytesTotal = node.GetFileSize()
			}
			s.mu.Unlock()
			switch node.Type {
			case membusspb.MemFSType_FILE:
				if len(node.Data) > 0 && len(node.Blocks) == 1 && node.Blocks[0] != nil && len(node.Blocks[0].Mid) > 0 {
					var codec uint64 = mid.CodecMemFS
					if node.Blocks[0].Size > 0 {
						codec = mid.CodecRaw
					}
					if child, err := mid.FromMultihash(codec, node.Blocks[0].Mid); err == nil {
						_ = s.cfg.Engine.bs.Put(child, node.Data)
						s.mu.Lock()
						childStr := child.String()
						s.enqueued[childStr] = struct{}{}
						s.resolved[childStr] = struct{}{}
						s.bytesDelivered += uint64(len(node.Data))
						s.mu.Unlock()
						s.emitProgress()
						select {
						case s.resolvedCh <- struct{}{}:
						default:
						}
					}
					return nil
				}
				for _, b := range node.Blocks {
					if b == nil || len(b.Mid) == 0 {
						continue
					}
					var codec uint64 = mid.CodecMemFS
					if b.Size > 0 {
						codec = mid.CodecRaw
					}
					child, err := mid.FromMultihash(codec, b.Mid)
					if err == nil {
						childMIDs = append(childMIDs, child)
					}
				}
			case membusspb.MemFSType_DIR:
				for _, e := range node.Entries {
					if e == nil || len(e.Mid) == 0 {
						continue
					}
					var codec uint64 = mid.CodecMemFS
					if e.Type == membusspb.MemFSType_RAW {
						codec = mid.CodecRaw
					}
					child, err := mid.FromMultihash(codec, e.Mid)
					if err == nil {
						childMIDs = append(childMIDs, child)
					}
				}
			}
		}
	} else {
		var node membusspb.DAGNode
		if uerr := proto.Unmarshal(data, &node); uerr == nil && len(node.Links) > 0 {
			for _, ls := range node.Links {
				child, err := mid.Parse(ls)
				if err == nil {
					childMIDs = append(childMIDs, child)
				}
			}
		}
	}

	for _, child := range childMIDs {
		s.checkAndEnqueue(ctx, child)
	}
	return nil
}
