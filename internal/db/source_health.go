package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SourceHealth holds the per-run outcome of fetching one source, written
// back after every pipeline run regardless of source type.
type SourceHealth struct {
	Status     string // "ok" | "error"
	Error      string // empty on success
	EventCount int
	TTLMinutes *int // parsed from X-PUBLISHED-TTL / REFRESH-INTERVAL; url sources only, nil otherwise
	FetchedAt  time.Time
}

// UpdateSourceHealth records the outcome of fetching one source.
func UpdateSourceHealth(ctx context.Context, sqlDB *sql.DB, sourceID int64, h SourceHealth) error {
	var ttl sql.NullInt64
	if h.TTLMinutes != nil {
		ttl = sql.NullInt64{Int64: int64(*h.TTLMinutes), Valid: true}
	}

	_, err := sqlDB.ExecContext(ctx, `
		UPDATE sources
		SET last_fetch_at = ?, last_fetch_status = ?, last_fetch_error = ?,
		    last_event_count = ?, source_ttl_minutes = ?
		WHERE id = ?`,
		h.FetchedAt, h.Status, nullableString(h.Error), h.EventCount, ttl, sourceID)
	if err != nil {
		return fmt.Errorf("update source health for source %d: %w", sourceID, err)
	}
	return nil
}
