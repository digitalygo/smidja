package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMailboxDir      = ".smidja"
	defaultGatewaySubdir   = "gateway"
	defaultJournalKeep     = 1000
	defaultDeliveryBuffer  = 64
	defaultBurst           = 10
	defaultRefillPerMinute = 10
	defaultMaxInboundBytes = 1 << 20
)

type Options struct {
	Dir                string
	MailboxSize        int
	MaxActiveTurns     int
	JournalKeep        int
	DeliveryBuffer     int
	Burst              int
	RefillPerMinute    float64
	MaxActiveAdmission int
	MaxInboundBytes    int
	Resolver           Resolver
	Runner             TurnRunner
}

type noopRunner struct{}

func (noopRunner) Run(ctx context.Context, work WorkItem) (RunResult, error) {
	return RunResult{}, nil
}

type Gateway struct {
	opts      Options
	journal   *Journal
	scheduler *Scheduler
	limiter   *RateLimiter
	router    *Router
	delivery  *DeliveryDispatcher
	sinksMu   sync.RWMutex
	sinks     map[string]DeliverySink
	started   atomic.Bool
	cancel    context.CancelFunc
}

func New(opts Options) (*Gateway, error) {
	opts = withDefaults(opts)
	dir := opts.Dir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("gateway: resolve home dir: %w", err)
		}
		dir = filepath.Join(home, defaultMailboxDir, defaultGatewaySubdir)
	}
	journal, err := OpenJournal(dir, opts.JournalKeep)
	if err != nil {
		return nil, err
	}
	sched := NewScheduler(opts.MaxActiveTurns)
	limiter := NewRateLimiter(RateLimiterOptions{
		Burst:           opts.Burst,
		RefillPerMinute: opts.RefillPerMinute,
		MaxActive:       opts.MaxActiveAdmission,
	})
	g := &Gateway{
		opts:      opts,
		journal:   journal,
		scheduler: sched,
		limiter:   limiter,
		sinks:     make(map[string]DeliverySink),
	}
	g.delivery = NewDeliveryDispatcher(opts.DeliveryBuffer, g.routeDelivery)
	g.router = NewRouter(opts.Resolver, func(key, workspace, sessionHint string) *Actor {
		return NewActor(ActorConfig{
			Key:         key,
			Workspace:   workspace,
			SessionHint: sessionHint,
			ResolveHint: func() string {
				_, hint := opts.Resolver(key)
				return hint
			},
			MailboxSize: opts.MailboxSize,
			Runner:      opts.Runner,
			Marker:      journal,
			Scheduler:   sched,
			Deliver:     func(d Delivery) { g.delivery.Send(d) },
			EntriesDone: func() {},
			EndTurn:     func() { limiter.EndTurn() },
		})
	})
	return g, nil
}

func withDefaults(opts Options) Options {
	if opts.MailboxSize <= 0 {
		opts.MailboxSize = defaultMailboxSize
	}
	if opts.MaxActiveTurns <= 0 {
		opts.MaxActiveTurns = 4
	}
	if opts.JournalKeep <= 0 {
		opts.JournalKeep = defaultJournalKeep
	}
	if opts.DeliveryBuffer <= 0 {
		opts.DeliveryBuffer = defaultDeliveryBuffer
	}
	if opts.Burst <= 0 {
		opts.Burst = defaultBurst
	}
	if opts.RefillPerMinute <= 0 {
		opts.RefillPerMinute = defaultRefillPerMinute
	}
	if opts.MaxInboundBytes <= 0 {
		opts.MaxInboundBytes = defaultMaxInboundBytes
	}
	if opts.Resolver == nil {
		opts.Resolver = func(string) (string, string) { return "", "" }
	}
	if opts.Runner == nil {
		opts.Runner = noopRunner{}
	}
	return opts
}

func (g *Gateway) Start(ctx context.Context) error {
	if !g.started.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}
	gctx, cancel := context.WithCancel(ctx)
	g.cancel = cancel
	g.router.Start(gctx)
	g.delivery.Start()
	pending, err := g.journal.ReplayPending()
	if err != nil {
		return err
	}
	for _, rec := range pending {
		if err := g.resume(rec); err != nil {
			continue
		}
	}
	return nil
}

func (g *Gateway) Submit(ctx context.Context, msg InboundMessage) (Receipt, error) {
	if !g.started.Load() {
		return Receipt{}, ErrClosed
	}
	if err := validateInbound(msg); err != nil {
		return Receipt{}, err
	}
	if err := g.limiter.CheckSize(msg.Text, g.opts.MaxInboundBytes); err != nil {
		return Receipt{}, err
	}
	if !g.limiter.AllowUser(msg.UserIDHash, time.Now()) {
		return Receipt{}, ErrRateLimited
	}
	if !g.limiter.TryBeginTurn() {
		return Receipt{}, ErrTooManyActive
	}
	rec := Record{
		ID:              msg.ID,
		Ts:              time.Now().UTC(),
		Transport:       msg.Transport,
		ExternalChatKey: msg.ExternalChatKey,
		UserIDHash:      msg.UserIDHash,
		Text:            msg.Text,
		Status:          StatusAccepted,
	}
	inserted, err := g.journal.AppendUnique(rec)
	if err != nil {
		g.limiter.EndTurn()
		return Receipt{}, err
	}
	if !inserted {
		g.limiter.EndTurn()
		return Receipt{}, ErrDuplicate
	}
	actor, err := g.router.Actor(RoutingKey(msg.Transport, msg.ExternalChatKey))
	if err != nil {
		g.limiter.EndTurn()
		g.journal.MarkCancelled(msg.ID)
		return Receipt{}, err
	}
	receipt, err := actor.Submit(msg)
	if err != nil {
		g.limiter.EndTurn()
		g.journal.MarkCancelled(msg.ID)
		return Receipt{}, err
	}
	return receipt, nil
}

func (g *Gateway) resume(rec Record) error {
	msg := InboundMessage{
		ID:              rec.ID,
		Transport:       rec.Transport,
		ExternalChatKey: rec.ExternalChatKey,
		UserIDHash:      rec.UserIDHash,
		Text:            rec.Text,
		SessionID:       rec.SessionID,
	}
	actor, err := g.router.Actor(RoutingKey(rec.Transport, rec.ExternalChatKey))
	if err != nil {
		return err
	}
	if _, err := actor.Submit(msg); err != nil {
		g.journal.MarkFailed(rec.ID, "mailbox_full")
		return err
	}
	return nil
}

func (g *Gateway) RegisterSink(transport string, sink DeliverySink) {
	g.sinksMu.Lock()
	defer g.sinksMu.Unlock()
	if sink == nil {
		delete(g.sinks, transport)
		return
	}
	g.sinks[transport] = sink
}

func (g *Gateway) Cancel(transport, externalChatKey string) bool {
	actor, ok := g.router.Lookup(RoutingKey(transport, externalChatKey))
	if !ok {
		return false
	}
	if !actor.Busy() {
		return false
	}
	actor.CancelCurrent()
	return true
}

func (g *Gateway) routeDelivery(d Delivery) {
	g.sinksMu.RLock()
	sink, ok := g.sinks[d.Transport]
	g.sinksMu.RUnlock()
	if !ok {
		return
	}
	sink.Deliver(context.Background(), d)
}

func (g *Gateway) Shutdown(ctx context.Context) error {
	if !g.started.CompareAndSwap(true, false) {
		return ErrNotStarted
	}
	if g.cancel != nil {
		g.cancel()
	}
	var shutdownErr error
	done := make(chan struct{})
	go func() {
		g.router.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		shutdownErr = ctx.Err()
	}
	g.delivery.Stop()
	closeErr := g.journal.Close()
	if shutdownErr != nil {
		return shutdownErr
	}
	return closeErr
}

func (g *Gateway) Journal() *Journal {
	return g.journal
}

func (g *Gateway) RateLimiter() *RateLimiter {
	return g.limiter
}

func (g *Gateway) Scheduler() *Scheduler {
	return g.scheduler
}

func validateInbound(msg InboundMessage) error {
	switch {
	case msg.ID == "":
		return fmt.Errorf("%w: empty id", ErrInvalidMessage)
	case msg.Transport == "":
		return fmt.Errorf("%w: empty transport", ErrInvalidMessage)
	case msg.ExternalChatKey == "":
		return fmt.Errorf("%w: empty external chat key", ErrInvalidMessage)
	case msg.UserIDHash == "":
		return fmt.Errorf("%w: empty user id hash", ErrInvalidMessage)
	}
	return nil
}
