package gateway

import (
	"context"
	"errors"
	"testing"
)

func TestRouterCreatesAndResumesActor(t *testing.T) {
	created := make(chan string, 4)
	factory := func(key, workspace, sessionHint string) *Actor {
		created <- key + "|" + workspace + "|" + sessionHint
		return NewActor(ActorConfig{Key: key, Workspace: workspace, SessionHint: sessionHint})
	}
	r := NewRouter(func(key string) (string, string) {
		return "ws:" + key, "hint:" + key
	}, factory)
	r.Start(context.Background())
	a1, err := r.Actor("telegram:123")
	if err != nil {
		t.Fatalf("actor: %v", err)
	}
	a2, err := r.Actor("telegram:123")
	if err != nil {
		t.Fatalf("actor resume: %v", err)
	}
	if a1 != a2 {
		t.Fatalf("resume returned a different actor")
	}
	if got := <-created; got != "telegram:123|ws:telegram:123|hint:telegram:123" {
		t.Fatalf("factory args = %q", got)
	}
	select {
	case extra := <-created:
		t.Fatalf("factory called twice: %q", extra)
	default:
	}
	r.Shutdown()
}

func TestRouterMultipleKeys(t *testing.T) {
	factory := func(key, workspace, sessionHint string) *Actor {
		return NewActor(ActorConfig{Key: key, Workspace: workspace})
	}
	r := NewRouter(fixedResolver("ws", "hint"), factory)
	r.Start(context.Background())
	for _, key := range []string{"a:1", "b:2", "c:3"} {
		if _, err := r.Actor(key); err != nil {
			t.Fatalf("actor %s: %v", key, err)
		}
	}
	keys := r.Keys()
	if len(keys) != 3 {
		t.Fatalf("keys = %v", keys)
	}
	if r.Size() != 3 {
		t.Fatalf("size = %d, want 3", r.Size())
	}
	r.Shutdown()
}

func TestRouterActorBeforeStart(t *testing.T) {
	factory := func(key, workspace, sessionHint string) *Actor {
		return NewActor(ActorConfig{Key: key})
	}
	r := NewRouter(fixedResolver("ws", "hint"), factory)
	if _, err := r.Actor("k"); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("expected ErrNotStarted, got %v", err)
	}
}

func TestRouterShutdownStopsAllActors(t *testing.T) {
	started := make(chan *Actor, 4)
	factory := func(key, workspace, sessionHint string) *Actor {
		a := NewActor(ActorConfig{Key: key, Workspace: workspace, Runner: instantRunner{}})
		return a
	}
	r := NewRouter(fixedResolver("ws", "hint"), factory)
	r.Start(context.Background())
	for _, key := range []string{"a", "b", "c"} {
		a, err := r.Actor(key)
		if err != nil {
			t.Fatalf("actor: %v", err)
		}
		started <- a
	}
	close(started)
	r.Shutdown()
	for a := range started {
		if _, err := a.Submit(sampleMessage("x")); !errors.Is(err, ErrClosed) {
			t.Fatalf("actor should be closed after router shutdown, got %v", err)
		}
	}
}

func TestRouterNilResolverDefaults(t *testing.T) {
	r := NewRouter(nil, func(key, workspace, sessionHint string) *Actor {
		return NewActor(ActorConfig{Key: key, Workspace: workspace, SessionHint: sessionHint})
	})
	r.Start(context.Background())
	a, err := r.Actor("k")
	if err != nil {
		t.Fatalf("actor: %v", err)
	}
	if a.workspace != "" || a.sessionHint != "" {
		t.Fatalf("default resolver should yield empty workspace/hint, got %q %q", a.workspace, a.sessionHint)
	}
	r.Shutdown()
}
