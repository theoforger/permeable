package db

import (
	"context"
	"database/sql"
	"fmt"

	"permeable/internal/model"
)

// ListFilters returns all global filters in the order they were created —
// filtersvc applies them in this order, last match wins.
func ListFilters(ctx context.Context, sqlDB *sql.DB) ([]model.Filter, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT id, field, type, value FROM filters ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query filters: %w", err)
	}
	defer rows.Close()

	var filters []model.Filter
	for rows.Next() {
		var f model.Filter
		var field, typ string
		if err := rows.Scan(&f.ID, &field, &typ, &f.Value); err != nil {
			return nil, fmt.Errorf("scan filter: %w", err)
		}
		f.Field = model.FilterField(field)
		f.Type = model.FilterAction(typ)
		filters = append(filters, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate filters: %w", err)
	}
	return filters, nil
}

// CreateFilter inserts a new global filter and returns its id.
func CreateFilter(ctx context.Context, sqlDB *sql.DB, f model.Filter) (int64, error) {
	res, err := sqlDB.ExecContext(ctx, `
		INSERT INTO filters (field, type, value) VALUES (?, ?, ?)`,
		string(f.Field), string(f.Type), f.Value)
	if err != nil {
		return 0, fmt.Errorf("insert filter: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted filter id: %w", err)
	}
	return id, nil
}

// DeleteFilter removes a global filter.
func DeleteFilter(ctx context.Context, sqlDB *sql.DB, id int64) error {
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM filters WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete filter %d: %w", id, err)
	}
	return nil
}

// DeleteAllFilters removes every global filter — used by exportsvc's
// "replace" import mode.
func DeleteAllFilters(ctx context.Context, sqlDB *sql.DB) error {
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM filters`); err != nil {
		return fmt.Errorf("delete all filters: %w", err)
	}
	return nil
}
