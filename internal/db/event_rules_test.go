package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"permeable/internal/db"
	"permeable/internal/model"
)

func TestEventRules_UpsertFlipsAction(t *testing.T) {
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

	if err := db.UpsertEventRule(ctx, sqlDB, sourceID, "standup", model.FilterActionExclude); err != nil {
		t.Fatalf("UpsertEventRule(exclude): %v", err)
	}
	rules, err := db.ListEventRules(ctx, sqlDB)
	if err != nil || len(rules) != 1 || rules[0].Action != model.FilterActionExclude {
		t.Fatalf("after first upsert: rules=%+v err=%v, want 1 rule with action=exclude", rules, err)
	}

	// Re-upserting the same (source, title) with a different action must
	// flip it in place, not add a second row (schema UNIQUE constraint).
	if err := db.UpsertEventRule(ctx, sqlDB, sourceID, "standup", model.FilterActionInclude); err != nil {
		t.Fatalf("UpsertEventRule(include): %v", err)
	}
	rules, err = db.ListEventRules(ctx, sqlDB)
	if err != nil {
		t.Fatalf("ListEventRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("len(rules) = %d, want 1 (upsert should update in place)", len(rules))
	}
	if rules[0].Action != model.FilterActionInclude {
		t.Errorf("rules[0].Action = %q, want %q", rules[0].Action, model.FilterActionInclude)
	}
}

func TestEventRules_Delete(t *testing.T) {
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
	if err := db.UpsertEventRule(ctx, sqlDB, sourceID, "standup", model.FilterActionExclude); err != nil {
		t.Fatalf("UpsertEventRule: %v", err)
	}

	rules, err := db.ListEventRules(ctx, sqlDB)
	if err != nil || len(rules) != 1 {
		t.Fatalf("setup: ListEventRules() = %v, %v", rules, err)
	}

	if err := db.DeleteEventRule(ctx, sqlDB, rules[0].ID); err != nil {
		t.Fatalf("DeleteEventRule: %v", err)
	}

	rules, err = db.ListEventRules(ctx, sqlDB)
	if err != nil {
		t.Fatalf("ListEventRules: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("len(rules) = %d, want 0 after delete", len(rules))
	}
}
