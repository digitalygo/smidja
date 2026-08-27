package gateway

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSchedulerGlobalLimitSerializesDifferentWorkspaces(t *testing.T) {
	s := NewScheduler(1)
	start := make(chan struct{})
	var mu sync.Mutex
	var active, maxSeen int
	var wg sync.WaitGroup
	run := func(ws string) {
		defer wg.Done()
		<-start
		if err := s.Acquire(context.Background(), ws); err != nil {
			t.Errorf("acquire %s: %v", ws, err)
			return
		}
		defer s.Release(ws)
		mu.Lock()
		active++
		if active > maxSeen {
			maxSeen = active
		}
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
	}
	wg.Add(2)
	go run("ws1")
	go run("ws2")
	close(start)
	wg.Wait()
	if maxSeen != 1 {
		t.Fatalf("max concurrent = %d, want 1", maxSeen)
	}
}

func TestSchedulerWorkspaceExclusionSerializesSameWorkspace(t *testing.T) {
	s := NewScheduler(4)
	start := make(chan struct{})
	var mu sync.Mutex
	var active, maxSeen int
	var wg sync.WaitGroup
	run := func(ws string) {
		defer wg.Done()
		<-start
		if err := s.Acquire(context.Background(), ws); err != nil {
			t.Errorf("acquire: %v", err)
			return
		}
		defer s.Release(ws)
		mu.Lock()
		active++
		if active > maxSeen {
			maxSeen = active
		}
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
	}
	wg.Add(3)
	go run("same")
	go run("same")
	go run("same")
	close(start)
	wg.Wait()
	if maxSeen != 1 {
		t.Fatalf("max concurrent in workspace = %d, want 1", maxSeen)
	}
}

func TestSchedulerFairnessFIFOOrder(t *testing.T) {
	s := NewScheduler(4)
	if err := s.Acquire(context.Background(), "ws"); err != nil {
		t.Fatalf("owner acquire: %v", err)
	}
	acquired := make(chan string, 3)
	var wg sync.WaitGroup
	for _, name := range []string{"first", "second", "third"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if err := s.Acquire(context.Background(), "ws"); err != nil {
				t.Errorf("acquire %s: %v", name, err)
				return
			}
			acquired <- name
			s.Release("ws")
		}(name)
		time.Sleep(20 * time.Millisecond)
	}
	s.Release("ws")
	wg.Wait()
	var order []string
	for i := 0; i < 3; i++ {
		order = append(order, <-acquired)
	}
	want := []string{"first", "second", "third"}
	for i, w := range want {
		if order[i] != w {
			t.Fatalf("acquire order = %v, want %v", order, want)
		}
	}
}

func TestSchedulerParallelAcquisitionTiming(t *testing.T) {
	s := NewScheduler(4)
	begin := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s.Acquire(context.Background(), "ws1")
		time.Sleep(40 * time.Millisecond)
		s.Release("ws1")
	}()
	go func() {
		defer wg.Done()
		s.Acquire(context.Background(), "ws2")
		time.Sleep(40 * time.Millisecond)
		s.Release("ws2")
	}()
	wg.Wait()
	elapsed := time.Since(begin)
	if elapsed >= 70*time.Millisecond {
		t.Fatalf("parallel workspaces took %v, want overlap", elapsed)
	}
}

func TestSchedulerSerializedAcquisitionTiming(t *testing.T) {
	s := NewScheduler(4)
	begin := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)
	for _, ws := range []string{"ws1", "ws1"} {
		go func(ws string) {
			defer wg.Done()
			s.Acquire(context.Background(), ws)
			time.Sleep(40 * time.Millisecond)
			s.Release(ws)
		}(ws)
	}
	wg.Wait()
	elapsed := time.Since(begin)
	if elapsed < 70*time.Millisecond {
		t.Fatalf("same workspace took %v, want serialized", elapsed)
	}
}

func TestSchedulerAcquireContextCancellation(t *testing.T) {
	s := NewScheduler(4)
	if err := s.Acquire(context.Background(), "ws"); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.Acquire(ctx, "ws")
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected cancellation error")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("acquire did not return after cancel")
	}
	s.Release("ws")
	if err := s.Acquire(context.Background(), "ws"); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	s.Release("ws")
	if s.Active() != 0 {
		t.Fatalf("active = %d, want 0", s.Active())
	}
}

func TestSchedulerGateRecoversAfterCancelledWaiter(t *testing.T) {
	s := NewScheduler(4)
	if err := s.Acquire(context.Background(), "ws"); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.Acquire(ctx, "ws")
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	s.Release("ws")
	err := s.Acquire(context.Background(), "ws")
	if err != nil {
		t.Fatalf("acquire after cancelled waiter: %v", err)
	}
	s.Release("ws")
	if s.Active() != 0 {
		t.Fatalf("active = %d, want 0", s.Active())
	}
}

func TestSchedulerGateReuseAcrossCancellations(t *testing.T) {
	s := NewScheduler(2)
	for i := 0; i < 5; i++ {
		if err := s.Acquire(context.Background(), "ws"); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- s.Acquire(ctx, "ws")
		}()
		time.Sleep(5 * time.Millisecond)
		cancel()
		<-done
		s.Release("ws")
	}
	if s.Active() != 0 {
		t.Fatalf("active = %d, want 0", s.Active())
	}
}

func TestSchedulerActiveCounts(t *testing.T) {
	s := NewScheduler(2)
	if s.MaxActive() != 2 {
		t.Fatalf("max active = %d, want 2", s.MaxActive())
	}
	s.Acquire(context.Background(), "a")
	s.Acquire(context.Background(), "b")
	if s.Active() != 2 {
		t.Fatalf("active = %d, want 2", s.Active())
	}
	s.Release("a")
	if s.Active() != 1 {
		t.Fatalf("active = %d, want 1", s.Active())
	}
	s.Release("b")
	if s.Active() != 0 {
		t.Fatalf("active = %d, want 0", s.Active())
	}
}
