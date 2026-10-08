package tui

import (
	"sync"
	"testing"
	"time"
)

type runtimeTask struct {
	id       int
	deadline time.Time
	fn       func()
}

type runtimeTimer struct {
	clock *runtimeClock
	id    int
}

func (t *runtimeTimer) Stop() bool { return t.clock.stop(t.id) }

type runtimeClock struct {
	mu     sync.Mutex
	now    time.Time
	nextID int
	tasks  map[int]*runtimeTask
}

func newRuntimeClock() *runtimeClock {
	return &runtimeClock{now: time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC), tasks: map[int]*runtimeTask{}}
}

func (c *runtimeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *runtimeClock) AfterFunc(d time.Duration, fn func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	task := &runtimeTask{id: c.nextID, deadline: c.now.Add(d), fn: fn}
	c.tasks[task.id] = task
	return &runtimeTimer{clock: c, id: task.id}
}

func (c *runtimeClock) stop(id int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.tasks[id]; !ok {
		return false
	}
	delete(c.tasks, id)
	return true
}

func (c *runtimeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	for {
		var chosen *runtimeTask
		for _, task := range c.tasks {
			if task.deadline.After(c.now) {
				continue
			}
			if chosen == nil || task.deadline.Before(chosen.deadline) || (task.deadline.Equal(chosen.deadline) && task.id < chosen.id) {
				chosen = task
			}
		}
		if chosen == nil {
			break
		}
		delete(c.tasks, chosen.id)
		c.mu.Unlock()
		chosen.fn()
		c.mu.Lock()
	}
	c.mu.Unlock()
}

func (c *runtimeClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tasks)
}

func TestRuntimeRunSerializesSections(t *testing.T) {
	runtime := NewRuntime(nil)
	order := make([]int, 0, 4)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			runtime.Run(func() {
				mu.Lock()
				order = append(order, id)
				mu.Unlock()
			})
		}(i)
	}
	wg.Wait()
	if len(order) != 4 {
		t.Fatalf("expected 4 sections, got %d", len(order))
	}
}

func TestRuntimeAfterFiresAndCancels(t *testing.T) {
	clock := newRuntimeClock()
	runtime := NewRuntime(clock)
	fired := 0
	runtime.After(10*time.Millisecond, func() { fired++ })
	if clock.Pending() != 1 {
		t.Fatalf("expected 1 pending timer, got %d", clock.Pending())
	}
	clock.Advance(10 * time.Millisecond)
	if fired != 1 {
		t.Fatalf("expected timer to fire once, got %d", fired)
	}
	cancel := runtime.After(10*time.Millisecond, func() { fired++ })
	clock.Advance(5 * time.Millisecond)
	if !cancel() {
		t.Fatal("expected cancel to stop a pending timer")
	}
	if cancel() {
		t.Fatal("expected second cancel to report nothing to stop")
	}
	clock.Advance(10 * time.Millisecond)
	if fired != 1 {
		t.Fatalf("expected cancelled timer not to fire, got %d", fired)
	}
}

func TestRuntimeEveryRepeatsUntilCancelled(t *testing.T) {
	clock := newRuntimeClock()
	runtime := NewRuntime(clock)
	fired := 0
	cancel := runtime.Every(10*time.Millisecond, func() { fired++ })
	clock.Advance(10 * time.Millisecond)
	if fired != 1 {
		t.Fatalf("expected first tick, got %d", fired)
	}
	clock.Advance(20 * time.Millisecond)
	if fired != 2 {
		t.Fatalf("expected two ticks, got %d", fired)
	}
	cancel()
	clock.Advance(50 * time.Millisecond)
	if fired != 2 {
		t.Fatalf("expected cancelled animation to stop, got %d", fired)
	}
	if clock.Pending() != 0 {
		t.Fatalf("expected no pending timers after cancel, got %d", clock.Pending())
	}
}

func TestRuntimeStopCancelsTimers(t *testing.T) {
	clock := newRuntimeClock()
	runtime := NewRuntime(clock)
	fired := 0
	runtime.After(10*time.Millisecond, func() { fired++ })
	runtime.Every(10*time.Millisecond, func() { fired++ })
	runtime.Stop()
	if !runtime.Stopped() {
		t.Fatal("expected runtime to report stopped")
	}
	if clock.Pending() != 0 {
		t.Fatalf("expected stop to cancel timers, got %d pending", clock.Pending())
	}
	clock.Advance(time.Second)
	if fired != 0 {
		t.Fatalf("expected no callbacks after stop, got %d", fired)
	}
	late := runtime.After(time.Millisecond, func() { fired++ })
	if late() {
		t.Fatal("expected scheduling after stop to return a no-op cancel")
	}
	clock.Advance(time.Second)
	if fired != 0 {
		t.Fatalf("expected no callbacks after stop rescheduling, got %d", fired)
	}
}

func TestSystemClockTimer(t *testing.T) {
	clock := SystemClock{}
	if clock.Now().IsZero() {
		t.Fatal("system clock returned zero time")
	}
	done := make(chan struct{})
	timer := clock.AfterFunc(time.Millisecond, func() { close(done) })
	defer timer.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("system clock timer did not fire")
	}
}

func TestRuntimeEveryCancelBeforeFirstTick(t *testing.T) {
	clock := newRuntimeClock()
	runtime := NewRuntime(clock)
	fired := 0
	cancel := runtime.Every(10*time.Millisecond, func() { fired++ })
	if !cancel() {
		t.Fatal("expected cancel to stop the animation")
	}
	if cancel() {
		t.Fatal("expected second cancel to report nothing to stop")
	}
	clock.Advance(time.Second)
	if fired != 0 {
		t.Fatalf("cancelled animation fired %d times", fired)
	}
}
