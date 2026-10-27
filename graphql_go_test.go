package graphql_go

import (
	"math"
	"testing"
	"time"
)

// fakeClock is a Clock whose time advances only when Advance is called. Tests
// never touch the real clock, so they are deterministic regardless of host load.
type fakeClock struct {
	t time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1_000_000, 0).UTC()}
}

func (f *fakeClock) Now() time.Time { return f.t }

func (f *fakeClock) Advance(d time.Duration) { f.t = f.t.Add(d) }

const floatTolerance = 1e-9

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < floatTolerance
}

func TestNewStartsFull(t *testing.T) {
	lim := NewWithClock(10, 5, newFakeClock())
	got := lim.Tokens()
	if !approxEqual(got, 5) {
		t.Fatalf("expected bucket to start full at burst=5, got %f", got)
	}
}

func TestAllowDrainsOne(t *testing.T) {
	lim := NewWithClock(10, 5, newFakeClock())
	if !lim.Allow() {
		t.Fatal("expected first Allow to succeed against a full bucket")
	}
	if got := lim.Tokens(); !approxEqual(got, 4) {
		t.Fatalf("expected 4 tokens after one Allow, got %f", got)
	}
}

func TestNonBlockingRefusesWhenEmpty(t *testing.T) {
	clock := newFakeClock()
	lim := NewWithClock(1, 2, clock)
	if !lim.SpendBorrow(2, false) {
		t.Fatal("spending the whole burst should succeed")
	}
	if lim.SpendBorrow(1, false) {
		t.Fatal("a further non-blocking spend should refuse")
	}
}

func TestNonBlockingSucceedsAfterRefill(t *testing.T) {
	clock := newFakeClock()
	lim := NewWithClock(1, 1, clock)
	if !lim.SpendBorrow(1, false) {
		t.Fatal("initial spend should succeed")
	}
	clock.Advance(time.Second)
	if !lim.SpendBorrow(1, false) {
		t.Fatal("spend should succeed after one second of refill")
	}
}

func TestTokensCappedAtBurst(t *testing.T) {
	clock := newFakeClock()
	lim := NewWithClock(10, 2, clock)
	clock.Advance(100 * time.Second)
	if got := lim.Tokens(); !approxEqual(got, 2) {
		t.Fatalf("expected tokens capped at burst=2, got %f", got)
	}
}

func TestRefillAccruesFractional(t *testing.T) {
	clock := newFakeClock()
	lim := NewWithClock(2, 1, clock)
	if !lim.SpendBorrow(1, false) {
		t.Fatal("spend should succeed")
	}
	clock.Advance(500 * time.Millisecond)
	got := lim.Tokens()
	if !approxEqual(got, 1.0) {
		t.Fatalf("expected 1 token after 0.5s of 2/s refill, got %f", got)
	}
}

func TestWaitDurationExact(t *testing.T) {
	clock := newFakeClock()
	lim := NewWithClock(2, 1, clock)
	if !lim.SpendBorrow(1, false) {
		t.Fatal("initial spend should succeed")
	}
	d, ok := lim.WaitDuration(1)
	if !ok {
		t.Fatal("expected WaitDuration to report success")
	}
	if d != 500*time.Millisecond {
		t.Fatalf("expected 500ms wait at 2/s, got %v", d)
	}
}

func TestWaitDurationOverBurst(t *testing.T) {
	lim := NewWithClock(10, 5, newFakeClock())
	if _, ok := lim.WaitDuration(6); ok {
		t.Fatal("WaitDuration(6) with burst=5 should be impossible")
	}
}

func TestWaitDurationZeroAndNegativeNoCost(t *testing.T) {
	lim := NewWithClock(1, 1, newFakeClock())
	for _, n := range []float64{0, -1, -3.5} {
		if d, ok := lim.WaitDuration(n); !ok || d != 0 {
			t.Fatalf("WaitDuration(%v) should be (0,true), got (%v,%v)", n, d, ok)
		}
	}
}

func TestSpendBorrowNonPositiveAlwaysSucceeds(t *testing.T) {
	lim := NewWithClock(1, 0.5, newFakeClock())
	for _, n := range []float64{0, -1, -7} {
		if !lim.SpendBorrow(n, false) {
			t.Fatalf("SpendBorrow(%v, false) should return true", n)
		}
		if !lim.SpendBorrow(n, true) {
			t.Fatalf("SpendBorrow(%v, true) should return true", n)
		}
	}
}

func TestClockGoingBackwardsDoesNotGrantTokens(t *testing.T) {
	clock := newFakeClock()
	lim := NewWithClock(10, 1, clock)
	if !lim.SpendBorrow(1, false) {
		t.Fatal("initial spend should succeed")
	}
	clock.Advance(-5 * time.Second)
	if got := lim.Tokens(); !approxEqual(got, 0) {
		t.Fatalf("backward clock should not grant tokens, got %f", got)
	}
}

func TestRateAndBurstAccessors(t *testing.T) {
	lim := NewWithClock(7.5, 3.25, newFakeClock())
	if !approxEqual(lim.Rate(), 7.5) {
		t.Fatalf("Rate() = %f, want 7.5", lim.Rate())
	}
	if !approxEqual(lim.Burst(), 3.25) {
		t.Fatalf("Burst() = %f, want 3.25", lim.Burst())
	}
}

func TestMultipleConsumersShareBucket(t *testing.T) {
	clock := newFakeClock()
	lim := NewWithClock(1, 3, clock)
	if !lim.SpendBorrow(1, false) {
		t.Fatal("spend 1 should succeed")
	}
	if !lim.SpendBorrow(1, false) {
		t.Fatal("spend 2 should succeed")
	}
	if !lim.SpendBorrow(1, false) {
		t.Fatal("spend 3 should succeed")
	}
	if lim.SpendBorrow(1, false) {
		t.Fatal("spend 4 should refuse — bucket exhausted")
	}
	clock.Advance(2 * time.Second)
	if !lim.SpendBorrow(2, false) {
		t.Fatal("after 2s at 1/s the bucket should hold 2 again")
	}
}
