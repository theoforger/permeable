// Package scheduler owns the background cron wiring: it runs the full
// fetch -> filter -> generate pipeline (feedsvc.Generate) on a
// time.Ticker paced by config.refresh_minutes, and exposes a manual
// trigger ("Refresh now") for on-demand runs. Both paths funnel through
// the same feedsvc.Generate call, serialized so they never overlap.
package scheduler

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"permeable/internal/db"
	"permeable/internal/feedsvc"
	"permeable/internal/model"
)

// Scheduler runs feedsvc.Generate on a ticker paced by config.refresh_minutes.
type Scheduler struct {
	sqlDB *sql.DB

	mu sync.Mutex // serializes generate() so ticks and manual refreshes never overlap

	reloadCh chan struct{}

	// intervalFn resolves the current tick interval; overridable in
	// tests to avoid waiting out real refresh_minutes-scale durations.
	// Defaults to reading config.refresh_minutes.
	intervalFn func(ctx context.Context) (time.Duration, error)

	// afterGenerate, if set, is called after every generate() attempt
	// (tick or manual). Test-only hook; nil (no-op) in production.
	afterGenerate func(model.FeedCache, error)
}

// New returns a Scheduler paced by the live config.refresh_minutes value.
func New(sqlDB *sql.DB) *Scheduler {
	return &Scheduler{
		sqlDB:      sqlDB,
		reloadCh:   make(chan struct{}, 1),
		intervalFn: configInterval(sqlDB),
	}
}

func configInterval(sqlDB *sql.DB) func(context.Context) (time.Duration, error) {
	return func(ctx context.Context) (time.Duration, error) {
		cfg, err := db.GetConfig(ctx, sqlDB)
		if err != nil {
			return 0, err
		}
		return time.Duration(cfg.RefreshMinutes) * time.Minute, nil
	}
}

// Run generates once immediately, then on every tick of the interval
// resolved by intervalFn, until ctx is canceled. Call Reload after
// changing config.refresh_minutes to apply the new interval immediately
// instead of waiting out whatever's left of the old one.
func (s *Scheduler) Run(ctx context.Context) {
	interval := s.mustInterval(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.generate(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.reloadCh:
			if newInterval := s.mustInterval(ctx); newInterval != interval {
				interval = newInterval
				ticker.Reset(interval)
				log.Printf("scheduler: refresh interval changed to %s", interval)
			}
		case <-ticker.C:
			s.generate(ctx)
		}
	}
}

// Reload signals Run to re-read the interval and reset its ticker now,
// rather than at the next tick. Safe to call whether or not Run is
// currently active; non-blocking.
func (s *Scheduler) Reload() {
	select {
	case s.reloadCh <- struct{}{}:
	default: // a reload is already pending; no need to queue a second
	}
}

// RefreshNow runs the pipeline immediately, outside the regular ticker
// schedule (e.g. a "Refresh now" button), and returns its result.
func (s *Scheduler) RefreshNow(ctx context.Context) (model.FeedCache, error) {
	return s.generate(ctx)
}

func (s *Scheduler) generate(ctx context.Context) (model.FeedCache, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fc, err := feedsvc.Generate(ctx, s.sqlDB)
	if err != nil {
		log.Printf("scheduler: generate: %v", err)
	} else {
		log.Printf("scheduler: generated feed: pre=%d post=%d ok=%d error=%d ttl=%dm",
			fc.TotalEventsPreFilter, fc.TotalEventsPostFilter, fc.SourcesOK, fc.SourcesError, fc.EffectiveTTLMinutes)
	}
	if s.afterGenerate != nil {
		s.afterGenerate(fc, err)
	}
	return fc, err
}

func (s *Scheduler) mustInterval(ctx context.Context) time.Duration {
	interval, err := s.intervalFn(ctx)
	if err != nil {
		log.Printf("scheduler: resolve refresh interval: %v (falling back to 60m)", err)
		return 60 * time.Minute
	}
	if interval <= 0 {
		log.Printf("scheduler: refresh interval %s is not positive, falling back to 60m", interval)
		return 60 * time.Minute
	}
	return interval
}
