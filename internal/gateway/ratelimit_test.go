package gateway

import (
	"errors"
	"testing"
	"time"
)

func TestRateLimiterBurstWindow(t *testing.T) {
	rl := NewRateLimiter(RateLimiterOptions{Burst: 10, RefillPerMinute: 10})
	now := time.Unix(1_700_000_000, 0)
	user := "u1"
	for i := 0; i < 10; i++ {
		if !rl.AllowUser(user, now) {
			t.Fatalf("allow %d should pass within burst", i)
		}
	}
	if rl.AllowUser(user, now) {
		t.Fatalf("allow beyond burst should be denied")
	}
	if !rl.AllowUser("other", now) {
		t.Fatalf("other user should have its own full bucket")
	}
}

func TestRateLimiterRefillOverTime(t *testing.T) {
	rl := NewRateLimiter(RateLimiterOptions{Burst: 1, RefillPerMinute: 60})
	now := time.Unix(1_700_000_000, 0)
	user := "u1"
	if !rl.AllowUser(user, now) {
		t.Fatalf("first allow should pass")
	}
	if rl.AllowUser(user, now) {
		t.Fatalf("second allow should be denied")
	}
	if !rl.AllowUser(user, now.Add(2*time.Second)) {
		t.Fatalf("allow after 2s at 1/sec should pass")
	}
}

func TestRateLimiterRefillCapsAtCapacity(t *testing.T) {
	rl := NewRateLimiter(RateLimiterOptions{Burst: 2, RefillPerMinute: 60})
	now := time.Unix(1_700_000_000, 0)
	user := "u1"
	if !rl.AllowUser(user, now) || !rl.AllowUser(user, now) {
		t.Fatalf("burst should pass")
	}
	if rl.AllowUser(user, now) {
		t.Fatalf("burst exhausted")
	}
	later := now.Add(10 * time.Minute)
	if !rl.AllowUser(user, later) || !rl.AllowUser(user, later) {
		t.Fatalf("refill must cap at capacity 2")
	}
	if rl.AllowUser(user, later) {
		t.Fatalf("capacity cap must not accumulate beyond burst")
	}
}

func TestRateLimiterPerUserBucketsIndependent(t *testing.T) {
	rl := NewRateLimiter(RateLimiterOptions{Burst: 2, RefillPerMinute: 1})
	now := time.Unix(1_700_000_000, 0)
	if !rl.AllowUser("a", now) || !rl.AllowUser("a", now) {
		t.Fatalf("user a burst")
	}
	if rl.AllowUser("a", now) {
		t.Fatalf("user a exhausted")
	}
	if !rl.AllowUser("b", now) || !rl.AllowUser("b", now) {
		t.Fatalf("user b must not share a bucket")
	}
}

func TestRateLimiterMaxActiveTurns(t *testing.T) {
	rl := NewRateLimiter(RateLimiterOptions{Burst: 10, RefillPerMinute: 10, MaxActive: 2})
	if !rl.TryBeginTurn() || !rl.TryBeginTurn() {
		t.Fatalf("two turns should begin")
	}
	if rl.TryBeginTurn() {
		t.Fatalf("third turn should be rejected")
	}
	if rl.ActiveTurns() != 2 {
		t.Fatalf("active = %d, want 2", rl.ActiveTurns())
	}
	rl.EndTurn()
	if !rl.TryBeginTurn() {
		t.Fatalf("turn after end should begin")
	}
	rl.EndTurn()
	rl.EndTurn()
	if rl.ActiveTurns() != 0 {
		t.Fatalf("active = %d, want 0", rl.ActiveTurns())
	}
}

func TestRateLimiterUnlimitedMaxActive(t *testing.T) {
	rl := NewRateLimiter(RateLimiterOptions{Burst: 10, RefillPerMinute: 10, MaxActive: 0})
	for i := 0; i < 50; i++ {
		if !rl.TryBeginTurn() {
			t.Fatalf("unlimited active rejected turn %d", i)
		}
	}
	if rl.ActiveTurns() != 50 {
		t.Fatalf("active = %d, want 50", rl.ActiveTurns())
	}
}

func TestRateLimiterCheckSize(t *testing.T) {
	rl := NewRateLimiter(RateLimiterOptions{})
	if err := rl.CheckSize("small", 100); err != nil {
		t.Fatalf("small text should pass: %v", err)
	}
	err := rl.CheckSize("this is a long text", 5)
	if !errors.Is(err, ErrInboundTooLarge) {
		t.Fatalf("expected ErrInboundTooLarge, got %v", err)
	}
	if err := rl.CheckSize("ignored", 0); err != nil {
		t.Fatalf("zero cap should disable check: %v", err)
	}
}
