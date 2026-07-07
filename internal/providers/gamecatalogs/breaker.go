// Package gamecatalogs is the quarantined adapter for the unofficial
// Game Pass and PS+ catalog endpoints: full-catalog snapshots fetched
// once per refresh cycle, persisted under catalogs/, retained on fetch
// failure, and matched locally against tracked games.
package gamecatalogs

// breaker is a per-catalog circuit breaker: after threshold consecutive
// request failures it opens and stays open until Reset (cycle start), so
// a dead unofficial endpoint cannot stall a refresh cycle.
type breaker struct {
	threshold int
	failures  int
	open      bool
}

func newBreaker(threshold int) *breaker { return &breaker{threshold: threshold} }

func (b *breaker) Allow() bool { return !b.open }

func (b *breaker) Success() { b.failures = 0 }

func (b *breaker) Failure() {
	b.failures++
	if b.failures >= b.threshold {
		b.open = true
	}
}

func (b *breaker) Reset() {
	b.failures = 0
	b.open = false
}
