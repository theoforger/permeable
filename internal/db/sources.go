package db

import (
	"context"
	"database/sql"
	"fmt"

	"permeable/internal/model"
)

// ListSources returns all sources ordered by creation time.
func ListSources(ctx context.Context, sqlDB *sql.DB) ([]model.Source, error) {
	rows, err := sqlDB.QueryContext(ctx, `
		SELECT id, name, type, url, enabled, default_include, title_prefix,
		       last_fetch_at, last_fetch_status, last_fetch_error, last_event_count,
		       source_ttl_minutes, created_at
		FROM sources
		ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("query sources: %w", err)
	}
	defer rows.Close()

	var sources []model.Source
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sources: %w", err)
	}
	return sources, nil
}

// GetSource returns a single source by id.
func GetSource(ctx context.Context, sqlDB *sql.DB, id int64) (model.Source, error) {
	row := sqlDB.QueryRowContext(ctx, `
		SELECT id, name, type, url, enabled, default_include, title_prefix,
		       last_fetch_at, last_fetch_status, last_fetch_error, last_event_count,
		       source_ttl_minutes, created_at
		FROM sources
		WHERE id = ?`, id)
	return scanSource(row)
}

// CreateSource inserts a new source and returns its id. s.Type, s.Name,
// and (for url sources) s.URL are required; other fields are optional.
func CreateSource(ctx context.Context, sqlDB *sql.DB, s model.Source) (int64, error) {
	res, err := sqlDB.ExecContext(ctx, `
		INSERT INTO sources (name, type, url, enabled, default_include, title_prefix)
		VALUES (?, ?, ?, ?, ?, ?)`,
		s.Name, string(s.Type), nullableString(s.URL), s.Enabled, s.DefaultInclude, nullableString(s.TitlePrefix))
	if err != nil {
		return 0, fmt.Errorf("insert source: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted source id: %w", err)
	}
	return id, nil
}

// UpdateSource updates the editable fields of an existing source. Type is
// fixed at creation time and is not updated here.
func UpdateSource(ctx context.Context, sqlDB *sql.DB, s model.Source) error {
	_, err := sqlDB.ExecContext(ctx, `
		UPDATE sources
		SET name = ?, url = ?, enabled = ?, default_include = ?, title_prefix = ?
		WHERE id = ?`,
		s.Name, nullableString(s.URL), s.Enabled, s.DefaultInclude, nullableString(s.TitlePrefix), s.ID)
	if err != nil {
		return fmt.Errorf("update source %d: %w", s.ID, err)
	}
	return nil
}

// DeleteSource removes a source. Its manual_events, event_rules, and
// event_exceptions cascade via foreign keys.
func DeleteSource(ctx context.Context, sqlDB *sql.DB, id int64) error {
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM sources WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete source %d: %w", id, err)
	}
	return nil
}

// DeleteAllSources removes every source (cascading to manual_events,
// event_rules, event_exceptions) — used by exportsvc's "replace" import
// mode to clear the slate before restoring from a snapshot.
func DeleteAllSources(ctx context.Context, sqlDB *sql.DB) error {
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM sources`); err != nil {
		return fmt.Errorf("delete all sources: %w", err)
	}
	return nil
}

// ImportSourceWithID inserts a source preserving its original id, so
// manual_events/event_rules/event_exceptions rows exported alongside it
// (which reference that id) stay valid without remapping. Health fields
// are deliberately not restored — see model.ExportBundle. Only meant for
// "replace" imports into an already-cleared sources table.
func ImportSourceWithID(ctx context.Context, sqlDB *sql.DB, s model.Source) error {
	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO sources (id, name, type, url, enabled, default_include, title_prefix)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, string(s.Type), nullableString(s.URL), s.Enabled, s.DefaultInclude, nullableString(s.TitlePrefix))
	if err != nil {
		return fmt.Errorf("import source %d (%s): %w", s.ID, s.Name, err)
	}
	return nil
}

// rowScanner abstracts over *sql.Row and *sql.Rows, both of which
// implement Scan.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSource(row rowScanner) (model.Source, error) {
	var (
		s                                model.Source
		typ                              string
		url, titlePrefix                 sql.NullString
		lastFetchAt                      sql.NullTime
		lastFetchStatus, lastFetchError  sql.NullString
		lastEventCount, sourceTTLMinutes sql.NullInt64
	)

	err := row.Scan(&s.ID, &s.Name, &typ, &url, &s.Enabled, &s.DefaultInclude, &titlePrefix,
		&lastFetchAt, &lastFetchStatus, &lastFetchError, &lastEventCount,
		&sourceTTLMinutes, &s.CreatedAt)
	if err != nil {
		return model.Source{}, fmt.Errorf("scan source: %w", err)
	}

	s.Type = model.SourceType(typ)
	s.URL = url.String
	s.TitlePrefix = titlePrefix.String
	s.LastFetchStatus = lastFetchStatus.String
	s.LastFetchError = lastFetchError.String
	if lastFetchAt.Valid {
		t := lastFetchAt.Time
		s.LastFetchAt = &t
	}
	if lastEventCount.Valid {
		n := int(lastEventCount.Int64)
		s.LastEventCount = &n
	}
	if sourceTTLMinutes.Valid {
		n := int(sourceTTLMinutes.Int64)
		s.SourceTTLMinutes = &n
	}

	return s, nil
}

func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
