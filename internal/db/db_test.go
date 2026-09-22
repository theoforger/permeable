package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"permeable/internal/db"
)

// TestOpen_SeedsInitialRefreshMinutesOnFirstRun guards a real bug: the
// -refresh-minutes flag/env var was parsed but never actually applied —
// the config row was always seeded with the schema's hardcoded default
// (60) regardless of what was passed in.
func TestOpen_SeedsInitialRefreshMinutesOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permeable.db")

	sqlDB, err := db.Open(path, 15)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()

	cfg, err := db.GetConfig(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.RefreshMinutes != 15 {
		t.Errorf("RefreshMinutes = %d, want 15 (from initialRefreshMinutes)", cfg.RefreshMinutes)
	}
}

// TestOpen_DoesNotReseedOnSubsequentRuns ensures a later run's
// -refresh-minutes value (or a default that differs from a
// user-customized setting) never clobbers what's already in the DB —
// seeding must only ever apply once, on table creation.
func TestOpen_DoesNotReseedOnSubsequentRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permeable.db")

	sqlDB, err := db.Open(path, 15)
	if err != nil {
		t.Fatalf("db.Open (first run): %v", err)
	}

	cfg, err := db.GetConfig(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	cfg.RefreshMinutes = 45 // simulate a user customizing it via the UI
	if err := db.UpdateConfig(context.Background(), sqlDB, cfg); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	sqlDB.Close()

	// Reopen with a *different* initialRefreshMinutes, as would happen if
	// the flag/env default is still 60 on a later restart.
	sqlDB, err = db.Open(path, 60)
	if err != nil {
		t.Fatalf("db.Open (second run): %v", err)
	}
	defer sqlDB.Close()

	cfg, err = db.GetConfig(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("GetConfig after reopen: %v", err)
	}
	if cfg.RefreshMinutes != 45 {
		t.Errorf("RefreshMinutes = %d, want 45 (user's customized value must survive reopen)", cfg.RefreshMinutes)
	}
}
