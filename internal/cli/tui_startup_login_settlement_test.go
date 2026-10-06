package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/providers/oauth"
	"github.com/digitalygo/smidja/internal/ui"
)

func TestStartupFinishLoginPrefersSettledOutcome(t *testing.T) {
	lates := []struct {
		name string
		res  startupLoginResult
	}{
		{name: "provider error", res: startupLoginResult{err: context.Canceled}},
		{name: "credential", res: startupLoginResult{entry: authstore.Entry{Type: "oauth", Access: "late-access", Refresh: "late-refresh"}}},
	}
	for _, late := range lates {
		t.Run(late.name, func(t *testing.T) {
			startup, terminal, d := newLoginFixture(t)
			p := testLoginProvider(nil)
			op, err := startup.runner.StartLogin(context.Background(), ui.LoginRequest{Provider: "test", Title: "Sign in to test"})
			if err != nil {
				t.Fatalf("StartLogin: %v", err)
			}
			waitForTerminalOutput(t, terminal, "Sign in to test", 3*time.Second)
			op.Cancel()
			<-op.Done()
			if got := startup.finishLogin(context.Background(), d, p, op, late.res); !errors.Is(got, errStartupLoginCanceled) {
				t.Fatalf("finishLogin after cancel = %v, want errStartupLoginCanceled", got)
			}
			if _, ok := loginEntry(t, d); ok {
				t.Fatal("a canceled login must not persist a late result")
			}
		})
	}
}

func TestStartupFinishLoginPrefersSettledTimeout(t *testing.T) {
	startup, terminal, d := newLoginFixture(t)
	p := testLoginProvider(nil)
	op, err := startup.runner.StartLogin(context.Background(), ui.LoginRequest{Provider: "test", Title: "Sign in to test", Deadline: time.Now().Add(20 * time.Millisecond)})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	waitForTerminalOutput(t, terminal, "Sign in to test", 3*time.Second)
	select {
	case <-op.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("login deadline was not honored")
	}
	if got := startup.finishLogin(context.Background(), d, p, op, startupLoginResult{err: context.Canceled}); !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("finishLogin after timeout = %v, want context.DeadlineExceeded", got)
	}
	if _, ok := loginEntry(t, d); ok {
		t.Fatal("a timed out login must not persist a late result")
	}
}

func TestStartupFinishLoginSuccessPrecedesLateCancel(t *testing.T) {
	startup, terminal, d := newLoginFixture(t)
	p := testLoginProvider(nil)
	op, err := startup.runner.StartLogin(context.Background(), ui.LoginRequest{Provider: "test", Title: "Sign in to test"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	waitForTerminalOutput(t, terminal, "Sign in to test", 3*time.Second)
	entry := authstore.Entry{Type: "oauth", Access: "ACCESS-TOKEN", Refresh: "REFRESH-TOKEN"}
	if got := startup.finishLogin(context.Background(), d, p, op, startupLoginResult{entry: entry}); got != nil {
		t.Fatalf("finishLogin = %v, want nil", got)
	}
	op.Cancel()
	<-op.Done()
	if op.State() != ui.LoginSucceeded {
		t.Fatalf("late cancel changed state to %q, want %q", op.State(), ui.LoginSucceeded)
	}
	stored, ok := loginEntry(t, d)
	if !ok || stored.Access != "ACCESS-TOKEN" || stored.Refresh != "REFRESH-TOKEN" {
		t.Fatalf("stored entry = %+v, %v, want the committed credential", stored, ok)
	}
}

func TestStartupLoginCanceledOutcomeIsStable(t *testing.T) {
	for i := 0; i < 16; i++ {
		startup, terminal, d := newLoginFixture(t)
		p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
			<-ctx.Done()
			return authstore.Entry{}, ctx.Err()
		})
		done := make(chan error, 1)
		go func() { done <- startup.runLogin(context.Background(), d, p) }()
		waitForTerminalOutput(t, terminal, "Sign in to test", 3*time.Second)
		terminal.SendInput("\x03")
		select {
		case err := <-done:
			if !errors.Is(err, errStartupLoginCanceled) {
				t.Fatalf("iteration %d: canceled login error = %v, want errStartupLoginCanceled", i, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("iteration %d: runLogin did not return after the dialog was canceled", i)
		}
		if _, ok := loginEntry(t, d); ok {
			t.Fatalf("iteration %d: a canceled login persisted a credential", i)
		}
		startup.abort()
	}
}

func TestStartupLoginCanceledJoinsProviderWorker(t *testing.T) {
	startup, terminal, d := newLoginFixture(t)
	workerReturned := make(chan struct{})
	p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
		<-ctx.Done()
		close(workerReturned)
		return authstore.Entry{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() { done <- startup.runLogin(context.Background(), d, p) }()
	waitForTerminalOutput(t, terminal, "Sign in to test", 3*time.Second)
	terminal.SendInput("\x03")
	select {
	case err := <-done:
		if !errors.Is(err, errStartupLoginCanceled) {
			t.Fatalf("canceled login error = %v, want errStartupLoginCanceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runLogin did not return after the dialog was canceled")
	}
	select {
	case <-workerReturned:
	default:
		t.Fatal("runLogin returned before the provider worker was joined")
	}
}

func TestJoinLoginWorkerBoundsUncooperativeCallbacks(t *testing.T) {
	blocked := make(chan struct{})
	start := time.Now()
	if joinLoginWorker(blocked, 20*time.Millisecond) {
		t.Fatal("joinLoginWorker reported a joined worker for a blocked callback")
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("joinLoginWorker returned after %v, want the bounded grace", elapsed)
	}
	finished := make(chan struct{})
	close(finished)
	if !joinLoginWorker(finished, time.Second) {
		t.Fatal("joinLoginWorker did not report a completed worker")
	}
}
