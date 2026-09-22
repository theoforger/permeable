package db

import (
	"context"
	"database/sql"
	"fmt"

	"permeable/internal/model"
)

// GetDurationFilters returns the single global min/max duration setting.
// The row is seeded by migration 0002, so this should never see
// sql.ErrNoRows in practice.
func GetDurationFilters(ctx context.Context, sqlDB *sql.DB) (model.DurationFilter, error) {
	var min, max sql.NullInt64
	err := sqlDB.QueryRowContext(ctx, `SELECT min_minutes, max_minutes FROM duration_filters WHERE id = 1`).
		Scan(&min, &max)
	if err != nil {
		return model.DurationFilter{}, fmt.Errorf("get duration filters: %w", err)
	}

	var df model.DurationFilter
	if min.Valid {
		n := int(min.Int64)
		df.MinMinutes = &n
	}
	if max.Valid {
		n := int(max.Int64)
		df.MaxMinutes = &n
	}
	return df, nil
}

// UpdateDurationFilters updates the single global min/max duration
// setting. Singleton table — always an UPDATE, never an INSERT.
func UpdateDurationFilters(ctx context.Context, sqlDB *sql.DB, df model.DurationFilter) error {
	var min, max sql.NullInt64
	if df.MinMinutes != nil {
		min = sql.NullInt64{Int64: int64(*df.MinMinutes), Valid: true}
	}
	if df.MaxMinutes != nil {
		max = sql.NullInt64{Int64: int64(*df.MaxMinutes), Valid: true}
	}

	_, err := sqlDB.ExecContext(ctx, `
		UPDATE duration_filters SET min_minutes = ?, max_minutes = ? WHERE id = 1`, min, max)
	if err != nil {
		return fmt.Errorf("update duration filters: %w", err)
	}
	return nil
}
