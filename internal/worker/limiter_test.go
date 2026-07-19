package worker

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestLimiterPerTargetCap proves no more than PerTarget acquirers hold the same
// target at once, while a different target is unaffected.
func TestLimiterPerTargetCap(t *testing.T) {
	l := newLimiter(Limits{PerTarget: 2})
	var mu sync.Mutex
	live, max := 0, 0
	release := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.acquire(context.Background(), "host"); err != nil {
				return
			}
			mu.Lock()
			live++
			if live > max {
				max = live
			}
			mu.Unlock()
			<-release
			mu.Lock()
			live--
			mu.Unlock()
			l.release("host")
		}()
	}

	// A different target must not be blocked by the "host" cap.
	if err := l.acquire(context.Background(), "other"); err != nil {
		t.Fatalf("acquire other: %v", err)
	}
	l.release("other")

	waitForCond(t, func() bool { mu.Lock(); defer mu.Unlock(); return max == 2 })
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	if max > 2 {
		mu.Unlock()
		t.Fatalf("max concurrent on host = %d, want 2", max)
	}
	mu.Unlock()
	close(release)
	wg.Wait()
}

// TestLimiterGlobalCap proves MaxWorkers bounds total concurrency across targets.
func TestLimiterGlobalCap(t *testing.T) {
	l := newLimiter(Limits{MaxWorkers: 2})
	var mu sync.Mutex
	live, max := 0, 0
	release := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < 6; i++ {
		target := "t" + string(rune('a'+i)) // all distinct targets
		wg.Add(1)
		go func(tg string) {
			defer wg.Done()
			if err := l.acquire(context.Background(), tg); err != nil {
				return
			}
			mu.Lock()
			live++
			if live > max {
				max = live
			}
			mu.Unlock()
			<-release
			mu.Lock()
			live--
			mu.Unlock()
			l.release(tg)
		}(target)
	}

	waitForCond(t, func() bool { mu.Lock(); defer mu.Unlock(); return max == 2 })
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	got := max
	mu.Unlock()
	if got != 2 {
		t.Fatalf("global max concurrent = %d, want 2", got)
	}
	close(release)
	wg.Wait()
}

// TestLimiterRateSpacing proves starts against one target are spaced by Interval.
func TestLimiterRateSpacing(t *testing.T) {
	const interval = 25 * time.Millisecond
	l := newLimiter(Limits{Interval: interval})

	var mu sync.Mutex
	var starts []time.Time
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.acquire(context.Background(), "host"); err != nil {
				return
			}
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
			l.release("host")
		}()
	}
	wg.Wait()

	if len(starts) != 3 {
		t.Fatalf("recorded %d starts, want 3", len(starts))
	}
	sortTimes(starts)
	for i := 1; i < len(starts); i++ {
		gap := starts[i].Sub(starts[i-1])
		if gap < interval-10*time.Millisecond {
			t.Fatalf("start %d came %v after the previous, want >= ~%v (rate limit)", i, gap, interval)
		}
	}
}

// TestLimiterAcquireCanceled proves a canceled context unblocks a queued
// acquirer instead of wedging it.
func TestLimiterAcquireCanceled(t *testing.T) {
	l := newLimiter(Limits{PerTarget: 1})
	if err := l.acquire(context.Background(), "host"); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.acquire(ctx, "host") }()

	// The second acquire must block behind the cap until we cancel it.
	select {
	case err := <-done:
		t.Fatalf("second acquire returned early with %v, want it to block", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled acquire returned nil, want a context error")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled acquire did not unblock")
	}
	l.release("host")
}

func waitForCond(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func sortTimes(ts []time.Time) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j].Before(ts[j-1]); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}
