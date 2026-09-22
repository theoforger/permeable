package db

import (
	"context"
	"database/sql"
	"fmt"

	"permeable/internal/model"
)

// ListEventRules returns every event_rules row, across all sources —
// filtersvc needs the full set to resolve inclusion for any event.
func ListEventRules(ctx context.Context, sqlDB *sql.DB) ([]model.EventRule, error) {
	rows, err := sqlDB.QueryContext(ctx, `
		SELECT id, source_id, title_normalized, action, created_at
		FROM event_rules ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("query event rules: %w", err)
	}
	defer rows.Close()

	var rules []model.EventRule
	for rows.Next() {
		var r model.EventRule
		var action string
		if err := rows.Scan(&r.ID, &r.SourceID, &r.TitleNormalized, &action, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event rule: %w", err)
		}
		r.Action = model.FilterAction(action)
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event rules: %w", err)
	}
	return rules, nil
}

// UpsertEventRule creates a rule, or updates its action if one already
// exists for (source_id, title_normalized) — the schema's UNIQUE
// constraint, so re-choosing include/exclude for the same title flips it
// in place instead of erroring.
func UpsertEventRule(ctx context.Context, sqlDB *sql.DB, sourceID int64, titleNormalized string, action model.FilterAction) error {
	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO event_rules (source_id, title_normalized, action)
		VALUES (?, ?, ?)
		ON CONFLICT (source_id, title_normalized) DO UPDATE SET action = excluded.action`,
		sourceID, titleNormalized, string(action))
	if err != nil {
		return fmt.Errorf("upsert event rule (source %d, title %q): %w", sourceID, titleNormalized, err)
	}
	return nil
}

// DeleteEventRule removes an event rule.
func DeleteEventRule(ctx context.Context, sqlDB *sql.DB, id int64) error {
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM event_rules WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete event rule %d: %w", id, err)
	}
	return nil
}
