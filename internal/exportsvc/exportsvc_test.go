package exportsvc_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"permeable/internal/db"
	"permeable/internal/exportsvc"
	"permeable/internal/model"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

// seededSourceIDs names the sources seedFullConfig creates, for
// assertions keyed by role rather than by numeric id.
type seededSourceIDs struct {
	url    int64
	manual int64
}

// seedFullConfig populates every table exportsvc.Export reads: one url
// source, one manual source with two events, a global filter, a duration
// filter, an event rule, an event exception, and customized config.
func seedFullConfig(t *testing.T, ctx context.Context, sqlDB *sql.DB) seededSourceIDs {
	t.Helper()

	urlSourceID, err := db.CreateSource(ctx, sqlDB, model.Source{
		Name: "Work Calendar", Type: model.SourceTypeURL, URL: "https://example.com/work.ics",
		Enabled: true, DefaultInclude: true, TitlePrefix: "[Work]",
	})
	if err != nil {
		t.Fatalf("CreateSource(url): %v", err)
	}

	manualSourceID, err := db.CreateSource(ctx, sqlDB, model.Source{
		Name: "Personal", Type: model.SourceTypeManual, Enabled: true, DefaultInclude: false,
	})
	if err != nil {
		t.Fatalf("CreateSource(manual): %v", err)
	}

	if err := db.UpsertManualEvent(ctx, sqlDB, manualSourceID, "dentist@example.com",
		"BEGIN:VEVENT\r\nUID:dentist@example.com\r\nDTSTAMP:20260101T000000Z\r\n"+
			"DTSTART:20260901T140000Z\r\nDTEND:20260901T150000Z\r\nSUMMARY:Dentist\r\nEND:VEVENT"); err != nil {
		t.Fatalf("UpsertManualEvent(dentist): %v", err)
	}
	if err := db.UpsertManualEvent(ctx, sqlDB, manualSourceID, "birthday@example.com",
		"BEGIN:VEVENT\r\nUID:birthday@example.com\r\nDTSTAMP:20260101T000000Z\r\n"+
			"DTSTART:20260902T000000Z;VALUE=DATE\r\nSUMMARY:Birthday\r\nEND:VEVENT"); err != nil {
		t.Fatalf("UpsertManualEvent(birthday): %v", err)
	}

	if _, err := db.CreateFilter(ctx, sqlDB, model.Filter{
		Field: model.FilterFieldTitle, Type: model.FilterActionExclude, Value: "lunch",
	}); err != nil {
		t.Fatalf("CreateFilter: %v", err)
	}

	minM, maxM := 5, 240
	if err := db.UpdateDurationFilters(ctx, sqlDB, model.DurationFilter{MinMinutes: &minM, MaxMinutes: &maxM}); err != nil {
		t.Fatalf("UpdateDurationFilters: %v", err)
	}

	if err := db.UpsertEventRule(ctx, sqlDB, urlSourceID, "standup", model.FilterActionExclude); err != nil {
		t.Fatalf("UpsertEventRule: %v", err)
	}
	if err := db.UpsertEventException(ctx, sqlDB, manualSourceID, "dentist", "2026-09-01", model.FilterActionExclude); err != nil {
		t.Fatalf("UpsertEventException: %v", err)
	}

	cfg, err := db.GetConfig(ctx, sqlDB)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	cfg.FeedTitle = "My Custom Feed"
	cfg.RefreshMinutes = 42
	cfg.MaxFeedTTLMinutes = 33
	cfg.DaysBack = 3
	cfg.DaysForward = 45
	if err := db.UpdateConfig(ctx, sqlDB, cfg); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	return seededSourceIDs{url: urlSourceID, manual: manualSourceID}
}

// TestExportImport_ReplaceIntoFreshDBReproducesOriginalState is Stage
// 10's own done-criteria, verbatim.
func TestExportImport_ReplaceIntoFreshDBReproducesOriginalState(t *testing.T) {
	ctx := context.Background()

	source := openTestDB(t)
	seedFullConfig(t, ctx, source)

	bundle, err := exportsvc.Export(ctx, source)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	fresh := openTestDB(t)
	stats, err := exportsvc.Import(ctx, fresh, bundle, exportsvc.ModeReplace)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if stats.Sources != 2 || stats.ManualEvents != 2 || stats.Filters != 1 || stats.EventRules != 1 || stats.EventExceptions != 1 {
		t.Errorf("Stats = %+v, want {Sources:2 ManualEvents:2 Filters:1 EventRules:1 EventExceptions:1}", stats)
	}

	// Sources: same ids preserved (replace mode), same core fields.
	wantSources, err := db.ListSources(ctx, source)
	if err != nil {
		t.Fatalf("ListSources(source): %v", err)
	}
	gotSources, err := db.ListSources(ctx, fresh)
	if err != nil {
		t.Fatalf("ListSources(fresh): %v", err)
	}
	if len(gotSources) != len(wantSources) {
		t.Fatalf("len(gotSources) = %d, want %d", len(gotSources), len(wantSources))
	}
	for i := range wantSources {
		w, g := wantSources[i], gotSources[i]
		if g.ID != w.ID || g.Name != w.Name || g.Type != w.Type || g.URL != w.URL ||
			g.Enabled != w.Enabled || g.DefaultInclude != w.DefaultInclude || g.TitlePrefix != w.TitlePrefix {
			t.Errorf("source %d mismatch:\n  want %+v\n  got  %+v", i, w, g)
		}
	}

	// Manual events: same uid/raw_vevent, still attached to the same
	// (preserved) source id.
	wantEvents, err := db.ListAllManualEvents(ctx, source)
	if err != nil {
		t.Fatalf("ListAllManualEvents(source): %v", err)
	}
	gotEvents, err := db.ListAllManualEvents(ctx, fresh)
	if err != nil {
		t.Fatalf("ListAllManualEvents(fresh): %v", err)
	}
	if len(gotEvents) != len(wantEvents) {
		t.Fatalf("len(gotEvents) = %d, want %d", len(gotEvents), len(wantEvents))
	}
	for i := range wantEvents {
		w, g := wantEvents[i], gotEvents[i]
		if g.SourceID != w.SourceID || g.UID != w.UID || g.RawVEvent != w.RawVEvent {
			t.Errorf("manual event %d mismatch:\n  want %+v\n  got  %+v", i, w, g)
		}
	}

	// Filters.
	wantFilters, _ := db.ListFilters(ctx, source)
	gotFilters, _ := db.ListFilters(ctx, fresh)
	if len(gotFilters) != 1 || len(wantFilters) != 1 {
		t.Fatalf("filters: got %d, want %d (both expected 1)", len(gotFilters), len(wantFilters))
	}
	if gotFilters[0].Field != wantFilters[0].Field || gotFilters[0].Type != wantFilters[0].Type || gotFilters[0].Value != wantFilters[0].Value {
		t.Errorf("filter mismatch: want %+v, got %+v", wantFilters[0], gotFilters[0])
	}

	// Duration filter.
	wantDF, _ := db.GetDurationFilters(ctx, source)
	gotDF, _ := db.GetDurationFilters(ctx, fresh)
	if *gotDF.MinMinutes != *wantDF.MinMinutes || *gotDF.MaxMinutes != *wantDF.MaxMinutes {
		t.Errorf("duration filter mismatch: want %+v, got %+v", wantDF, gotDF)
	}

	// Event rules.
	wantRules, _ := db.ListEventRules(ctx, source)
	gotRules, _ := db.ListEventRules(ctx, fresh)
	if len(gotRules) != 1 || len(wantRules) != 1 {
		t.Fatalf("event rules: got %d, want %d (both expected 1)", len(gotRules), len(wantRules))
	}
	if gotRules[0].SourceID != wantRules[0].SourceID || gotRules[0].TitleNormalized != wantRules[0].TitleNormalized || gotRules[0].Action != wantRules[0].Action {
		t.Errorf("event rule mismatch: want %+v, got %+v", wantRules[0], gotRules[0])
	}

	// Event exceptions.
	wantExc, _ := db.ListEventExceptions(ctx, source)
	gotExc, _ := db.ListEventExceptions(ctx, fresh)
	if len(gotExc) != 1 || len(wantExc) != 1 {
		t.Fatalf("event exceptions: got %d, want %d (both expected 1)", len(gotExc), len(wantExc))
	}
	if gotExc[0].SourceID != wantExc[0].SourceID || gotExc[0].TitleNormalized != wantExc[0].TitleNormalized ||
		gotExc[0].OccurrenceDate != wantExc[0].OccurrenceDate || gotExc[0].Action != wantExc[0].Action {
		t.Errorf("event exception mismatch: want %+v, got %+v", wantExc[0], gotExc[0])
	}

	// Config.
	wantCfg, _ := db.GetConfig(ctx, source)
	gotCfg, _ := db.GetConfig(ctx, fresh)
	if gotCfg != wantCfg {
		t.Errorf("config mismatch: want %+v, got %+v", wantCfg, gotCfg)
	}
}

// TestExportImport_MergeRemapsSourceIDsAndKeepsExistingConfig verifies
// merge mode: existing data survives, imported sources get new ids with
// their children correctly remapped, and config/duration_filters are not
// clobbered by the imported values.
func TestExportImport_MergeRemapsSourceIDsAndKeepsExistingConfig(t *testing.T) {
	ctx := context.Background()

	source := openTestDB(t)
	seedFullConfig(t, ctx, source)
	bundle, err := exportsvc.Export(ctx, source)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	target := openTestDB(t)
	existingID, err := db.CreateSource(ctx, target, model.Source{
		Name: "Pre-existing", Type: model.SourceTypeManual, Enabled: true, DefaultInclude: true,
	})
	if err != nil {
		t.Fatalf("CreateSource(pre-existing): %v", err)
	}
	targetCfg, err := db.GetConfig(ctx, target)
	if err != nil {
		t.Fatalf("GetConfig(target, before): %v", err)
	}
	targetCfg.FeedTitle = "Target's Own Title"
	if err := db.UpdateConfig(ctx, target, targetCfg); err != nil {
		t.Fatalf("UpdateConfig(target): %v", err)
	}

	stats, err := exportsvc.Import(ctx, target, bundle, exportsvc.ModeMerge)
	if err != nil {
		t.Fatalf("Import(merge): %v", err)
	}
	if stats.Sources != 2 {
		t.Errorf("stats.Sources = %d, want 2", stats.Sources)
	}

	gotSources, err := db.ListSources(ctx, target)
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	if len(gotSources) != 3 {
		t.Fatalf("len(gotSources) = %d, want 3 (1 pre-existing + 2 merged)", len(gotSources))
	}

	// The pre-existing source must be untouched, at its original id.
	found := false
	for _, s := range gotSources {
		if s.ID == existingID {
			found = true
			if s.Name != "Pre-existing" {
				t.Errorf("pre-existing source was modified: %+v", s)
			}
		}
	}
	if !found {
		t.Error("pre-existing source is gone after merge")
	}

	// Every merged source must have a *new* id (none can collide with
	// the pre-existing one), and none should equal their original
	// exported id unless by sheer coincidence — check via manual events
	// instead, which is unambiguous: they must be attached to a source
	// that actually exists in the target DB post-merge.
	gotEvents, err := db.ListAllManualEvents(ctx, target)
	if err != nil {
		t.Fatalf("ListAllManualEvents: %v", err)
	}
	if len(gotEvents) != 2 {
		t.Fatalf("len(gotEvents) = %d, want 2", len(gotEvents))
	}
	sourceIDs := make(map[int64]bool, len(gotSources))
	for _, s := range gotSources {
		sourceIDs[s.ID] = true
	}
	for _, me := range gotEvents {
		if !sourceIDs[me.SourceID] {
			t.Errorf("manual event %s references source_id %d which doesn't exist in target DB", me.UID, me.SourceID)
		}
		if me.SourceID == existingID {
			t.Errorf("manual event %s got remapped onto the pre-existing source, not its own imported source", me.UID)
		}
	}

	// config/duration_filters must NOT be touched by a merge import.
	gotCfg, err := db.GetConfig(ctx, target)
	if err != nil {
		t.Fatalf("GetConfig(target, after): %v", err)
	}
	if gotCfg.FeedTitle != "Target's Own Title" {
		t.Errorf("FeedTitle = %q, want target's own %q (merge must not overwrite config)", gotCfg.FeedTitle, "Target's Own Title")
	}
}

// TestExportImport_EmptyDBRoundTrips guards the trivial case: exporting
// an empty DB and importing it must not error.
func TestExportImport_EmptyDBRoundTrips(t *testing.T) {
	ctx := context.Background()

	source := openTestDB(t)
	bundle, err := exportsvc.Export(ctx, source)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if bundle.Version != model.ExportBundleVersion {
		t.Errorf("Version = %d, want %d", bundle.Version, model.ExportBundleVersion)
	}

	fresh := openTestDB(t)
	if _, err := exportsvc.Import(ctx, fresh, bundle, exportsvc.ModeReplace); err != nil {
		t.Fatalf("Import: %v", err)
	}
}
