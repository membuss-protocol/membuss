package memex_v2

import (
	"context"
	"io"
	"log"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

type memexBlockstoreAdapter struct {
	bs      Blockstore
	session *Session
}

func asBlockstore(bs Blockstore, session *Session) store.Blockstore {
	return &memexBlockstoreAdapter{bs: bs, session: session}
}

func (a *memexBlockstoreAdapter) Put(m mid.MID, data []byte) error {
	return a.bs.Put(m, data)
}

func (a *memexBlockstoreAdapter) Get(m mid.MID) ([]byte, error) {
	data, err := a.bs.Get(m)
	if err == nil {
		return data, nil
	}

	wt := a.session.cfg.Engine.wm.subscribe(m)
	defer a.session.cfg.Engine.wm.unsubscribe(m, wt)

	a.session.checkAndEnqueue(a.session.ctx, m)

	select {
	case <-wt.ch:
		return a.bs.Get(m)
	case <-a.session.ctx.Done():
		return nil, a.session.ctx.Err()
	}
}

func (a *memexBlockstoreAdapter) Has(m mid.MID) (bool, error) {
	return a.bs.Has(m)
}

func (a *memexBlockstoreAdapter) Delete(m mid.MID) error {
	return nil
}

func (a *memexBlockstoreAdapter) PutMeta(key string, value []byte) error {
	return nil
}

func (a *memexBlockstoreAdapter) GetMeta(key string) ([]byte, error) {
	return nil, store.ErrNotFound
}

type RetryConfig struct {
	Initial     time.Duration
	Max         time.Duration
	Factor      float64
	MaxAttempts int
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		Initial:     100 * time.Millisecond,
		Max:         30 * time.Second,
		Factor:      2.0,
		MaxAttempts: 4,
	}
}

func (s *Session) FetchWithBackoff(ctx context.Context, cfg RetryConfig) (io.Reader, error) {
	return s.Fetch(ctx)
}

func (s *Session) schedulerLoop(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scheduleWants()
		case <-s.schedulerWakeCh:
			s.scheduleWants()
		}
	}
}

func (s *Session) scheduleWants() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	const maxBlockAttempts = 12

	for midStr, ws := range s.wantStates {
		if _, ok := s.resolved[midStr]; ok {
			delete(s.wantStates, midStr)
			continue
		}

		blockTimeout := 3 * time.Second
		if ws.currentProvider != "" {
			lat := s.cfg.Engine.GetPeerLatency(ws.currentProvider)
			blockTimeout = lat * 3
			if blockTimeout < 2500*time.Millisecond {
				blockTimeout = 2500 * time.Millisecond
			}
			if blockTimeout > 5*time.Second {
				blockTimeout = 5 * time.Second
			}
		}

		needsScheduling := false
		if ws.currentProvider == "" {
			needsScheduling = true
		} else if now.Sub(ws.lastSent) > blockTimeout {
			ws.triedProviders[ws.currentProvider] = struct{}{}
			s.cfg.Engine.RecordPeerFailure(ws.currentProvider)
			ws.currentProvider = ""
			needsScheduling = true
		}

		if !needsScheduling {
			continue
		}

		candidates := s.selectPeersForMID(ws.mid)
		if len(candidates) == 0 {
			candidates = s.cfg.Providers
		}

		s.provMu.Lock()
		seenCands := make(map[peer.ID]struct{})
		for _, c := range candidates {
			seenCands[c.ID] = struct{}{}
		}
		for _, lp := range s.liveProviders {
			if _, already := seenCands[lp.ID]; !already {
				candidates = append(candidates, lp)
			}
		}

		type activeCandidate struct {
			peerID peer.ID
			stream *pooledStream
		}
		var activeList []activeCandidate
		for pid, ps := range s.activeProviders {
			if ps.isClosed() {
				continue
			}
			isCand := false
			for _, c := range candidates {
				if c.ID == pid {
					isCand = true
					break
				}
			}
			if isCand {
				if _, tried := ws.triedProviders[pid]; !tried {
					activeList = append(activeList, activeCandidate{peerID: pid, stream: ps})
				}
			}
		}
		s.provMu.Unlock()

		if len(activeList) == 0 {
			ws.triedProviders = make(map[peer.ID]struct{})
			ws.attempts++
			if ws.attempts < maxBlockAttempts {
				s.wakeProviderManager()
			}
			continue
		}

		if ws.attempts >= maxBlockAttempts {
			log.Printf("memex_v2 session: block %s failed after maximum download attempts", ws.mid)
			continue
		}

		total := len(s.enqueued)
		resolved := len(s.resolved)
		remaining := total - resolved
		isEndgame := remaining > 0 && (remaining <= 3 || float64(remaining)/float64(total) <= 0.05)

		if isEndgame {
			ws.currentProvider = "endgame"
			ws.lastSent = now
			for _, ac := range activeList {
				ac.stream.PushEvent(sessionEvent{isCancel: false, mid: ws.mid})
			}
		} else {
			var selected activeCandidate
			maxEffectiveScore := -1.0
			for _, ac := range activeList {
				load := 0
				for _, otherWs := range s.wantStates {
					if otherWs.currentProvider == ac.peerID {
						load++
					}
				}
				lat := s.cfg.Engine.GetPeerLatency(ac.peerID)
				score := 1.0 / (float64(lat.Milliseconds()) + 1.0)
				effectiveScore := score / float64(load+1)
				if maxEffectiveScore == -1.0 || effectiveScore > maxEffectiveScore {
					maxEffectiveScore = effectiveScore
					selected = ac
				}
			}

			ws.currentProvider = selected.peerID
			ws.lastSent = now
			selected.stream.PushEvent(sessionEvent{isCancel: false, mid: ws.mid})
		}
	}
}

func (s *Session) providerManagerLoop(ctx context.Context, fanout int) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.manageProviders(ctx, fanout)
		case <-s.managerWakeCh:
			s.manageProviders(ctx, fanout)
		}
	}
}

func (s *Session) manageProviders(ctx context.Context, fanout int) {
	s.mu.Lock()
	hasPending := len(s.wantStates) > 0
	searchMID := s.cfg.Root
	for _, ws := range s.wantStates {
		searchMID = ws.mid
		break
	}
	s.mu.Unlock()

	s.provMu.Lock()
	defer s.provMu.Unlock()

	for pid, ps := range s.activeProviders {
		if ps.isClosed() {
			delete(s.activeProviders, pid)
		}
	}

	needed := fanout - len(s.activeProviders)
	if needed <= 0 {
		return
	}

	var toStart []peer.AddrInfo
	for _, p := range s.liveProviders {
		if _, active := s.activeProviders[p.ID]; active {
			continue
		}
		if _, failed := s.failedProviders[p.ID]; failed {
			continue
		}
		toStart = append(toStart, p)
		if len(toStart) >= needed {
			break
		}
	}

	for _, p := range toStart {
		// Unlock to avoid blocking other concurrent session events (e.g. cancels/resolves)
		// during potentially slow network dialing.
		s.provMu.Unlock()
		ps, err := s.cfg.Engine.streamPool.GetOrCreateStream(ctx, p.ID)
		s.provMu.Lock()

		if err != nil {
			s.failedProviders[p.ID] = struct{}{}
			continue
		}
		s.activeProviders[p.ID] = ps
	}

	if len(s.activeProviders) < fanout && s.cfg.ProviderFinder != nil {
		if hasPending {
			go func(m mid.MID) {
				discCtx, discCancel := context.WithTimeout(ctx, 5*time.Second)
				defer discCancel()
				newProvs, err := s.cfg.ProviderFinder(discCtx, m)
				if err != nil || len(newProvs) == 0 {
					return
				}
				s.provMu.Lock()
				for _, np := range newProvs {
					exists := false
					for _, lp := range s.liveProviders {
						if lp.ID == np.ID {
							exists = true
							break
						}
					}
					if !exists {
						s.liveProviders = append(s.liveProviders, np)
					}
				}
				s.provMu.Unlock()
				s.wakeProviderManager()
			}(searchMID)
		}
	}
}
