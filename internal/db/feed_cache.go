package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"permeable/internal/model"
)

// GetFeedCache returns the cached feed. Unlike config/duration_filters,
// feed_cache is never seeded by migration (there's no sensible default
// feed content) — so the row genuinely may not exist yet, reported via
// the second return value rather than an error.
func GetFeedCache(ctx context.Context, sqlDB *sql.DB) (model.FeedCache, bool, error) {
	var fc model.FeedCache
	err := sqlDB.QueryRowContext(ctx, `
		SELECT ics_content, generated_at, total_events_pre_filter, total_events_post_filter,
		       sources_ok, sources_error, effective_ttl_minutes
		FROM feed_cache WHERE id = 1`,
	).Scan(&fc.ICSContent, &fc.GeneratedAt, &fc.TotalEventsPreFilter, &fc.TotalEventsPostFilter,
		&fc.SourcesOK, &fc.SourcesError, &fc.EffectiveTTLMinutes)
	if errors.Is(err, sql.ErrNoRows) {
		return model.FeedCache{}, false, nil
	}
	if err != nil {
		return model.FeedCache{}, false, fmt.Errorf("get feed cache: %w", err)
	}
	return fc, true, nil
}

// UpsertFeedCache replaces the cached feed. Genuinely upsert (unlike
// config/duration_filters' UPDATE-only pattern) since there's no row to
// seed a sensible default into before the first successful generation.
func UpsertFeedCache(ctx context.Context, sqlDB *sql.DB, fc model.FeedCache) error {
	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO feed_cache (id, ics_content, generated_at, total_events_pre_filter,
		                         total_events_post_filter, sources_ok, sources_error, effective_ttl_minutes)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			ics_content = excluded.ics_content,
			generated_at = excluded.generated_at,
			total_events_pre_filter = excluded.total_events_pre_filter,
			total_events_post_filter = excluded.total_events_post_filter,
			sources_ok = excluded.sources_ok,
			sources_error = excluded.sources_error,
			effective_ttl_minutes = excluded.effective_ttl_minutes`,
		fc.ICSContent, fc.GeneratedAt, fc.TotalEventsPreFilter, fc.TotalEventsPostFilter,
		fc.SourcesOK, fc.SourcesError, fc.EffectiveTTLMinutes)
	if err != nil {
		return fmt.Errorf("upsert feed cache: %w", err)
	}
	return nil
}
