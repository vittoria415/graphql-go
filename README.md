# graphql_go

A token bucket rate limiter for Go: control how many operations per second are
allowed, with a configurable burst ceiling and optional blocking consumption.

## Usage

```go
package main

import (
	"fmt"
	"time"
	"graphql_go"
)

func main() {
	// 5 ops/sec steady, allow up to 10 in a burst.
	lim := graphql_go.New(5, 10)

	if lim.Allow() { // non-blocking; consumes 1 token
		fmt.Println("admitted")
	}

	// Block until 3 tokens are available.
	lim.SpendBorrow(3, true)

	// Ask how long a blocking spend of 4 would wait, without mutating.
	d, ok := lim.WaitDuration(4)
	if ok {
		fmt.Println("would wait", d)
	}

	_ = time.Second
}
```

The exported surface is: `New(rate, burst float64) *Limiter`,
`NewWithClock(rate, burst float64, clock Clock) *Limiter`, the `Clock`
interface (`Now() time.Time`), and the `Limiter` type with methods
`Allow() bool`, `Wait()`, `SpendBorrow(n float64, block bool) bool`,
`WaitDuration(n float64) (time.Duration, bool)`, `Rate() float64`,
`Burst() float64`, and `Tokens() float64`.

## Why

Most rate limiting falls into two camps: a fixed window (simple but bursty at
the boundary) and a token bucket (smooth and burst-tolerant). This is a token
bucket, because the brief asked for burst support and steady throughput in the
same breath, and that is exactly what token buckets do.

The trade-off: a token bucket cannot promise a hard ceiling over any sub-second
interval. At rate=10, burst=10, the first ten calls in a millisecond all pass.
That is the cost of burst support; if you need a strict per-window cap, write a
fixed-window limiter instead.

## Edge cases

- The bucket starts full. A brand-new limiter will not throttle its very first
  burst of requests. That is intentional but worth knowing if you expected a
  cold start.
- `SpendBorrow` with `n <= 0` always returns `true` and consumes nothing. This
  keeps callers from having to special-case "zero items".
- If the clock goes backwards (NTP slew, or a fake clock reset in tests), the
  limiter treats the past as the present rather than awarding negative tokens,
  so a backwards slew never silently raises throughput.
- `WaitDuration(n)` reports `(0, false)` when `n > burst` — such a request can
  never succeed, regardless of how long you wait.

Tests use a fake clock and never touch wall time, so they are deterministic
regardless of host load. Run them with `go test -v ./...`.
