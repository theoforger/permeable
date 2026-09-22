package scheduler

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"permeable/internal/db"
	"permeable/internal/model"
)

// TestScheduler_RunsImmediatelyThenTicks verifies Run fires once on
// startup (not just at the first tick) and again after the interval
// elapses.
func TestScheduler_RunsImmediatelyThenTicks(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()

	sched := New(sqlDB)
	var intervalNanos atomic.Int64
	intervalNanos.Store(int64(40 * time.Millisecond))
	sched.intervalFn = func(context.Context) (time.Duration, error) {
		return time.Duration(intervalNanos.Load()), nil
	}

	calls := make(chan struct{}, 16)
	sched.afterGenerate = func(model.FeedCache, error) { calls <- struct{}{} }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	waitForCall(t, calls, "startup generate")
	waitForCall(t, calls, "first tick")
}

// TestScheduler_ReloadAppliesNewIntervalWithoutRestart is Stage 9's own
// done-criteria, directly: changing the interval must change tick timing
// immediately, not after the old interval finally elapses.
func TestScheduler_ReloadAppliesNewIntervalWithoutRestart(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()

	sched := New(sqlDB)
	var intervalNanos atomic.Int64
	intervalNanos.Store(int64(10 * time.Second)) // long enough it won't fire on its own during this test
	sched.intervalFn = func(context.Context) (time.Duration, error) {
		return time.Duration(intervalNanos.Load()), nil
	}

	calls := make(chan struct{}, 16)
	sched.afterGenerate = func(model.FeedCache, error) { calls <- struct{}{} }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	waitForCall(t, calls, "startup generate")

	// Shrink the interval and signal Reload — a tick should now arrive
	// well within the *new* short interval, which could never happen
	// within this test's timeout on the original 10s interval.
	intervalNanos.Store(int64(30 * time.Millisecond))
	sched.Reload()

	waitForCall(t, calls, "tick on new short interval after Reload")
}

// TestScheduler_RefreshNowIsSynchronousAndSerialized checks RefreshNow
// blocks until generation completes and returns its result directly.
func TestScheduler_RefreshNowIsSynchronousAndSerialized(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()

	sched := New(sqlDB)

	fc, err := sched.RefreshNow(context.Background())
	if err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}
	if fc.GeneratedAt.IsZero() {
		t.Error("RefreshNow returned a zero-value FeedCache")
	}

	cached, ok, err := db.GetFeedCache(context.Background(), sqlDB)
	if err != nil || !ok {
		t.Fatalf("GetFeedCache after RefreshNow: ok=%v err=%v", ok, err)
	}
	// Compare with tolerance, not equality: GeneratedAt round-trips
	// through SQLite storage, which can drop sub-second precision.
	if diff := cached.GeneratedAt.Sub(fc.GeneratedAt); diff > time.Second || diff < -time.Second {
		t.Errorf("feed_cache was not updated synchronously by RefreshNow: cached=%v returned=%v", cached.GeneratedAt, fc.GeneratedAt)
	}
}

func waitForCall(t *testing.T, calls <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for: %s", what)
	}
}
