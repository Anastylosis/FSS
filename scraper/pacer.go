package scraper

import (
	"context"
	"sync"
	"time"
)

// Pacer spaces requests across every goroutine that shares it.
//
// A worker pool where each goroutine sleeps `--delay` between its own requests
// does not pace the site at all: N workers issue N near-simultaneous requests
// every delay, so eight workers at the 500ms default hit ~16 requests a second
// rather than two. A Pacer hands out one slot per delay to whichever goroutine
// asks next, which is the cadence `--delay` and RecommendedDelay describe.
//
// A zero or negative delay disables pacing entirely, matching `--delay 0`.
type Pacer struct {
	mu    sync.Mutex
	delay time.Duration
	next  time.Time
}

// NewPacer returns a Pacer handing out one slot every delay. A zero or
// negative delay yields a Pacer that never blocks.
func NewPacer(delay time.Duration) *Pacer {
	return &Pacer{delay: delay}
}

// reserve claims the next slot and reports how long the caller must wait for
// it. The slot is claimed even if the caller then gives up, which is what keeps
// concurrent callers from all taking the same one.
func (p *Pacer) reserve(now time.Time) time.Duration {
	if p == nil || p.delay <= 0 {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next.Before(now) {
		p.next = now
	}
	wait := p.next.Sub(now)
	p.next = p.next.Add(p.delay)
	return wait
}

// Wait blocks until this Pacer's next slot, reporting false if ctx ended first.
func (p *Pacer) Wait(ctx context.Context) bool {
	wait := p.reserve(time.Now())
	if wait <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

type pacerKey struct{}

// WithPacer attaches a Pacer to ctx so every goroutine of a scrape shares one
// schedule. The cmd layer installs it once per run from the resolved delay.
func WithPacer(ctx context.Context, p *Pacer) context.Context {
	return context.WithValue(ctx, pacerKey{}, p)
}

// PacerFrom returns the Pacer attached to ctx, or nil.
func PacerFrom(ctx context.Context) *Pacer {
	p, _ := ctx.Value(pacerKey{}).(*Pacer)
	return p
}

// Pace blocks for this scrape's turn before the caller's next request, and
// reports false if ctx ended first.
//
// With a Pacer on the context — which `fss scrape` installs from the resolved
// delay — the wait is shared, so concurrent workers interleave instead of
// bursting. Without one, it falls back to sleeping `delay` in the calling
// goroutine, which is what a scraper driven directly as a library does.
func Pace(ctx context.Context, delay time.Duration) bool {
	if p := PacerFrom(ctx); p != nil {
		return p.Wait(ctx)
	}
	if delay <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
