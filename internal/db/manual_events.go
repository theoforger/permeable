package db

import (
	"context"
	"database/sql"
	"fmt"

	"permeable/internal/model"
)

// ListManualEvents returns every event attached to a manual source,
// ordered by when they were added.
func ListManualEvents(ctx context.Context, sqlDB *sql.DB, sourceID int64) ([]model.ManualEvent, error) {
	rows, err := sqlDB.QueryContext(ctx, `
		SELECT id, source_id, uid, raw_vevent, added_at
		FROM manual_events
		WHERE source_id = ?
		ORDER BY added_at, id`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("query manual events for source %d: %w", sourceID, err)
	}
	defer rows.Close()

	var events []model.ManualEvent
	for rows.Next() {
		var e model.ManualEvent
		if err := rows.Scan(&e.ID, &e.SourceID, &e.UID, &e.RawVEvent, &e.AddedAt); err != nil {
			return nil, fmt.Errorf("scan manual event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate manual events: %w", err)
	}
	return events, nil
}

// ListAllManualEvents returns every manual event across all sources —
// used by exportsvc, which needs the full set, not one source at a time.
func ListAllManualEvents(ctx context.Context, sqlDB *sql.DB) ([]model.ManualEvent, error) {
	rows, err := sqlDB.QueryContext(ctx, `
		SELECT id, source_id, uid, raw_vevent, added_at
		FROM manual_events
		ORDER BY source_id, added_at, id`)
	if err != nil {
		return nil, fmt.Errorf("query all manual events: %w", err)
	}
	defer rows.Close()

	var events []model.ManualEvent
	for rows.Next() {
		var e model.ManualEvent
		if err := rows.Scan(&e.ID, &e.SourceID, &e.UID, &e.RawVEvent, &e.AddedAt); err != nil {
			return nil, fmt.Errorf("scan manual event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate manual events: %w", err)
	}
	return events, nil
}

// UpsertManualEvent inserts a manual event, or, if a row with the same
// (source_id, uid) already exists, updates its raw_vevent in place.
func UpsertManualEvent(ctx context.Context, sqlDB *sql.DB, sourceID int64, uid, rawVEvent string) error {
	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO manual_events (source_id, uid, raw_vevent)
		VALUES (?, ?, ?)
		ON CONFLICT (source_id, uid) DO UPDATE SET raw_vevent = excluded.raw_vevent`,
		sourceID, uid, rawVEvent)
	if err != nil {
		return fmt.Errorf("upsert manual event (source %d, uid %s): %w", sourceID, uid, err)
	}
	return nil
}

// DeleteManualEvent removes a single manual event, scoped to sourceID so a
// stray event id from another source can't be deleted through this call.
func DeleteManualEvent(ctx context.Context, sqlDB *sql.DB, sourceID, eventID int64) error {
	_, err := sqlDB.ExecContext(ctx, `
		DELETE FROM manual_events WHERE id = ? AND source_id = ?`, eventID, sourceID)
	if err != nil {
		return fmt.Errorf("delete manual event %d (source %d): %w", eventID, sourceID, err)
	}
	return nil
}
