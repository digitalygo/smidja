package tui

import (
	"sync"
	"time"
)

type Timer interface {
	Stop() bool
}

type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, fn func()) Timer
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) AfterFunc(d time.Duration, fn func()) Timer {
	return time.AfterFunc(d, fn)
}

type Runtime struct {
	mu    sync.Mutex
	clock Clock

	timerMu sync.Mutex
	stopped bool
	timers  map[int]Timer
	nextID  int
}

func NewRuntime(clock Clock) *Runtime {
	if clock == nil {
		clock = SystemClock{}
	}
	return &Runtime{clock: clock, timers: map[int]Timer{}}
}

func (r *Runtime) Now() time.Time { return r.clock.Now() }

func (r *Runtime) Run(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn()
}

func (r *Runtime) Stopped() bool {
	r.timerMu.Lock()
	defer r.timerMu.Unlock()
	return r.stopped
}

func (r *Runtime) After(d time.Duration, fn func()) func() bool {
	r.timerMu.Lock()
	if r.stopped {
		r.timerMu.Unlock()
		return func() bool { return false }
	}
	r.nextID++
	id := r.nextID
	timer := r.clock.AfterFunc(d, func() {
		r.timerMu.Lock()
		if r.stopped {
			r.timerMu.Unlock()
			return
		}
		delete(r.timers, id)
		r.timerMu.Unlock()
		fn()
	})
	r.timers[id] = timer
	r.timerMu.Unlock()
	return func() bool { return r.cancel(id) }
}

func (r *Runtime) cancel(id int) bool {
	r.timerMu.Lock()
	timer, ok := r.timers[id]
	delete(r.timers, id)
	r.timerMu.Unlock()
	if !ok {
		return false
	}
	return timer.Stop()
}

func (r *Runtime) Every(d time.Duration, fn func()) func() bool {
	var (
		mu      sync.Mutex
		cancel  func() bool
		stopped bool
	)
	var schedule func()
	schedule = func() {
		next := r.After(d, func() {
			mu.Lock()
			halted := stopped
			mu.Unlock()
			if halted {
				return
			}
			fn()
			schedule()
		})
		mu.Lock()
		if stopped {
			mu.Unlock()
			next()
			return
		}
		cancel = next
		mu.Unlock()
	}
	schedule()
	return func() bool {
		mu.Lock()
		if stopped {
			mu.Unlock()
			return false
		}
		stopped = true
		current := cancel
		mu.Unlock()
		if current == nil {
			return false
		}
		return current()
	}
}

func (r *Runtime) Stop() {
	r.timerMu.Lock()
	r.stopped = true
	timers := r.timers
	r.timers = map[int]Timer{}
	r.timerMu.Unlock()
	for _, timer := range timers {
		timer.Stop()
	}
}
