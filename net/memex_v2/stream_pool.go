package memex_v2

import (
	"context"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

type PeerStreamPool struct {
	mu      sync.Mutex
	streams map[peer.ID]*pooledStream
	engine  *Engine
}

func newPeerStreamPool(e *Engine) *PeerStreamPool {
	return &PeerStreamPool{
		streams: make(map[peer.ID]*pooledStream),
		engine:  e,
	}
}

func (p *PeerStreamPool) GetOrCreateStream(ctx context.Context, pid peer.ID) (*pooledStream, error) {
	p.mu.Lock()
	ps, exists := p.streams[pid]
	if exists && !ps.isClosed() {
		p.mu.Unlock()
		return ps, nil
	}
	p.mu.Unlock()

	// Open new stream
	stream, err := p.engine.openStream(ctx, pid)
	if err != nil {
		return nil, err
	}

	ps = newPooledStream(pid, stream, p.engine)
	p.mu.Lock()
	// Close any previous stream
	if old, ok := p.streams[pid]; ok {
		old.Close()
	}
	p.streams[pid] = ps
	p.mu.Unlock()

	ps.Start()
	return ps, nil
}

func (p *PeerStreamPool) RegisterInbound(stream network.Stream, pid peer.ID) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if old, ok := p.streams[pid]; ok {
		old.Close()
	}

	ps := newPooledStream(pid, stream, p.engine)
	p.streams[pid] = ps
	ps.Start()
}

func (p *PeerStreamPool) RemoveStream(pid peer.ID, ps *pooledStream) {
	p.mu.Lock()
	defer p.mu.Unlock()
	current, exists := p.streams[pid]
	if exists && current == ps {
		delete(p.streams, pid)
	}
}

func (p *PeerStreamPool) CloseAll() {
	p.mu.Lock()
	streams := make([]*pooledStream, 0, len(p.streams))
	for _, ps := range p.streams {
		streams = append(streams, ps)
	}
	p.streams = make(map[peer.ID]*pooledStream)
	p.mu.Unlock()

	for _, ps := range streams {
		ps.Close()
	}
}

// Broadcast sends a MemexMessage frame to all active stream connections in the pool.
func (p *PeerStreamPool) Broadcast(msg *membusspb.MemexMessage) {
	if msg == nil {
		return
	}
	p.mu.Lock()
	streams := make([]*pooledStream, 0, len(p.streams))
	for _, ps := range p.streams {
		if !ps.isClosed() {
			streams = append(streams, ps)
		}
	}
	p.mu.Unlock()

	for _, ps := range streams {
		_ = ps.writeFrameLocked(msg)
	}
}

type pooledStream struct {
	mu     sync.Mutex
	pid    peer.ID
	stream network.Stream
	engine *Engine
	queue  *eventQueue
	closed bool
	cancel context.CancelFunc
	ctx    context.Context

	writeMu    sync.Mutex // serialize writes to stream

	// Congestion window and sequence tracking
	seqMu      sync.Mutex
	nextSeq    uint64
	cwnd       int
	inFlight   int
	capCh      chan struct{}
}

func (ps *pooledStream) writeFrameLocked(m *membusspb.MemexMessage) error {
	ps.writeMu.Lock()
	defer ps.writeMu.Unlock()
	return writeFrame(ps.stream, m)
}

func newPooledStream(pid peer.ID, stream network.Stream, engine *Engine) *pooledStream {
	ctx, cancel := context.WithCancel(engine.ctx)
	return &pooledStream{
		pid:      pid,
		stream:   stream,
		engine:   engine,
		queue:    newEventQueue(),
		closed:   false,
		ctx:      ctx,
		cancel:   cancel,
		cwnd:     8, // Initial sliding window
		inFlight: 0,
		capCh:    make(chan struct{}, 128),
	}
}

func (ps *pooledStream) Start() {
	go ps.readLoop()
	go ps.writeLoop()
}

func (ps *pooledStream) isClosed() bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.closed
}

func (ps *pooledStream) Close() {
	ps.mu.Lock()
	if ps.closed {
		ps.mu.Unlock()
		return
	}
	ps.closed = true
	ps.mu.Unlock()

	ps.cancel()
	_ = ps.stream.Reset()
	ps.queue.Close()
	ps.engine.streamPool.RemoveStream(ps.pid, ps)
	if ps.engine.peerWantlist != nil {
		ps.engine.peerWantlist.RemovePeer(ps.pid)
	}
	ps.engine.NotifyPeerFailed(ps.pid)
}

func (ps *pooledStream) PushEvent(ev sessionEvent) {
	ps.queue.Push(ev)
}

func (ps *pooledStream) readLoop() {
	defer ps.Close()
	for {
		if ps.ctx.Err() != nil {
			return
		}
		_ = ps.stream.SetReadDeadline(time.Now().Add(5 * time.Minute))
		buf := readFrame(ps.stream)
		if buf == nil {
			return
		}
		var msg membusspb.MemexMessage
		if err := proto.Unmarshal(buf, &msg); err != nil {
			return
		}

		// Adjust Congestion Window (AIMD: Additive Increase on successful responses)
		ps.seqMu.Lock()
		resolvedCount := len(msg.Blocks) + len(msg.HaveMids) + len(msg.DontHaves)
		if resolvedCount > 0 {
			if len(msg.Blocks) > 0 || len(msg.HaveMids) > 0 {
				// Increase window size on positive responses
				if ps.cwnd < 128 {
					ps.cwnd++
				}
			}
			// Reduce in-flight wants
			ps.inFlight -= resolvedCount
			if ps.inFlight < 0 {
				ps.inFlight = 0
			}
			// Signal writeLoop capacity opened
			for i := 0; i < resolvedCount; i++ {
				select {
				case ps.capCh <- struct{}{}:
				default:
				}
			}
		}
		ps.seqMu.Unlock()

		// Store ObjectInfo metadata if present
		if len(msg.ObjectInfos) > 0 {
			ps.engine.StoreObjectInfos(msg.ObjectInfos)
		}

		// Deliver incoming blocks to verifier pool
		for _, b := range msg.Blocks {
			if b == nil || b.Mid == "" {
				continue
			}
			select {
			case ps.engine.verifierCh <- verifierJob{block: b, from: ps.pid}:
			case <-ps.ctx.Done():
				return
			}
		}

		// Process positive HAVE responses (from WANT_HAVE queries)
		for _, haveMidStr := range msg.HaveMids {
			id, err := mid.Parse(haveMidStr)
			if err != nil {
				continue
			}
			ps.engine.NotifyPeerHasBlock(id, ps.pid)
		}

		// Process negative ACKs / DONT_HAVE
		for _, dontHaveMidStr := range msg.DontHaves {
			id, err := mid.Parse(dontHaveMidStr)
			if err != nil {
				continue
			}
			ps.engine.NotifyBlockFailed(id, ps.pid)
		}

		// Handle wants & cancels sent by the remote peer
		if len(msg.Wants) > 0 || len(msg.Cancel) > 0 {
			resp := ps.engine.HandleRemoteWants(ps.pid, msg.Wants, msg.Cancel)
			if len(resp.Wants)+len(resp.Blocks)+len(resp.HaveMids)+len(resp.DontHaves)+len(resp.Cancel) > 0 {
				_ = ps.stream.SetWriteDeadline(time.Now().Add(DefaultPeerTimeout))
				if err := ps.writeFrameLocked(resp); err != nil {
					return
				}
			}
		}
	}
}

func (ps *pooledStream) writeLoop() {
	defer ps.Close()
	const maxBatchSize = 32
	const flushTimeout = 5 * time.Millisecond

	var pending []sessionEvent
	inFlightMIDs := make(map[string]struct{})

	for {
		var firstEv sessionEvent
		var gotFirst bool
		if len(pending) > 0 {
			firstEv = pending[0]
			pending = pending[1:]
			gotFirst = true
		} else {
			select {
			case <-ps.ctx.Done():
				return
			case _, ok := <-ps.queue.ch:
				events := ps.queue.PopAll()
				if len(events) > 0 {
					firstEv = events[0]
					pending = append(pending, events[1:]...)
					gotFirst = true
				}
				if !ok && len(pending) == 0 && !gotFirst {
					return
				}
			}
		}

		if !gotFirst {
			continue
		}

		// Congestion Control check for wants
		if !firstEv.isCancel {
			ps.seqMu.Lock()
			currentCwnd := ps.cwnd
			currentInFlight := ps.inFlight
			ps.seqMu.Unlock()

			for currentInFlight >= currentCwnd {
				select {
				case <-ps.ctx.Done():
					return
				case <-ps.capCh:
					ps.seqMu.Lock()
					ps.inFlight--
					if ps.inFlight < 0 {
						ps.inFlight = 0
					}
					currentInFlight = ps.inFlight
					currentCwnd = ps.cwnd
					ps.seqMu.Unlock()
				case _, ok := <-ps.queue.ch:
					events := ps.queue.PopAll()
					for _, ev := range events {
						if ev.isCancel {
							// Write cancel immediately
							msg := membusspb.MemexMessage{
								Cancel: []string{ev.mid.String()},
							}
							if _, ok := inFlightMIDs[ev.mid.String()]; ok {
								delete(inFlightMIDs, ev.mid.String())
								ps.seqMu.Lock()
								ps.inFlight--
								if ps.inFlight < 0 {
									ps.inFlight = 0
								}
								currentInFlight = ps.inFlight
								ps.seqMu.Unlock()
								select {
								case ps.capCh <- struct{}{}:
								default:
								}
							}
							_ = ps.stream.SetWriteDeadline(time.Now().Add(DefaultPeerTimeout))
							_ = ps.writeFrameLocked(&msg)
						} else {
							pending = append(pending, ev)
						}
					}
					if !ok {
						return
					}
				}
			}
		}

		// Build batch
		var msg membusspb.MemexMessage
		newWantCount := 0

		addEvent := func(ev sessionEvent) {
			if ev.isCancel {
				foundInBatch := false
				for i, w := range msg.Wants {
					if w.Mid == ev.mid.String() {
						msg.Wants = append(msg.Wants[:i], msg.Wants[i+1:]...)
						newWantCount--
						delete(inFlightMIDs, ev.mid.String())
						foundInBatch = true
						break
					}
				}
				if !foundInBatch {
					msg.Cancel = append(msg.Cancel, ev.mid.String())
					if _, ok := inFlightMIDs[ev.mid.String()]; ok {
						delete(inFlightMIDs, ev.mid.String())
						ps.seqMu.Lock()
						ps.inFlight--
						if ps.inFlight < 0 {
							ps.inFlight = 0
						}
						ps.seqMu.Unlock()
						select {
						case ps.capCh <- struct{}{}:
						default:
						}
					}
				}
			} else {
				msg.Wants = append(msg.Wants, &membusspb.WantEntry{
					Mid:          ev.mid.String(),
					SendDontHave: true,
				})
				newWantCount++
				inFlightMIDs[ev.mid.String()] = struct{}{}
			}
		}

		addEvent(firstEv)

		batchCount := 1
		timer := time.NewTimer(flushTimeout)
		closed := false

	batchLoop:
		for batchCount < maxBatchSize && !closed {
			if len(pending) > 0 {
				nextEv := pending[0]
				pending = pending[1:]
				if !nextEv.isCancel {
					ps.seqMu.Lock()
					currentCwnd := ps.cwnd
					currentInFlight := ps.inFlight
					ps.seqMu.Unlock()
					if currentInFlight+newWantCount >= currentCwnd {
						pending = append([]sessionEvent{nextEv}, pending...)
						break batchLoop
					}
				}
				addEvent(nextEv)
				batchCount++
				continue
			}

			select {
			case <-ps.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				break batchLoop
			case _, ok := <-ps.queue.ch:
				events := ps.queue.PopAll()
				pending = append(pending, events...)
				if !ok {
					closed = true
				}
			}
		}
		timer.Stop()

		if len(msg.Wants) == 0 && len(msg.Cancel) == 0 {
			if closed && len(pending) == 0 {
				return
			}
			continue
		}

		// Sequence number assignment
		ps.seqMu.Lock()
		msg.SequenceNumber = ps.nextSeq
		ps.nextSeq++
		ps.inFlight += newWantCount
		ps.seqMu.Unlock()

		_ = ps.stream.SetWriteDeadline(time.Now().Add(DefaultPeerTimeout))
		if err := ps.writeFrameLocked(&msg); err != nil {
			// Decrement congestion window on packet write errors
			ps.seqMu.Lock()
			ps.cwnd = ps.cwnd / 2
			if ps.cwnd < 2 {
				ps.cwnd = 2
			}
			ps.seqMu.Unlock()
			return
		}

		if closed && len(pending) == 0 {
			return
		}
	}
}
