package ui

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLoginCommitPersistsBeforeSuccess(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	persisted := false
	if err := op.Commit(func() error {
		persisted = true
		return nil
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !persisted {
		t.Fatal("commit callback was not invoked")
	}
	result := op.Wait()
	if result.State != LoginSucceeded || result.Err != nil {
		t.Fatalf("result = %+v, want succeeded", result)
	}
	op.Cancel()
	if op.State() != LoginSucceeded {
		t.Fatalf("state = %q after late cancel, want %q", op.State(), LoginSucceeded)
	}
}

func TestLoginCommitSkipsPersistAfterSettle(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	op.Cancel()
	op.Wait()
	persisted := false
	if err := op.Commit(func() error {
		persisted = true
		return nil
	}); !errors.Is(err, ErrLoginSettled) {
		t.Fatalf("Commit after settle = %v, want ErrLoginSettled", err)
	}
	if persisted {
		t.Fatal("commit callback ran after the operation had settled")
	}
	if op.State() != LoginCanceled {
		t.Fatalf("state = %q, want %q", op.State(), LoginCanceled)
	}
}

func TestLoginCommitFailureSettlesFailed(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	boom := errors.New("ui: persist failed")
	if err := op.Commit(func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("Commit = %v, want %v", err, boom)
	}
	result := op.Wait()
	if result.State != LoginFailed || !errors.Is(result.Err, boom) {
		t.Fatalf("result = %+v, want failed with %v", result, boom)
	}
}

func TestLoginCommitSerializesWithCancel(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	commitErr := make(chan error, 1)
	go func() {
		commitErr <- op.Commit(func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	canceled := make(chan struct{})
	go func() {
		op.Cancel()
		close(canceled)
	}()
	select {
	case <-canceled:
		t.Fatal("cancel settled while the credential commit owned settlement")
	case <-time.After(50 * time.Millisecond):
	}
	if op.State() != LoginStarting {
		t.Fatalf("state = %q during commit, want %q", op.State(), LoginStarting)
	}
	close(release)
	if err := <-commitErr; err != nil {
		t.Fatalf("Commit: %v", err)
	}
	<-canceled
	result := op.Wait()
	if result.State != LoginSucceeded || result.Err != nil {
		t.Fatalf("result = %+v, want succeeded", result)
	}
}
