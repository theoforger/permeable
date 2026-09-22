// Package db owns the SQLite connection and schema migrations. Migration
// SQL files are embedded at build time (see migrations/) so the binary
// never reads schema files from disk at runtime.
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens (creating if necessary) the SQLite database at path, enables
// foreign key enforcement and WAL mode, applies any pending migrations,
// and seeds the singleton config/duration_filters rows if this is a
// brand new database. initialRefreshMinutes seeds config.refresh_minutes
// on that first run only (the -refresh-minutes flag/env var) — every run
// after that reads/writes it as a normal DB-backed setting, no longer
// tied to the flag.
func Open(path string, initialRefreshMinutes int) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db directory %q: %w", dir, err)
		}
	}

	// busy_timeout makes concurrent writers (fetchsvc fetches sources in
	// parallel, each writing its own health row) block and retry instead
	// of immediately failing with SQLITE_BUSY; WAL mode lets those writes
	// interleave with readers without blocking them.
	dsn := path + "?_foreign_keys=1&_busy_timeout=5000&_journal_mode=WAL"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite at %q: %w", path, err)
	}

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite at %q: %w", path, err)
	}

	if err := migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	if err := seedSingletons(sqlDB, initialRefreshMinutes); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("seed config: %w", err)
	}

	return sqlDB, nil
}

// seedSingletons ensures the config/duration_filters singleton rows
// exist (CLAUDE.md: "treat them as upsert-only, never insert a second
// row" — schema.sql never seeds them itself). INSERT OR IGNORE makes
// this idempotent: a no-op on every run after the first.
func seedSingletons(sqlDB *sql.DB, initialRefreshMinutes int) error {
	if _, err := sqlDB.Exec(
		`INSERT OR IGNORE INTO config (id, refresh_minutes) VALUES (1, ?)`, initialRefreshMinutes,
	); err != nil {
		return fmt.Errorf("seed config row: %w", err)
	}
	if _, err := sqlDB.Exec(`INSERT OR IGNORE INTO duration_filters (id) VALUES (1)`); err != nil {
		return fmt.Errorf("seed duration_filters row: %w", err)
	}
	return nil
}

// migrate applies every embedded migration not yet recorded in
// schema_migrations, in ascending version order, each in its own
// transaction.
func migrate(sqlDB *sql.DB) error {
	if _, err := sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	names, err := migrationFilenames()
	if err != nil {
		return err
	}

	for _, name := range names {
		version, err := versionFromFilename(name)
		if err != nil {
			return err
		}

		var alreadyApplied bool
		if err := sqlDB.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)`, version,
		).Scan(&alreadyApplied); err != nil {
			return fmt.Errorf("check migration %d applied: %w", version, err)
		}
		if alreadyApplied {
			continue
		}

		if err := applyMigration(sqlDB, name, version); err != nil {
			return err
		}
	}

	return nil
}

func applyMigration(sqlDB *sql.DB, filename string, version int) error {
	script, err := migrationsFS.ReadFile("migrations/" + filename)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", filename, err)
	}

	tx, err := sqlDB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx for migration %d: %w", version, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	if _, err := tx.Exec(string(script)); err != nil {
		return fmt.Errorf("apply migration %d (%s): %w", version, filename, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", version, err)
	}
	log.Printf("db: applied migration %d (%s)", version, filename)
	return nil
}

func migrationFilenames() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations dir: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// versionFromFilename extracts the leading integer from a migration
// filename like "0001_initial.sql" -> 1.
func versionFromFilename(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration file %q missing '<version>_' prefix", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("migration file %q has non-numeric version prefix: %w", name, err)
	}
	return version, nil
}
