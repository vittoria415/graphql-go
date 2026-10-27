package graphql_go

import (
	"sync"
	"time"
)

// Clock returns the current time. Production callers pass time.Now; tests pass
// a fake so assertions never depend on wall-clock behaviour. Keeping this as a
// tiny interface rather than accepting a function directly lets a single fake
// satisfy every Limiter in a test without allocations.
type Clock interface {
	Now() time.Time
}

// realClock is the default Clock, wrapping time.Now so callers don't have to.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Limiter is a token bucket. Tokens accrue at rate per second up to a ceiling
// of burst. SpendBorrow consumes n tokens; if fewer than n are available it
// either waits (block=true) or returns false (block=false). WaitDuration is
// the non-mutating query form: it reports how long SpendBorrow(block=true)
// would block for n tokens, or false if it would refuse (n > burst).
//
// The bucket is safe for concurrent use; Allow / SpendBorrow serialise on a
// single mutex which is held only for arithmetic, never across a Sleep, so a
// slow caller cannot starve others.
type Limiter struct {
	mu         sync.Mutex
	rate       float64
	burst      float64
	tokens     float64
	last       time.Time
	clock      Clock
	timeNowSec func() float64
}

// New returns a Limiter with the given steady-state rate (tokens per second)
// and burst (maximum tokens that may accumulate). The bucket starts full so
// a freshly-constructed limiter does not throttle its very first request —
// the common expectation for a "burst then settle" policy.
func New(rate, burst float64) *Limiter {
	return NewWithClock(rate, burst, realClock{})
}

// NewWithClock is New with a custom Clock, for deterministic tests.
func NewWithClock(rate, burst float64, clock Clock) *Limiter {
	return &Limiter{
		rate:       rate,
		burst:      burst,
		tokens:     burst,
		last:       clock.Now(),
		clock:      clock,
		timeNowSec: func() float64 { return float64(clock.Now().UnixNano()) / 1e9 },
	}
}

// refill adds tokens accrued since the last call. Caller holds mu.
func (l *Limiter) refill(now time.Time) {
	if now.Before(l.last) {
		// Clock went backwards (NTP slew, fake clock reset). Treat the past as
		// the present to avoid handing out tokens for a negative interval, which
		// would silently raise throughput above rate.
		l.last = now
		return
	}
	elapsed := now.Sub(l.last).Seconds()
	if elapsed <= 0 {
		return
	}
	l.tokens += elapsed * l.rate
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.last = now
}

// Allow reports whether one token is available right now, consuming it if so.
func (l *Limiter) Allow() bool {
	return l.SpendBorrow(1, false)
}

// Wait blocks until one token is available and consumes it.
func (l *Limiter) Wait() {
	l.SpendBorrow(1, true)
}

// SpendBorrow consumes n tokens. When block is true it sleeps until enough
// tokens have accrued (no busy-wait); when false it returns false immediately
// if the bucket is short. n must be positive; a non-positive n is treated as
// zero cost and always succeeds — this mirrors the convention used by Go's
// own rate package and keeps callers from having to special-case "0 items".
func (l *Limiter) SpendBorrow(n float64, block bool) bool {
	if n <= 0 {
		return true
	}
	for {
		l.mu.Lock()
		l.refill(l.clock.Now())
		if l.tokens >= n {
			l.tokens -= n
			l.mu.Unlock()
			return true
		}
		if !block {
			l.mu.Unlock()
			return false
		}
		// Wait for exactly the deficit, then re-check. A long sleep would be rude
		// to concurrent callers whose requests might fit into partial accrual, so
		// we sleep per-deficit and loop rather than sleeping for the whole gap.
		deficit := n - l.tokens
		wait := time.Duration(deficit / l.rate * float64(time.Second))
		l.mu.Unlock()
		if wait <= 0 {
			wait = time.Millisecond
		}
		time.Sleep(wait)
	}
}

// WaitDuration reports how long SpendBorrow(n, true) would block, or
// (0, false) if n exceeds burst and therefore can never succeed. It does not
// mutate the bucket — handy for metrics and for deciding whether to even try.
func (l *Limiter) WaitDuration(n float64) (time.Duration, bool) {
	if n <= 0 {
		return 0, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refill(l.clock.Now())
	if n > l.burst {
		return 0, false
	}
	if l.tokens >= n {
		return 0, true
	}
	deficit := n - l.tokens
	return time.Duration(deficit / l.rate * float64(time.Second)), true
}

// Rate returns the configured steady-state rate.
func (l *Limiter) Rate() float64 { return l.rate }

// Burst returns the configured burst ceiling.
func (l *Limiter) Burst() float64 { return l.burst }

// Tokens returns the current token count after refilling against the clock.
// Mainly for inspection; do not use it to make admission decisions — use Allow
// or SpendBorrow, which atomically consume.
func (l *Limiter) Tokens() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refill(l.clock.Now())
	return l.tokens
}
