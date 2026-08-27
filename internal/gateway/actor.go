package gateway

import (
	"context"
	"sync"
)

const defaultMailboxSize = 8

type TurnMarker interface {
	MarkStarted(id, sessionID string) error
	MarkCompleted(id string) error
	MarkFailed(id, errClass string) error
	MarkCancelled(id string) error
	MarkOutcomeUnknown(id string) error
}

type scheduler interface {
	Acquire(ctx context.Context, workspace string) error
	Release(workspace string)
}

type ActorConfig struct {
	Key         string
	Workspace   string
	SessionHint string
	ResolveHint func() string
	MailboxSize int
	Runner      TurnRunner
	Marker      TurnMarker
	Scheduler   scheduler
	Deliver     func(Delivery)
	EntriesDone func()
	EndTurn     func()
}

type Actor struct {
	key         string
	workspace   string
	sessionHint string
	resolveHint func() string
	runner      TurnRunner
	marker      TurnMarker
	sched       scheduler
	deliver     func(Delivery)
	entriesDone func()
	endTurn     func()
	mailbox     chan InboundMessage
	wg          sync.WaitGroup

	mu            sync.Mutex
	started       bool
	closed        bool
	busy          bool
	ctx           context.Context
	cancelCtx     context.CancelFunc
	currentCancel context.CancelFunc
}

type noopMarker struct{}

func (noopMarker) MarkStarted(string, string) error { return nil }
func (noopMarker) MarkCompleted(string) error       { return nil }
func (noopMarker) MarkFailed(string, string) error  { return nil }
func (noopMarker) MarkCancelled(string) error       { return nil }
func (noopMarker) MarkOutcomeUnknown(string) error  { return nil }

type noopScheduler struct{}

func (noopScheduler) Acquire(context.Context, string) error { return nil }
func (noopScheduler) Release(string)                        {}

func NewActor(cfg ActorConfig) *Actor {
	size := cfg.MailboxSize
	if size <= 0 {
		size = defaultMailboxSize
	}
	if cfg.Marker == nil {
		cfg.Marker = noopMarker{}
	}
	if cfg.Scheduler == nil {
		cfg.Scheduler = noopScheduler{}
	}
	return &Actor{
		key:         cfg.Key,
		workspace:   cfg.Workspace,
		sessionHint: cfg.SessionHint,
		resolveHint: cfg.ResolveHint,
		runner:      cfg.Runner,
		marker:      cfg.Marker,
		sched:       cfg.Scheduler,
		deliver:     cfg.Deliver,
		entriesDone: cfg.EntriesDone,
		endTurn:     cfg.EndTurn,
		mailbox:     make(chan InboundMessage, size),
	}
}

func (a *Actor) start(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		return
	}
	a.ctx, a.cancelCtx = context.WithCancel(ctx)
	a.started = true
	a.wg.Add(1)
	go a.run()
}

func (a *Actor) stop() {
	a.mu.Lock()
	if !a.started {
		a.mu.Unlock()
		return
	}
	a.closed = true
	a.cancelCtx()
	a.mu.Unlock()
	a.wg.Wait()
}

func (a *Actor) run() {
	defer a.wg.Done()
	for {
		select {
		case <-a.ctx.Done():
			a.cancelQueued(nil)
			return
		case msg := <-a.mailbox:
			if a.ctx.Err() != nil {
				a.cancelQueued(&msg)
				return
			}
			a.process(a.ctx, msg)
		}
	}
}

func (a *Actor) Submit(msg InboundMessage) (Receipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return Receipt{}, ErrClosed
	}
	if len(a.mailbox) >= cap(a.mailbox) {
		return Receipt{}, ErrMailboxFull
	}
	pos := len(a.mailbox)
	a.mailbox <- msg
	return Receipt{ID: msg.ID, QueuePosition: pos}, nil
}

func (a *Actor) CancelCurrent() {
	a.mu.Lock()
	c := a.currentCancel
	a.mu.Unlock()
	if c != nil {
		c()
	}
}

func (a *Actor) Busy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.busy
}

func (a *Actor) process(base context.Context, msg InboundMessage) {
	a.setBusy(true)
	defer func() {
		a.setBusy(false)
		if a.endTurn != nil {
			a.endTurn()
		}
	}()
	turnCtx, cancel := context.WithCancel(base)
	a.setCurrentCancel(cancel)
	defer func() {
		a.setCurrentCancel(nil)
		cancel()
	}()
	if err := a.sched.Acquire(turnCtx, a.workspace); err != nil {
		a.marker.MarkCancelled(msg.ID)
		a.deliverTurn(msg, RunResult{}, ErrTurnCancelled)
		return
	}
	defer a.sched.Release(a.workspace)
	if err := a.marker.MarkStarted(msg.ID, msg.SessionID); err != nil {
		a.deliverTurn(msg, RunResult{}, err)
		return
	}
	work := WorkItem{Key: a.key, SessionPath: a.turnSessionHint(), Text: msg.Text, EntriesDone: a.entriesDone}
	result, runErr := a.runner.Run(turnCtx, work)
	if runErr != nil {
		if turnCtx.Err() != nil {
			a.marker.MarkOutcomeUnknown(msg.ID)
		} else {
			a.marker.MarkFailed(msg.ID, errorClass(runErr))
		}
		a.deliverTurn(msg, result, runErr)
		return
	}
	a.marker.MarkCompleted(msg.ID)
	if result.Summary != "" {
		summary := result
		summary.Text = result.Summary
		a.deliverKind(msg, summary, nil, DeliveryKindSummary)
	}
	a.deliverTurn(msg, result, nil)
}

func (a *Actor) cancelQueued(extra *InboundMessage) {
	if extra != nil {
		a.cancelMessage(*extra)
	}
	for {
		select {
		case msg := <-a.mailbox:
			a.cancelMessage(msg)
		default:
			return
		}
	}
}

func (a *Actor) cancelMessage(msg InboundMessage) {
	a.marker.MarkCancelled(msg.ID)
	if a.endTurn != nil {
		a.endTurn()
	}
	a.deliverTurn(msg, RunResult{}, ErrTurnCancelled)
}

func (a *Actor) turnSessionHint() string {
	if a.resolveHint != nil {
		if h := a.resolveHint(); h != "" {
			return h
		}
	}
	return a.sessionHint
}

func (a *Actor) deliverTurn(msg InboundMessage, result RunResult, err error) {
	a.deliverKind(msg, result, err, DeliveryKindResponse)
}

func (a *Actor) deliverKind(msg InboundMessage, result RunResult, err error, kind string) {
	if a.deliver == nil {
		return
	}
	a.deliver(Delivery{
		ID:              msg.ID,
		Transport:       msg.Transport,
		ExternalChatKey: msg.ExternalChatKey,
		UserIDHash:      msg.UserIDHash,
		Text:            msg.Text,
		Result:          result,
		Err:             err,
		Kind:            kind,
	})
}

func (a *Actor) setBusy(b bool) {
	a.mu.Lock()
	a.busy = b
	a.mu.Unlock()
}

func (a *Actor) setCurrentCancel(c context.CancelFunc) {
	a.mu.Lock()
	a.currentCancel = c
	a.mu.Unlock()
}
