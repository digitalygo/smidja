package extensions

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/sdk"
)

var (
	ErrCustomEventName     = errors.New("extensions: custom event requires a non-empty name")
	ErrCustomEventHandler  = errors.New("extensions: custom event requires a handler")
	ErrCustomEventClosed   = errors.New("extensions: the custom event bus is closed")
	ErrCustomEventOverflow = errors.New("extensions: too many nested custom events")
)

const customEventQueueLimit = 1024

type customSubscription struct {
	id      uint64
	handler sdk.CustomEventHandler
}

type pendingCustomEvent struct {
	name string
	data any
}

type CustomEventBus struct {
	mu        sync.Mutex
	subs      map[string][]customSubscription
	queue     []pendingCustomEvent
	draining  bool
	drainDone chan struct{}
	nested    int
	closed    bool
	sequence  uint64
}

var _ sdk.CustomEventSubscription = (*CustomEventBus)(nil)

func NewCustomEventBus() *CustomEventBus {
	return &CustomEventBus{subs: make(map[string][]customSubscription)}
}

func (b *CustomEventBus) SubscribeCustomEvent(name string, handler sdk.CustomEventHandler) (func(), error) {
	return b.Subscribe(name, handler)
}

func (b *CustomEventBus) Subscribe(name string, handler sdk.CustomEventHandler) (func(), error) {
	if b == nil {
		return nil, ErrUnavailable
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrCustomEventName
	}
	if handler == nil {
		return nil, ErrCustomEventHandler
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrCustomEventClosed
	}
	b.sequence++
	id := b.sequence
	b.subs[name] = append(b.subs[name], customSubscription{id: id, handler: handler})
	b.mu.Unlock()
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.remove(name, id)
		})
	}
	return unsubscribe, nil
}

func (b *CustomEventBus) remove(name string, id uint64) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.subs[name]
	for i, sub := range remaining {
		if sub.id != id {
			continue
		}
		b.subs[name] = append(remaining[:i:i], remaining[i+1:]...)
		if len(b.subs[name]) == 0 {
			delete(b.subs, name)
		}
		return
	}
}

func (b *CustomEventBus) Emit(name string, data any) error {
	if b == nil {
		return ErrUnavailable
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrCustomEventName
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrCustomEventClosed
	}
	if b.draining {
		if b.nested >= customEventQueueLimit {
			b.mu.Unlock()
			return ErrCustomEventOverflow
		}
		b.nested++
	}
	b.queue = append(b.queue, pendingCustomEvent{name: name, data: data})
	if b.draining {
		b.mu.Unlock()
		return nil
	}
	b.draining = true
	b.nested = 0
	b.drainDone = make(chan struct{})
	b.mu.Unlock()
	return b.drain()
}

func (b *CustomEventBus) drain() error {
	var errs []error
	for {
		b.mu.Lock()
		if b.closed || len(b.queue) == 0 {
			b.queue = nil
			b.draining = false
			b.nested = 0
			done := b.drainDone
			b.mu.Unlock()
			if done != nil {
				close(done)
			}
			break
		}
		event := b.queue[0]
		b.queue = b.queue[1:]
		subs := append([]customSubscription(nil), b.subs[event.name]...)
		b.mu.Unlock()
		for _, sub := range subs {
			b.mu.Lock()
			closed := b.closed
			b.mu.Unlock()
			if closed {
				break
			}
			if err := guardCustomEventHandler(sub.handler, event); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func guardCustomEventHandler(handler sdk.CustomEventHandler, event pendingCustomEvent) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("extensions: custom event %q handler panic: %v", event.name, recovered)
		}
	}()
	return handler(sdk.CustomEvent{Name: event.name, Data: event.data})
}

func (b *CustomEventBus) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.closed = true
	b.queue = nil
	b.subs = make(map[string][]customSubscription)
	b.mu.Unlock()
}

func (b *CustomEventBus) Wait(timeout time.Duration) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	done := b.drainDone
	b.mu.Unlock()
	if done == nil {
		return true
	}
	if timeout <= 0 {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (b *CustomEventBus) Closed() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

type customEventSnapshot struct {
	subs   map[string][]customSubscription
	closed bool
}

func (b *CustomEventBus) snapshot() customEventSnapshot {
	if b == nil {
		return customEventSnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	snap := customEventSnapshot{subs: make(map[string][]customSubscription, len(b.subs)), closed: b.closed}
	for name, subs := range b.subs {
		snap.subs[name] = append([]customSubscription(nil), subs...)
	}
	return snap
}

func (b *CustomEventBus) restore(snap customEventSnapshot) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		b.subs = make(map[string][]customSubscription)
		return
	}
	b.subs = snap.subs
	b.closed = snap.closed
}
