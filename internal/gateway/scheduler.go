package gateway

import (
	"context"
	"sync"
)

type Scheduler struct {
	maxActive int
	sem       chan struct{}
	mu        sync.Mutex
	gates     map[string]*WorkspaceGate
}

func NewScheduler(maxActive int) *Scheduler {
	if maxActive < 1 {
		maxActive = 1
	}
	return &Scheduler{
		maxActive: maxActive,
		sem:       make(chan struct{}, maxActive),
		gates:     make(map[string]*WorkspaceGate),
	}
}

func (s *Scheduler) Acquire(ctx context.Context, workspace string) error {
	gate := s.gateFor(workspace)
	if err := gate.acquire(ctx); err != nil {
		return err
	}
	select {
	case s.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		gate.release()
		return ctx.Err()
	}
}

func (s *Scheduler) Release(workspace string) {
	<-s.sem
	s.gateFor(workspace).release()
}

func (s *Scheduler) Active() int {
	return len(s.sem)
}

func (s *Scheduler) MaxActive() int {
	return s.maxActive
}

func (s *Scheduler) gateFor(workspace string) *WorkspaceGate {
	s.mu.Lock()
	defer s.mu.Unlock()
	gate, ok := s.gates[workspace]
	if !ok {
		gate = &WorkspaceGate{}
		s.gates[workspace] = gate
	}
	return gate
}

type WorkspaceGate struct {
	mu   sync.Mutex
	busy bool
	wait []chan struct{}
}

func (g *WorkspaceGate) acquire(ctx context.Context) error {
	g.mu.Lock()
	if !g.busy {
		g.busy = true
		g.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	g.wait = append(g.wait, ch)
	g.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		select {
		case <-ch:
			g.release()
			return ctx.Err()
		default:
			g.remove(ch)
			return ctx.Err()
		}
	}
}

func (g *WorkspaceGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.wait) > 0 {
		close(g.wait[0])
		g.wait = g.wait[1:]
		return
	}
	g.busy = false
}

func (g *WorkspaceGate) remove(ch chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, c := range g.wait {
		if c == ch {
			g.wait = append(g.wait[:i], g.wait[i+1:]...)
			return
		}
	}
}
