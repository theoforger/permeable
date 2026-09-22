// Package feedsvc runs the full pipeline (fetch, resolve, generate) and
// caches the result: CLAUDE.md pipeline steps 8-11. GET /feed.ics always
// serves feed_cache as-is (step 12) — nothing here runs on request.
package feedsvc

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	ics "github.com/arran4/golang-ical"

	"permeable/internal/db"
	"permeable/internal/fetchsvc"
	"permeable/internal/filtersvc"
	"permeable/internal/model"
)

// Generate runs fetch -> resolve -> generate end to end and writes the
// result to feed_cache. It's the single entrypoint both the eventual
// scheduler (Stage 9 ticker) and a manual "refresh now" trigger call.
func Generate(ctx context.Context, sqlDB *sql.DB) (model.FeedCache, error) {
	result, err := fetchsvc.Run(ctx, sqlDB)
	if err != nil {
		return model.FeedCache{}, fmt.Errorf("fetch: %w", err)
	}

	sources, err := db.ListSources(ctx, sqlDB)
	if err != nil {
		return model.FeedCache{}, fmt.Errorf("list sources: %w", err)
	}
	sourcesByID := make(map[int64]model.Source, len(sources))
	for _, s := range sources {
		sourcesByID[s.ID] = s
	}

	filters, err := db.ListFilters(ctx, sqlDB)
	if err != nil {
		return model.FeedCache{}, fmt.Errorf("list filters: %w", err)
	}
	durationFilter, err := db.GetDurationFilters(ctx, sqlDB)
	if err != nil {
		return model.FeedCache{}, fmt.Errorf("get duration filters: %w", err)
	}
	rules, err := db.ListEventRules(ctx, sqlDB)
	if err != nil {
		return model.FeedCache{}, fmt.Errorf("list event rules: %w", err)
	}
	exceptions, err := db.ListEventExceptions(ctx, sqlDB)
	if err != nil {
		return model.FeedCache{}, fmt.Errorf("list event exceptions: %w", err)
	}
	cfg, err := db.GetConfig(ctx, sqlDB)
	if err != nil {
		return model.FeedCache{}, fmt.Errorf("get config: %w", err)
	}

	resolved := filtersvc.Resolve(result.Events, filtersvc.Input{
		Sources: sourcesByID, Rules: rules, Exceptions: exceptions, Filters: filters, Duration: durationFilter,
	})

	ttlMinutes := effectiveTTLMinutes(sources, cfg.MaxFeedTTLMinutes)
	icsContent := generateICS(cfg.FeedTitle, resolved, sourcesByID, ttlMinutes)

	fc := model.FeedCache{
		ICSContent:            icsContent,
		GeneratedAt:           time.Now(),
		TotalEventsPreFilter:  len(result.Events),
		TotalEventsPostFilter: len(resolved),
		SourcesOK:             result.SourcesOK,
		SourcesError:          result.SourcesError,
		EffectiveTTLMinutes:   ttlMinutes,
	}
	if err := db.UpsertFeedCache(ctx, sqlDB, fc); err != nil {
		return model.FeedCache{}, fmt.Errorf("write feed cache: %w", err)
	}

	return fc, nil
}

// effectiveTTLMinutes implements CLAUDE.md pipeline step 9: the minimum
// of (a) the most frequently declared source_ttl_minutes among enabled
// sources and (b) config.max_feed_ttl_minutes. If no enabled source
// declares a TTL, max_feed_ttl_minutes is used alone. Ties in "most
// frequent" break toward the smaller value (a safer, more-frequent
// refresh default).
func effectiveTTLMinutes(sources []model.Source, maxFeedTTLMinutes int) int {
	counts := make(map[int]int)
	for _, s := range sources {
		if s.Enabled && s.SourceTTLMinutes != nil {
			counts[*s.SourceTTLMinutes]++
		}
	}
	if len(counts) == 0 {
		return maxFeedTTLMinutes
	}

	mode, bestCount := 0, -1
	for ttl, count := range counts {
		if count > bestCount || (count == bestCount && ttl < mode) {
			mode, bestCount = ttl, count
		}
	}

	if mode < maxFeedTTLMinutes {
		return mode
	}
	return maxFeedTTLMinutes
}

// generateICS builds the output feed: one standalone VEVENT per resolved
// occurrence (RRULE is not re-emitted — occurrences were already
// individually included/excluded upstream, which a raw RRULE can't
// represent), with title_prefix applied for display now that matching
// (which used the raw title) is done.
func generateICS(feedTitle string, events []model.Event, sourcesByID map[int64]model.Source, ttlMinutes int) string {
	cal := ics.NewCalendar()
	cal.SetProductId("-//permeable//EN")
	if feedTitle != "" {
		cal.SetXWRCalName(feedTitle)
		cal.SetName(feedTitle)
	}

	ttlISO8601 := fmt.Sprintf("PT%dM", ttlMinutes)
	cal.SetXPublishedTTL(ttlISO8601)
	cal.SetRefreshInterval(ttlISO8601)

	now := time.Now()
	for _, e := range events {
		e = applyTitlePrefix(e, sourcesByID)

		// Occurrences of the same recurring event share e.UID (correct
		// for dedup upstream, see fetchsvc); RFC 5545 requires a VEVENT's
		// UID to be unique unless paired with RECURRENCE-ID, which we
		// don't emit (Stage 4/12 known gap) — so synthesize a
		// per-occurrence UID here, at the output boundary only.
		outUID := fmt.Sprintf("%s-%d", e.UID, e.Start.Unix())
		vev := cal.AddEvent(outUID)
		vev.SetDtStampTime(now)
		vev.SetSummary(ics.ToText(e.Title))
		if e.Description != "" {
			vev.SetDescription(ics.ToText(e.Description))
		}
		if e.Location != "" {
			vev.SetLocation(ics.ToText(e.Location))
		}
		if e.AllDay {
			vev.SetAllDayStartAt(e.Start)
			vev.SetAllDayEndAt(e.End)
		} else {
			vev.SetStartAt(e.Start)
			vev.SetEndAt(e.End)
		}
	}

	return cal.Serialize()
}

func applyTitlePrefix(e model.Event, sourcesByID map[int64]model.Source) model.Event {
	if s, ok := sourcesByID[e.SourceID]; ok && s.TitlePrefix != "" {
		e.Title = s.TitlePrefix + " " + e.Title
	}
	return e
}
