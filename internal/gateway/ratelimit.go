package gateway

import (
	"fmt"
	"math"
	"sync"
	"time"
)

type RateLimiterOptions struct {
	Burst           int
	RefillPerMinute float64
	MaxActive       int
}

type RateLimiter struct {
	mu           sync.Mutex
	capacity     float64
	refillPerSec float64
	buckets      map[string]*bucket
	active       int
	maxActive    int
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewRateLimiter(opts RateLimiterOptions) *RateLimiter {
	burst := opts.Burst
	if burst <= 0 {
		burst = 10
	}
	refill := opts.RefillPerMinute
	if refill <= 0 {
		refill = 10
	}
	return &RateLimiter{
		capacity:     float64(burst),
		refillPerSec: refill / 60,
		buckets:      make(map[string]*bucket),
		maxActive:    opts.MaxActive,
	}
}

func (rl *RateLimiter) AllowUser(user string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[user]
	if !ok {
		b = &bucket{tokens: rl.capacity, last: now}
		rl.buckets[user] = b
	}
	if now.After(b.last) {
		elapsed := now.Sub(b.last).Seconds()
		b.tokens = math.Min(rl.capacity, b.tokens+elapsed*rl.refillPerSec)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

func (rl *RateLimiter) TryBeginTurn() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.maxActive > 0 && rl.active >= rl.maxActive {
		return false
	}
	rl.active++
	return true
}

func (rl *RateLimiter) EndTurn() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.active > 0 {
		rl.active--
	}
}

func (rl *RateLimiter) ActiveTurns() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.active
}

func (rl *RateLimiter) CheckSize(text string, maxBytes int) error {
	if maxBytes > 0 && len(text) > maxBytes {
		return fmt.Errorf("%w: %d bytes over cap %d", ErrInboundTooLarge, len(text), maxBytes)
	}
	return nil
}
