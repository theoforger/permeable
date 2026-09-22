package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"permeable/internal/model"
)

// ListEventExceptions returns every event_exceptions row, across all
// sources — filtersvc needs the full set to resolve inclusion for any
// event.
func ListEventExceptions(ctx context.Context, sqlDB *sql.DB) ([]model.EventException, error) {
	rows, err := sqlDB.QueryContext(ctx, `
		SELECT id, source_id, title_normalized, occurrence_date, created_at
		FROM event_exceptions ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("query event exceptions: %w", err)
	}
	defer rows.Close()

	var exceptions []model.EventException
	for rows.Next() {
		var e model.EventException
		// occurrence_date is declared DATE in schema.sql; modernc.org/sqlite
		// sniffs that declared type and returns a time.Time rather than the
		// raw text, so scan into time.Time here (not directly into the
		// model's string field) and reformat — scanning straight into
		// *string would silently corrupt "2026-08-25" into
		// "2026-08-25T00:00:00Z" and break every date comparison.
		var occurrenceDate time.Time
		if err := rows.Scan(&e.ID, &e.SourceID, &e.TitleNormalized, &occurrenceDate, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event exception: %w", err)
		}
		e.OccurrenceDate = occurrenceDate.Format("2006-01-02")
		exceptions = append(exceptions, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event exceptions: %w", err)
	}
	return exceptions, nil
}

// CreateEventException hides a single occurrence. A duplicate
// (source_id, title_normalized, occurrence_date) is a no-op — the schema's
// UNIQUE constraint on that triple already means "this occurrence is
// hidden," so there's nothing more to record.
func CreateEventException(ctx context.Context, sqlDB *sql.DB, sourceID int64, titleNormalized, occurrenceDate string) error {
	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO event_exceptions (source_id, title_normalized, occurrence_date)
		VALUES (?, ?, ?)
		ON CONFLICT (source_id, title_normalized, occurrence_date) DO NOTHING`,
		sourceID, titleNormalized, occurrenceDate)
	if err != nil {
		return fmt.Errorf("create event exception (source %d, title %q, date %s): %w", sourceID, titleNormalized, occurrenceDate, err)
	}
	return nil
}

// DeleteEventException removes an event exception.
func DeleteEventException(ctx context.Context, sqlDB *sql.DB, id int64) error {
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM event_exceptions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete event exception %d: %w", id, err)
	}
	return nil
}
