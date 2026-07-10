// Phase 11: Mem-Herald relay announcer.
//
// Anchor nodes and any node with RelayService=true re-publish
// their presence as providers of the DHT's relay discovery namespace. This
// keeps the relay set fresh in
// the face of DHT churn and gives freshly bootstrapping nodes
// a non-empty candidate set for AutoRelay.
//
// The announcer is intentionally tiny: a single ticker
// goroutine. Republish errors are logged and swallowed; the
// next tick will try again. The announcer does not advertise
// other peers' addresses; it only advertises the local node.
package herald

import (
	"context"
	"errors"
	"sync"
	"time"
)

// MaxRelayAdvertisementInterval matches routing discovery's advertised TTL.
// Refreshing less often creates avoidable gaps when provider records expire.
const MaxRelayAdvertisementInterval = 3 * time.Hour

// RelayAnnouncer periodically publishes the local node to
// the DHT's relay list. The lifecycle is Start/Stop; Start
// also fires one immediate publish so the node appears in
// the relay list right at startup.
type RelayAnnouncer struct {
	// DHT is the local DHT facade. Required.
	DHT RelayPublisher
	// Interval is the time between republishes. Zero
	// defaults to three hours and is capped at that value.
	Interval time.Duration
	// BootCheckInterval is the polling interval for checking
	// DHT routing table size before the first publish. Zero
	// defaults to 5 seconds.
	BootCheckInterval time.Duration
	// Now overrides the wall clock for tests. Default is
	// time.Now.
	Now func() time.Time
	// Logger is an optional structured logger; nil means
	// silent. The daemon wires its slog logger here.
	Logger AnnouncerLogger

	state *relayAnnouncerState
}

// RelayPublisher is the slice of *dht.MemDHT that the relay
// announcer needs. Defining the interface here (rather than
// importing the concrete type) keeps the announcer decoupled
// from the DHT package and trivial to test with a fake.
type RelayPublisher interface {
	PublishAsRelay(ctx context.Context) error
	RoutingTableSize() int
}

// AnnouncerLogger is the minimal logging surface the
// announcer needs. *slog.Logger satisfies this.
type AnnouncerLogger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type relayAnnouncerState struct {
	startOnce sync.Once
}

func newRelayAnnouncerState() *relayAnnouncerState {
	return &relayAnnouncerState{}
}

// NewRelayAnnouncer validates the config and returns a ready-
// to-use announcer. Callers should call Start in a goroutine
// (or synchronously: Start fires one immediate publish and
// returns).
func NewRelayAnnouncer(cfg RelayAnnouncer) (*RelayAnnouncer, error) {
	if cfg.DHT == nil {
		return nil, errors.New("herald: relay announcer: nil DHT")
	}
	if cfg.Interval <= 0 || cfg.Interval > MaxRelayAdvertisementInterval {
		cfg.Interval = MaxRelayAdvertisementInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &RelayAnnouncer{
		DHT:               cfg.DHT,
		Interval:          cfg.Interval,
		BootCheckInterval: cfg.BootCheckInterval,
		Now:               cfg.Now,
		Logger:            cfg.Logger,
		state:             newRelayAnnouncerState(),
	}, nil
}

// Start launches the background republish loop. It returns
// immediately. It waits in the background until the DHT's
// routing table is non-empty before performing the first
// publish and starting the interval loop.
func (r *RelayAnnouncer) Start(ctx context.Context) {
	r.state.startOnce.Do(func() {
		go func() {
			bootCheckInterval := r.BootCheckInterval
			if bootCheckInterval <= 0 {
				bootCheckInterval = 5 * time.Second
			}
			ticker := time.NewTicker(bootCheckInterval)
			defer ticker.Stop()
			for {
				if r.DHT.RoutingTableSize() > 0 && r.RunOnce(ctx) == nil {
					break
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
			r.loop(ctx)
		}()
	})
}

// loop is the long-lived ticker.
func (r *RelayAnnouncer) loop(ctx context.Context) {
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.RunOnce(ctx)
		}
	}
}

// RunOnce performs a single publish synchronously and
// returns the error (or nil) from the DHT call. The daemon
// ignores the return value; tests assert on it.
func (r *RelayAnnouncer) RunOnce(ctx context.Context) error {
	err := r.DHT.PublishAsRelay(ctx)
	if r.Logger != nil {
		if err != nil {
			r.Logger.Warn("relay announce failed", "err", err.Error())
		} else {
			r.Logger.Info("relay announce ok")
		}
	}
	return err
}
