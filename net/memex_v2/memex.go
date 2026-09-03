package memex_v2

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	corepeerstore "github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/net/swarm"
	ma "github.com/multiformats/go-multiaddr"
	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

// ProtocolID is the full libp2p protocol identifier for Memex v2.
const ProtocolID = protocol.ID("/membuss/memex/2.0.0")

const (
	DefaultSessionTimeout = 30 * time.Second
	DefaultPeerTimeout    = 10 * time.Second
	MaxParallelPeers      = 8
	maxFrameSize          = 16 << 20
	defaultChunkSize      = 256 * 1024
	perBlockTimeout       = 500 * time.Millisecond
	minSessionTimeout     = 30 * time.Second
	maxSessionTimeout     = 5 * time.Minute
)

// Blockstore is the local block storage interface.
type Blockstore interface {
	Put(m mid.MID, data []byte) error
	Get(m mid.MID) ([]byte, error)
	Has(m mid.MID) (bool, error)
}

// EstimateTimeout returns a session timeout proportional to block size.
func EstimateTimeout(contentBytes uint64) time.Duration {
	if contentBytes == 0 {
		return DefaultSessionTimeout
	}
	blocks := (contentBytes + defaultChunkSize - 1) / uint64(defaultChunkSize)
	parallel := uint64(MaxParallelPeers)
	if parallel == 0 {
		parallel = 1
	}
	batches := (blocks + parallel - 1) / parallel
	d := time.Duration(batches) * perBlockTimeout
	if d < minSessionTimeout {
		d = minSessionTimeout
	}
	if d > maxSessionTimeout {
		d = maxSessionTimeout
	}
	return d
}

// Engine is the long-lived Memex v2 node on a libp2p host.
type Engine struct {
	host         host.Host
	bs           Blockstore
	cfg          Config
	wm           *wantManager
	bloom        *BloomManager
	streamPool   *PeerStreamPool
	peerWantlist *PeerWantlistManager

	// Verifier Worker Pool & DB Queue
	verifierCh     chan verifierJob
	dbWriteCh      chan dbWriteJob
	rejectedBlocks uint64
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup

	metricsMu   sync.RWMutex
	peerMetrics map[peer.ID]*peerMetrics

	sessionsMu sync.Mutex
	sessions   map[*Session]struct{}
}

type peerMetrics struct {
	mu         sync.RWMutex
	successes  int
	failures   int
	avgLatency time.Duration
}

type verifierJob struct {
	block *membusspb.Block
	from  peer.ID
	src   *pooledStream
}

type dbWriteJob struct {
	id   mid.MID
	data []byte
}

type Config struct {
	Host       host.Host
	Blockstore Blockstore
	Bloom      *BloomManager

	// AcceptUnsolicited, when non-nil, gates blocks arriving on inbound
	// (server-side) streams — i.e. blocks pushed to us without a matching
	// local want. Returning false drops the block before persistence (no
	// store write, no notifications) and resets the sending stream.
	// Blocks arriving on streams we opened as a client answer our own
	// wants and bypass this gate. Nil means accept all (default, and the
	// historical behavior).
	AcceptUnsolicited func(from peer.ID, m mid.MID) bool
}

// New constructs an Engine.
func New(cfg Config) (*Engine, error) {
	if cfg.Host == nil {
		return nil, errors.New("memex: nil host")
	}
	if cfg.Blockstore == nil {
		return nil, errors.New("memex: nil blockstore")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		host:         cfg.Host,
		bs:           cfg.Blockstore,
		cfg:          cfg,
		wm:           newWantManager(),
		bloom:        cfg.Bloom,
		peerWantlist: newPeerWantlistManager(),
		peerMetrics:  make(map[peer.ID]*peerMetrics),
		verifierCh:   make(chan verifierJob, 2048),
		dbWriteCh:    make(chan dbWriteJob, 2048),
		ctx:          ctx,
		cancel:       cancel,
		sessions:     make(map[*Session]struct{}),
	}
	e.streamPool = newPeerStreamPool(e)
	return e, nil
}

func (e *Engine) Start() {
	e.host.SetStreamHandler(ProtocolID, e.handleStream)

	// Start Verifier Pool
	workers := runtime.NumCPU()
	if workers < 2 {
		workers = 2
	}
	for i := 0; i < workers; i++ {
		e.wg.Add(1)
		go e.verifierWorker()
	}

	// Start Database Batch Writer
	e.wg.Add(1)
	go e.dbBatchWriter()
}

func (e *Engine) Stop() {
	e.host.RemoveStreamHandler(ProtocolID)
	e.streamPool.CloseAll()
	e.cancel()
	e.wg.Wait()
}

func (e *Engine) StopWait(ctx context.Context) error {
	e.host.RemoveStreamHandler(ProtocolID)
	e.streamPool.CloseAll()
	e.cancel()

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) Blockstore() Blockstore             { return e.bs }
func (e *Engine) WantManager() *wantManager          { return e.wm }
func (e *Engine) BloomManager() *BloomManager        { return e.bloom }
func (e *Engine) PeerWantlist() *PeerWantlistManager { return e.peerWantlist }

func (e *Engine) RegisterSession(s *Session) {
	e.sessionsMu.Lock()
	e.sessions[s] = struct{}{}
	e.sessionsMu.Unlock()
}

func (e *Engine) UnregisterSession(s *Session) {
	e.sessionsMu.Lock()
	delete(e.sessions, s)
	e.sessionsMu.Unlock()
}

func (e *Engine) CancelAllSessions() {
	e.sessionsMu.Lock()
	defer e.sessionsMu.Unlock()
	for s := range e.sessions {
		if s.cancel != nil {
			s.cancel()
		}
	}
}

func (e *Engine) NotifyBlockResolved(id mid.MID) {
	e.sessionsMu.Lock()
	defer e.sessionsMu.Unlock()
	for s := range e.sessions {
		s.markResolved(id)
	}
}

func (e *Engine) NotifyPeerHasBlock(id mid.MID, pid peer.ID) {
	e.sessionsMu.Lock()
	defer e.sessionsMu.Unlock()
	for s := range e.sessions {
		s.markPeerHasBlock(id, pid)
	}
}

func (e *Engine) NotifyBlockFailed(id mid.MID, pid peer.ID) {
	e.sessionsMu.Lock()
	defer e.sessionsMu.Unlock()
	for s := range e.sessions {
		s.markFailed(id, pid)
	}
}

func (e *Engine) NotifyPeerFailed(pid peer.ID) {
	e.sessionsMu.Lock()
	defer e.sessionsMu.Unlock()
	for s := range e.sessions {
		s.markPeerFailed(pid)
	}
}

func (e *Engine) StoreObjectInfos(infos map[string]*membusspb.ObjectInfo) {
	type metaPutter interface {
		PutMeta(key string, value []byte) error
	}
	mp, ok := e.bs.(metaPutter)
	if !ok {
		return
	}
	for midStr, oi := range infos {
		if midStr == "" || oi == nil {
			continue
		}
		raw, err := json.Marshal(struct {
			Name     string `json:"name,omitempty"`
			MimeType string `json:"mime_type,omitempty"`
			Size     uint64 `json:"size,omitempty"`
		}{
			Name:     oi.Name,
			MimeType: oi.MimeType,
			Size:     oi.Size,
		})
		if err != nil {
			continue
		}
		_ = mp.PutMeta("obj/"+midStr, raw)
	}
}

// handleStream handles inbound Memex v2 streams.
func (e *Engine) handleStream(s network.Stream) {
	remote := s.Conn().RemotePeer()
	e.streamPool.RegisterInbound(s, remote)
}

func (e *Engine) verifierWorker() {
	defer e.wg.Done()
	for {
		select {
		case <-e.ctx.Done():
			return
		case job, ok := <-e.verifierCh:
			if !ok {
				return
			}
			id, err := mid.Parse(job.block.Mid)
			if err != nil {
				continue
			}
			// Verify cryptographic hash
			actualID := mid.FromBytesWithCodec(job.block.Data, id.Codec())
			if !actualID.Equal(id) {
				e.RecordPeerFailure(job.from)
				log.Printf("memex_v2 verifier: block hash mismatch for MID %s from peer %s", id, job.from)
				continue
			}

			// Unsolicited acceptance gate: blocks arriving on inbound
			// (server-side) streams were pushed to us without a matching
			// local want. Consult the configured policy before persisting.
			// Blocks on client-opened streams answer our own wants and are
			// exempt so local fetches are never gated.
			if job.src != nil && job.src.inbound {
				if accept := e.cfg.AcceptUnsolicited; accept != nil && !accept(job.from, id) {
					atomic.AddUint64(&e.rejectedBlocks, 1)
					_ = job.src.stream.Reset()
					log.Printf("memex_v2 verifier: rejected unsolicited block %s from peer %s (policy)", id, job.from)
					continue
				}
			}

			// Enqueue to batch DB writer
			select {
			case e.dbWriteCh <- dbWriteJob{id: id, data: job.block.Data}:
			case <-e.ctx.Done():
				return
			}
		}
	}
}

func (e *Engine) dbBatchWriter() {
	defer e.wg.Done()
	const maxBatch = 128
	const flushTick = 5 * time.Millisecond
	ticker := time.NewTicker(flushTick)
	defer ticker.Stop()

	var batch []dbWriteJob

	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Write to Badger via Blockstore. Since put is concurrent safe, we execute them.
		// If the Blockstore supports batching, we could leverage it; otherwise Put is fine.
		for _, item := range batch {
			if err := e.bs.Put(item.id, item.data); err != nil {
				log.Printf("memex_v2 writer: failed to save block %s: %v", item.id, err)
				continue
			}
			e.wm.deliver(item.id)
			e.NotifyBlockResolved(item.id)
			e.OpportunisticPushBlock(item.id, item.data)
			e.BroadcastCancel(item.id)
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-e.ctx.Done():
			flush()
			return
		case item, ok := <-e.dbWriteCh:
			if !ok {
				flush()
				return
			}
			batch = append(batch, item)
			if len(batch) >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (e *Engine) serveWants(wants []*membusspb.WantEntry) *membusspb.MemexMessage {
	resp := &membusspb.MemexMessage{}
	for _, w := range wants {
		if w == nil || w.Mid == "" {
			continue
		}
		id, err := mid.Parse(w.Mid)
		if err != nil {
			continue
		}
		has, err := e.bs.Has(id)
		if err != nil {
			continue
		}
		if has {
			data, err := e.bs.Get(id)
			if err != nil {
				continue
			}
			resp.Blocks = append(resp.Blocks, &membusspb.Block{
				Mid:  w.Mid,
				Data: data,
				Size: uint64(len(data)),
			})
			if oi, ok := e.objectInfoFor(id); ok {
				if resp.ObjectInfos == nil {
					resp.ObjectInfos = make(map[string]*membusspb.ObjectInfo)
				}
				resp.ObjectInfos[w.Mid] = oi
			}
			continue
		}
		if w.SendDontHave {
			resp.DontHaves = append(resp.DontHaves, w.Mid)
		}
	}
	return resp
}

func (e *Engine) objectInfoFor(m mid.MID) (*membusspb.ObjectInfo, bool) {
	type metaGetter interface {
		GetMeta(key string) ([]byte, error)
	}
	mg, ok := e.bs.(metaGetter)
	if !ok {
		return nil, false
	}
	raw, err := mg.GetMeta("obj/" + m.String())
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var info struct {
		Name     string `json:"name,omitempty"`
		MimeType string `json:"mime_type,omitempty"`
		Size     uint64 `json:"size,omitempty"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, false
	}
	return &membusspb.ObjectInfo{
		Name:     info.Name,
		MimeType: info.MimeType,
		Size:     info.Size,
	}, true
}

// RejectedUnsolicited reports how many hash-valid unsolicited blocks have
// been dropped by the AcceptUnsolicited policy gate.
func (e *Engine) RejectedUnsolicited() uint64 {
	return atomic.LoadUint64(&e.rejectedBlocks)
}

func (e *Engine) RecordPeerSuccess(pid peer.ID, latency time.Duration) {
	if pid == "" {
		return
	}
	e.metricsMu.Lock()
	m, exists := e.peerMetrics[pid]
	if !exists {
		m = &peerMetrics{}
		e.peerMetrics[pid] = m
	}
	e.metricsMu.Unlock()

	m.mu.Lock()
	m.successes++
	if m.avgLatency == 0 {
		m.avgLatency = latency
	} else {
		m.avgLatency = (m.avgLatency*9 + latency) / 10
	}
	m.mu.Unlock()
}

func (e *Engine) RecordPeerFailure(pid peer.ID) {
	if pid == "" {
		return
	}
	e.metricsMu.Lock()
	m, exists := e.peerMetrics[pid]
	if !exists {
		m = &peerMetrics{}
		e.peerMetrics[pid] = m
	}
	e.metricsMu.Unlock()

	m.mu.Lock()
	m.failures++
	m.mu.Unlock()
}

func (e *Engine) GetPeerLatency(pid peer.ID) time.Duration {
	e.metricsMu.RLock()
	m, exists := e.peerMetrics[pid]
	e.metricsMu.RUnlock()
	if !exists {
		return 150 * time.Millisecond // Sane default RTT
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.avgLatency
}

// readFrame reads length-prefixed protocol frames.
func readFrame(s network.Stream) []byte {
	var lenBuf [4]byte
	if _, err := io.ReadFull(s, lenBuf[:]); err != nil {
		return nil
	}
	l := binary.BigEndian.Uint32(lenBuf[:])
	if l == 0 || l > maxFrameSize {
		return nil
	}
	buf := make([]byte, l)
	if _, err := io.ReadFull(s, buf); err != nil {
		return nil
	}
	return buf
}

func writeFrame(s network.Stream, m *membusspb.MemexMessage) error {
	buf, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	if len(buf) > maxFrameSize {
		return errors.New("memex: frame too large")
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(buf)))
	if _, err := s.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = s.Write(buf)
	return err
}

func (e *Engine) openStream(ctx context.Context, pid peer.ID) (network.Stream, error) {
	cctx, cancel := context.WithTimeout(ctx, DefaultPeerTimeout)
	defer cancel()

	cctx = network.WithAllowLimitedConn(cctx, "membuss-memex-direct")
	cctx = network.WithUseTransient(cctx, "membuss-memex-direct")
	stream, err := e.host.NewStream(cctx, pid, ProtocolID)
	if err == nil {
		return stream, nil
	}

	var relays []peer.ID
	for _, p := range e.host.Peerstore().Peers() {
		protocols, err := e.host.Peerstore().SupportsProtocols(p, "/libp2p/circuit/relay/v2/hop")
		if err == nil && len(protocols) > 0 {
			relays = append(relays, p)
		}
	}
	if len(relays) == 0 {
		relays = e.host.Network().Peers()
	}
	if len(relays) == 0 {
		return nil, fmt.Errorf("direct stream open failed: %w (no relay candidates available)", err)
	}

	var relayAddrs []ma.Multiaddr
	for _, relayID := range relays {
		if relayID == pid || relayID == e.host.ID() {
			continue
		}
		maddrStr := fmt.Sprintf("/p2p/%s/p2p-circuit/p2p/%s", relayID.String(), pid.String())
		maddr, merr := ma.NewMultiaddr(maddrStr)
		if merr == nil {
			relayAddrs = append(relayAddrs, maddr)
		}

		addrs := e.host.Peerstore().Addrs(relayID)
		for _, addr := range addrs {
			var fullRelayAddr ma.Multiaddr
			if !strings.Contains(addr.String(), "/p2p/") {
				p2pPart, perr := ma.NewMultiaddr(fmt.Sprintf("/p2p/%s", relayID.String()))
				if perr == nil {
					fullRelayAddr = addr.Encapsulate(p2pPart)
				}
			} else {
				fullRelayAddr = addr
			}

			if fullRelayAddr != nil {
				circuitPart, cerr := ma.NewMultiaddr(fmt.Sprintf("/p2p-circuit/p2p/%s", pid.String()))
				if cerr == nil {
					relayAddrs = append(relayAddrs, fullRelayAddr.Encapsulate(circuitPart))
				}
			}
		}
	}

	if len(relayAddrs) == 0 {
		return nil, fmt.Errorf("direct stream open failed: %w (could not construct relay addresses)", err)
	}

	e.host.Peerstore().AddAddrs(pid, relayAddrs, corepeerstore.TempAddrTTL)
	if sw, ok := e.host.Network().(*swarm.Swarm); ok {
		sw.Backoff().Clear(pid)
	}

	rctx, rcancel := context.WithTimeout(ctx, DefaultPeerTimeout)
	defer rcancel()

	rctx = network.WithAllowLimitedConn(rctx, "membuss-memex-fallback")
	rctx = network.WithUseTransient(rctx, "membuss-memex-fallback")
	rstream, rerr := e.host.NewStream(rctx, pid, ProtocolID)
	if rerr != nil {
		return nil, fmt.Errorf("direct stream open failed: %w; relay fallback failed: %v", err, rerr)
	}
	return rstream, nil
}

// wantWaiter & wantManager for fanning out block delivery
type wantWaiter struct {
	ch chan mid.MID
}

type wantManager struct {
	mu      sync.Mutex
	waiters map[string][]*wantWaiter
}

func newWantManager() *wantManager {
	return &wantManager{waiters: make(map[string][]*wantWaiter)}
}

func (w *wantManager) subscribe(m mid.MID) *wantWaiter {
	wt := &wantWaiter{ch: make(chan mid.MID, 1)}
	w.mu.Lock()
	w.waiters[m.String()] = append(w.waiters[m.String()], wt)
	w.mu.Unlock()
	return wt
}

func (w *wantManager) unsubscribe(m mid.MID, wt *wantWaiter) {
	w.mu.Lock()
	defer w.mu.Unlock()
	list := w.waiters[m.String()]
	for i, x := range list {
		if x == wt {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(w.waiters, m.String())
	} else {
		w.waiters[m.String()] = list
	}
}

func (w *wantManager) deliver(m mid.MID) {
	w.mu.Lock()
	list := w.waiters[m.String()]
	delete(w.waiters, m.String())
	w.mu.Unlock()
	for _, wt := range list {
		select {
		case wt.ch <- m:
		default:
		}
	}
}

type ReadResult struct {
	Reader io.Reader
	Root   mid.MID
}
