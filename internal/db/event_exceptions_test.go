package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"permeable/internal/db"
	"permeable/internal/model"
)

// TestEventExceptions_OccurrenceDateRoundTrips guards against a real bug:
// occurrence_date is declared DATE in schema.sql, and modernc.org/sqlite
// sniffs that declared type and returns a time.Time on read. Scanning
// straight into a *string silently corrupted "2026-08-25" into
// "2026-08-25T00:00:00Z", which broke every exception match in
// filtersvc. This must stay a plain YYYY-MM-DD string end to end.
func TestEventExceptions_OccurrenceDateRoundTrips(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()
	ctx := context.Background()

	sourceID, err := db.CreateSource(ctx, sqlDB, model.Source{Name: "S", Type: model.SourceTypeManual, Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}

	const wantDate = "2026-08-25"
	if err := db.UpsertEventException(ctx, sqlDB, sourceID, "weekly standup", wantDate, model.FilterActionExclude); err != nil {
		t.Fatalf("UpsertEventException: %v", err)
	}

	exceptions, err := db.ListEventExceptions(ctx, sqlDB)
	if err != nil {
		t.Fatalf("ListEventExceptions: %v", err)
	}
	if len(exceptions) != 1 {
		t.Fatalf("len(exceptions) = %d, want 1", len(exceptions))
	}
	if got := exceptions[0].OccurrenceDate; got != wantDate {
		t.Errorf("OccurrenceDate = %q, want %q", got, wantDate)
	}
}

func TestEventExceptions_UpsertFlipsAction(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()
	ctx := context.Background()

	sourceID, err := db.CreateSource(ctx, sqlDB, model.Source{Name: "S", Type: model.SourceTypeManual, Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}

	if err := db.UpsertEventException(ctx, sqlDB, sourceID, "standup", "2026-08-25", model.FilterActionExclude); err != nil {
		t.Fatalf("UpsertEventException(exclude): %v", err)
	}
	exceptions, err := db.ListEventExceptions(ctx, sqlDB)
	if err != nil || len(exceptions) != 1 || exceptions[0].Action != model.FilterActionExclude {
		t.Fatalf("after first upsert: exceptions=%+v err=%v, want 1 exception with action=exclude", exceptions, err)
	}

	// Re-upserting the same (source, title, date) with a different action
	// must flip it in place, not add a second row (schema UNIQUE
	// constraint) — this is what "only include this occurrence" relies
	// on when a "hide this occurrence" already exists for the same spot.
	if err := db.UpsertEventException(ctx, sqlDB, sourceID, "standup", "2026-08-25", model.FilterActionInclude); err != nil {
		t.Fatalf("UpsertEventException(include): %v", err)
	}
	exceptions, err = db.ListEventExceptions(ctx, sqlDB)
	if err != nil {
		t.Fatalf("ListEventExceptions: %v", err)
	}
	if len(exceptions) != 1 {
		t.Fatalf("len(exceptions) = %d, want 1 (upsert should update in place)", len(exceptions))
	}
	if exceptions[0].Action != model.FilterActionInclude {
		t.Errorf("exceptions[0].Action = %q, want %q", exceptions[0].Action, model.FilterActionInclude)
	}
}

func TestEventExceptions_Delete(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()
	ctx := context.Background()

	sourceID, err := db.CreateSource(ctx, sqlDB, model.Source{Name: "S", Type: model.SourceTypeManual, Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	if err := db.UpsertEventException(ctx, sqlDB, sourceID, "standup", "2026-08-25", model.FilterActionExclude); err != nil {
		t.Fatalf("UpsertEventException: %v", err)
	}

	exceptions, err := db.ListEventExceptions(ctx, sqlDB)
	if err != nil || len(exceptions) != 1 {
		t.Fatalf("setup: ListEventExceptions() = %v, %v", exceptions, err)
	}

	if err := db.DeleteEventException(ctx, sqlDB, exceptions[0].ID); err != nil {
		t.Fatalf("DeleteEventException: %v", err)
	}

	exceptions, err = db.ListEventExceptions(ctx, sqlDB)
	if err != nil {
		t.Fatalf("ListEventExceptions: %v", err)
	}
	if len(exceptions) != 0 {
		t.Errorf("len(exceptions) = %d, want 0 after delete", len(exceptions))
	}
}
