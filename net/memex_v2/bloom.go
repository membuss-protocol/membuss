package memex_v2

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bits-and-blooms/bloom/v3"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

// BloomProtocolID is the libp2p protocol used to carry
// BloomAnnouncement messages.
const BloomProtocolID = protocol.ID("/membuss/memex-bloom/2.0.0")

// SealedLister is the subset of the store the BloomManager needs.
type SealedLister interface {
	AllSealed() ([]mid.MID, error)
}

// PeerLister is the only method we need from a host.
type PeerLister interface {
	Network() network.Network
}

// BloomConfig configures a BloomManager.
type BloomConfig struct {
	Host     host.Host
	Sealed   SealedLister
	Capacity uint
	FPRate   float64
	Interval time.Duration
}

// DefaultBloomConfig returns a config with safe defaults.
func DefaultBloomConfig() BloomConfig {
	return BloomConfig{
		Capacity: 1_000_000,
		FPRate:   0.01,
		Interval: 5 * time.Minute,
	}
}

// BloomManager owns the local announcement loop and the map of remote-peer filters.
type BloomManager struct {
	host     host.Host
	sealed   SealedLister
	interval time.Duration

	mu    sync.RWMutex
	local *localBloom
	peers map[peer.ID]*remoteBloom

	stop chan struct{}
	done chan struct{}
}

type localBloom struct {
	filter   *bloom.BloomFilter
	capacity uint
	fpRate   float64
	count    uint32
}

type remoteBloom struct {
	filter   *bloom.BloomFilter
	received time.Time
}

// NewBloomManager constructs a manager.
func NewBloomManager(cfg BloomConfig) (*BloomManager, error) {
	if cfg.Host == nil {
		return nil, errors.New("memex bloom: nil host")
	}
	if cfg.Capacity == 0 {
		cfg.Capacity = 1_000_000
	}
	if cfg.FPRate <= 0 {
		cfg.FPRate = 0.01
	}
	if cfg.Interval == 0 {
		cfg.Interval = 5 * time.Minute
	}
	return &BloomManager{
		host:     cfg.Host,
		sealed:   cfg.Sealed,
		interval: cfg.Interval,
		local: &localBloom{
			filter:   bloom.NewWithEstimates(cfg.Capacity, cfg.FPRate),
			capacity: cfg.Capacity,
			fpRate:   cfg.FPRate,
		},
		peers: make(map[peer.ID]*remoteBloom),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}, nil
}

func (m *BloomManager) Start() {
	m.host.SetStreamHandler(BloomProtocolID, m.handleStream)
	if m.interval > 0 {
		go m.loop()
	} else {
		go func() { <-m.stop; close(m.done) }()
	}
}

func (m *BloomManager) Stop() {
	m.host.RemoveStreamHandler(BloomProtocolID)
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
	<-m.done
}

func (m *BloomManager) localAnnouncement() (*membusspb.BloomAnnouncement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := m.local.filter.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("memex bloom: marshal local: %w", err)
	}
	return &membusspb.BloomAnnouncement{
		BloomFilter: data,
		ItemCount:   uint64(m.local.count),
		Capacity:    uint64(m.local.capacity),
		FpRate:      m.local.fpRate,
	}, nil
}

func (m *BloomManager) rebuildLocked() error {
	if m.sealed == nil {
		return nil
	}
	mids, err := m.sealed.AllSealed()
	if err != nil {
		return fmt.Errorf("memex bloom: list sealed: %w", err)
	}
	fresh := bloom.NewWithEstimates(m.local.capacity, m.local.fpRate)
	for _, x := range mids {
		fresh.Add(x.Bytes())
	}
	m.local.filter = fresh
	m.local.count = uint32(len(mids))
	return nil
}

func (m *BloomManager) RefreshLocal(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rebuildLocked()
}

func (m *BloomManager) AddSealed(x mid.MID) {
	if x.IsZero() {
		return
	}
	m.mu.Lock()
	if m.local.filter != nil {
		m.local.filter.Add(x.Bytes())
		m.local.count++
	}
	m.mu.Unlock()
}

func (m *BloomManager) loop() {
	defer close(m.done)
	t := time.NewTicker(m.interval)
	defer t.Stop()

	_ = m.RefreshLocal(context.Background())

	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
		}
		if err := m.RefreshLocal(context.Background()); err != nil {
			continue
		}
		m.broadcastAll(context.Background())
	}
}

func (m *BloomManager) broadcastAll(ctx context.Context) {
	peers := m.host.Network().Peers()
	ann, err := m.localAnnouncement()
	if err != nil {
		return
	}
	for _, pid := range peers {
		if pid == m.host.ID() {
			continue
		}
		_ = m.sendOne(ctx, pid, ann)
	}
}

func (m *BloomManager) sendOne(ctx context.Context, pid peer.ID, ann *membusspb.BloomAnnouncement) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stream, err := m.host.NewStream(cctx, pid, BloomProtocolID)
	if err != nil {
		return err
	}
	defer stream.Close()
	_ = stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
	buf, err := proto.Marshal(ann)
	if err != nil {
		return err
	}
	var lenBuf [4]byte
	lenBuf[0] = byte(len(buf) >> 24)
	lenBuf[1] = byte(len(buf) >> 16)
	lenBuf[2] = byte(len(buf) >> 8)
	lenBuf[3] = byte(len(buf))
	if _, err := stream.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := stream.Write(buf); err != nil {
		return err
	}
	return nil
}

func (m *BloomManager) handleStream(s network.Stream) {
	defer s.Close()
	remote := s.Conn().RemotePeer()
	_ = s.SetReadDeadline(time.Now().Add(10 * time.Second))

	var lenBuf [4]byte
	if _, err := readFull(s, lenBuf[:]); err != nil {
		return
	}
	l := uint32(lenBuf[0])<<24 | uint32(lenBuf[1])<<16 | uint32(lenBuf[2])<<8 | uint32(lenBuf[3])
	if l == 0 || l > 16<<20 {
		return
	}
	body := make([]byte, l)
	if _, err := readFull(s, body); err != nil {
		return
	}
	var ann membusspb.BloomAnnouncement
	if err := proto.Unmarshal(body, &ann); err != nil {
		return
	}
	if len(ann.BloomFilter) == 0 {
		return
	}
	bf := &bloom.BloomFilter{}
	if err := bf.UnmarshalBinary(ann.BloomFilter); err != nil {
		return
	}
	m.mu.Lock()
	m.peers[remote] = &remoteBloom{filter: bf, received: time.Now()}
	m.mu.Unlock()
}

func (m *BloomManager) PeerLikelyHas(pid peer.ID, want mid.MID) bool {
	if want.IsZero() {
		return true
	}
	m.mu.RLock()
	rb, ok := m.peers[pid]
	m.mu.RUnlock()
	if !ok || rb == nil || rb.filter == nil {
		return true
	}
	return rb.filter.Test(want.Bytes())
}

func (m *BloomManager) FilteredProviders(want mid.MID, providers []peer.AddrInfo) []peer.AddrInfo {
	if len(providers) == 0 {
		return providers
	}
	out := make([]peer.AddrInfo, 0, len(providers))
	for _, p := range providers {
		if m.PeerLikelyHas(p.ID, want) {
			out = append(out, p)
		}
	}
	return out
}

func readFull(s network.Stream, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := s.Read(buf[read:])
		if n > 0 {
			read += n
		}
		if err != nil {
			return read, err
		}
	}
	return read, nil
}
