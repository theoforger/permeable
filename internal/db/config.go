package db

import (
	"context"
	"database/sql"
	"fmt"

	"permeable/internal/model"
)

// GetConfig returns the single global config row. The row is seeded by
// migration 0002, so this should never see sql.ErrNoRows in practice.
func GetConfig(ctx context.Context, sqlDB *sql.DB) (model.Config, error) {
	var c model.Config
	err := sqlDB.QueryRowContext(ctx, `
		SELECT refresh_minutes, max_feed_ttl_minutes, days_back, days_forward, feed_title
		FROM config WHERE id = 1`,
	).Scan(&c.RefreshMinutes, &c.MaxFeedTTLMinutes, &c.DaysBack, &c.DaysForward, &c.FeedTitle)
	if err != nil {
		return model.Config{}, fmt.Errorf("get config: %w", err)
	}
	return c, nil
}

// UpdateConfig updates the single global config row. Singleton table —
// this is always an UPDATE, never an INSERT (see CLAUDE.md conventions).
func UpdateConfig(ctx context.Context, sqlDB *sql.DB, c model.Config) error {
	_, err := sqlDB.ExecContext(ctx, `
		UPDATE config
		SET refresh_minutes = ?, max_feed_ttl_minutes = ?, days_back = ?, days_forward = ?, feed_title = ?
		WHERE id = 1`,
		c.RefreshMinutes, c.MaxFeedTTLMinutes, c.DaysBack, c.DaysForward, c.FeedTitle)
	if err != nil {
		return fmt.Errorf("update config: %w", err)
	}
	return nil
}
