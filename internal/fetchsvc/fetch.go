// Package fetchsvc fetches every enabled source concurrently — url
// sources over HTTP, manual sources from manual_events — normalizes their
// VEVENTs into model.Event, expands recurrence and merges/dedupes across
// sources, and writes per-source health fields back to the DB. A single
// source's failure is recorded on that source and never aborts the run.
package fetchsvc

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	ics "github.com/arran4/golang-ical"
	"golang.org/x/sync/errgroup"

	"permeable/internal/db"
	"permeable/internal/model"
)

const fetchTimeout = 30 * time.Second

// Result is the aggregated outcome of one fetch run: every source's
// events, recurrence-expanded within the configured date window, merged
// across sources, and deduped to one entry per (UID, occurrence start);
// plus how many of this run's enabled sources succeeded/failed.
type Result struct {
	Events       []model.Event
	SourcesOK    int
	SourcesError int
}

// Run fetches all enabled sources concurrently, then expands and merges
// the combined events per CLAUDE.md pipeline steps 5-6.
func Run(ctx context.Context, sqlDB *sql.DB) (Result, error) {
	sources, err := db.ListSources(ctx, sqlDB)
	if err != nil {
		return Result{}, fmt.Errorf("list sources: %w", err)
	}

	var (
		mu                      sync.Mutex
		events                  []model.Event
		sourcesOK, sourcesError int
	)

	g, gctx := errgroup.WithContext(ctx)
	for _, s := range sources {
		if !s.Enabled {
			continue
		}
		g.Go(func() error {
			fetched, health := fetchSource(gctx, sqlDB, s)
			if err := db.UpdateSourceHealth(gctx, sqlDB, s.ID, health); err != nil {
				log.Printf("fetchsvc: write health for source %d (%s): %v", s.ID, s.Name, err)
			}
			mu.Lock()
			events = append(events, fetched...)
			if health.Status == "ok" {
				sourcesOK++
			} else {
				sourcesError++
			}
			mu.Unlock()
			return nil // per-source failures live in health, never abort the run
		})
	}
	if err := g.Wait(); err != nil {
		return Result{}, fmt.Errorf("fetch sources: %w", err)
	}

	cfg, err := db.GetConfig(ctx, sqlDB)
	if err != nil {
		return Result{}, fmt.Errorf("get config: %w", err)
	}
	now := time.Now()
	windowStart := now.AddDate(0, 0, -cfg.DaysBack)
	windowEnd := now.AddDate(0, 0, cfg.DaysForward)

	return Result{
		Events:       expandAll(events, windowStart, windowEnd),
		SourcesOK:    sourcesOK,
		SourcesError: sourcesError,
	}, nil
}

func fetchSource(ctx context.Context, sqlDB *sql.DB, s model.Source) ([]model.Event, db.SourceHealth) {
	now := time.Now()
	switch s.Type {
	case model.SourceTypeURL:
		return fetchURLSource(ctx, s, now)
	case model.SourceTypeManual:
		return fetchManualSource(ctx, sqlDB, s, now)
	default:
		return nil, errHealth(now, fmt.Errorf("unknown source type %q", s.Type))
	}
}

func fetchURLSource(ctx context.Context, s model.Source, now time.Time) ([]model.Event, db.SourceHealth) {
	reqCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, errHealth(now, fmt.Errorf("build request: %w", err))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errHealth(now, fmt.Errorf("http get: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errHealth(now, fmt.Errorf("http status %d", resp.StatusCode))
	}

	cal, err := ics.ParseCalendar(resp.Body)
	if err != nil {
		return nil, errHealth(now, fmt.Errorf("parse ics: %w", err))
	}

	events := normalizeEvents(s.ID, s.Name, cal.Events())
	return events, db.SourceHealth{
		Status:     "ok",
		EventCount: len(events),
		TTLMinutes: sourceTTLMinutes(cal),
		FetchedAt:  now,
	}
}

func fetchManualSource(ctx context.Context, sqlDB *sql.DB, s model.Source, now time.Time) ([]model.Event, db.SourceHealth) {
	manualEvents, err := db.ListManualEvents(ctx, sqlDB, s.ID)
	if err != nil {
		return nil, errHealth(now, fmt.Errorf("list manual events: %w", err))
	}
	if len(manualEvents) == 0 {
		return nil, db.SourceHealth{Status: "ok", FetchedAt: now}
	}

	// manual_events stores bare "BEGIN:VEVENT...END:VEVENT" blocks (see
	// schema.sql); wrap them in a minimal VCALENDAR so the same parser
	// used for url sources can be reused here.
	var wrapped strings.Builder
	wrapped.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//permeable//manual//EN\r\n")
	for _, me := range manualEvents {
		wrapped.WriteString(me.RawVEvent)
		wrapped.WriteString("\r\n")
	}
	wrapped.WriteString("END:VCALENDAR\r\n")

	cal, err := ics.ParseCalendar(strings.NewReader(wrapped.String()))
	if err != nil {
		return nil, errHealth(now, fmt.Errorf("parse manual events: %w", err))
	}

	events := normalizeEvents(s.ID, s.Name, cal.Events())
	return events, db.SourceHealth{Status: "ok", EventCount: len(events), FetchedAt: now}
}

func normalizeEvents(sourceID int64, sourceName string, raw []*ics.VEvent) []model.Event {
	events := make([]model.Event, 0, len(raw))
	for _, ev := range raw {
		e, err := normalizeEvent(sourceID, ev)
		if err != nil {
			log.Printf("fetchsvc: skipping malformed VEVENT in source %d (%s): %v", sourceID, sourceName, err)
			continue
		}
		events = append(events, e)
	}
	return events
}

func errHealth(now time.Time, err error) db.SourceHealth {
	return db.SourceHealth{Status: "error", Error: err.Error(), FetchedAt: now}
}
