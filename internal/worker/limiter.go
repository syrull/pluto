package worker

import (
	"context"
	"sync"
	"time"

	"github.com/syrull/pluto/internal/debug"
)

// Limits bound how aggressively a fan-out may start workers so a burst of
// parallel activity doesn't look like a portscan-shaped alarm to a target. All
// fields are optional; a zero field means "no limit".
type Limits struct {
	// MaxWorkers caps how many workers may run at once across every target.
	MaxWorkers int
	// PerTarget caps how many workers may run at once against a single scope
	// (the Spec.Scope string, used as the target key).
	PerTarget int
	// Interval is the minimum spacing between worker starts against the same
	// target, so starts against one host are paced rather than simultaneous.
	Interval time.Duration
}

// limiter enforces Limits. acquire blocks (honoring ctx) until a global slot and
// a per-target slot are free and the per-target rate interval has elapsed;
// release hands both slots back. It is safe for concurrent use.
type limiter struct {
	limits Limits
	global chan struct{} // buffered to MaxWorkers; nil when unlimited

	mu        sync.Mutex
	perTarget map[string]chan struct{} // one semaphore per target; created lazily
	lastStart map[string]time.Time     // last reserved start per target (rate gate)

	now   func() time.Time
	after func(time.Duration) <-chan time.Time
}

// newLimiter builds a limiter for the given limits.
func newLimiter(l Limits) *limiter {
	lm := &limiter{
		limits:    l,
		perTarget: make(map[string]chan struct{}),
		lastStart: make(map[string]time.Time),
		now:       time.Now,
		after:     time.After,
	}
	if l.MaxWorkers > 0 {
		lm.global = make(chan struct{}, l.MaxWorkers)
	}
	return lm
}

// targetSem returns the semaphore for target, creating it on first use. It
// returns nil when per-target concurrency is unlimited.
func (l *limiter) targetSem(target string) chan struct{} {
	if l.limits.PerTarget <= 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	sem, ok := l.perTarget[target]
	if !ok {
		sem = make(chan struct{}, l.limits.PerTarget)
		l.perTarget[target] = sem
	}
	return sem
}

// acquire blocks until a global and per-target slot are free and the rate gate
// allows a start against target, or ctx is done. On success the caller must call
// release(target) exactly once.
func (l *limiter) acquire(ctx context.Context, target string) error {
	if l.global != nil {
		select {
		case l.global <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if sem := l.targetSem(target); sem != nil {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			l.releaseGlobal()
			return ctx.Err()
		}
	}
	if err := l.rateGate(ctx, target); err != nil {
		l.release(target)
		return err
	}
	return nil
}

// rateGate reserves the next allowed start time for target and waits for it,
// spacing concurrent acquirers by Interval so starts against one target are
// paced. The reservation is made under the lock so simultaneous acquirers stack
// rather than all firing at once.
func (l *limiter) rateGate(ctx context.Context, target string) error {
	if l.limits.Interval <= 0 {
		return nil
	}
	l.mu.Lock()
	now := l.now()
	start := now
	if last, ok := l.lastStart[target]; ok {
		if earliest := last.Add(l.limits.Interval); earliest.After(start) {
			start = earliest
		}
	}
	l.lastStart[target] = start
	l.mu.Unlock()

	wait := start.Sub(now)
	if wait <= 0 {
		return nil
	}
	debug.Debug("worker", "rate gate wait", "target", target, "wait", wait)
	select {
	case <-l.after(wait):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release hands back the per-target and global slots taken by acquire.
func (l *limiter) release(target string) {
	if l.limits.PerTarget > 0 {
		l.mu.Lock()
		sem := l.perTarget[target]
		l.mu.Unlock()
		if sem != nil {
			select {
			case <-sem:
			default:
			}
		}
	}
	l.releaseGlobal()
}

func (l *limiter) releaseGlobal() {
	if l.global != nil {
		select {
		case <-l.global:
		default:
		}
	}
}
