// Package exportsvc reads/writes the full config (sources, manual_events,
// filters, duration_filters, event_rules, event_exceptions, config) as
// one JSON snapshot — GET /config/export and POST /config/import.
package exportsvc

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"permeable/internal/db"
	"permeable/internal/model"
)

// Mode is the import strategy chosen in the UI.
type Mode string

const (
	// ModeReplace clears sources/filters/config/duration_filters first,
	// then restores the snapshot exactly, preserving source ids so
	// manual_events/event_rules/event_exceptions references stay valid.
	ModeReplace Mode = "replace"
	// ModeMerge adds the snapshot's sources/filters alongside whatever
	// already exists. Sources get newly assigned ids (remapped
	// automatically); config/duration_filters are left untouched, since
	// there's no sensible way to "merge" a singleton row. Filters are
	// appended as-is — re-merging the same export twice will duplicate
	// them.
	ModeMerge Mode = "merge"
)

// Stats summarizes what Import restored, for the confirmation message.
type Stats struct {
	Sources         int
	ManualEvents    int
	Filters         int
	EventRules      int
	EventExceptions int
}

// Export reads the full current config into one snapshot.
func Export(ctx context.Context, sqlDB *sql.DB) (model.ExportBundle, error) {
	cfg, err := db.GetConfig(ctx, sqlDB)
	if err != nil {
		return model.ExportBundle{}, fmt.Errorf("get config: %w", err)
	}
	df, err := db.GetDurationFilters(ctx, sqlDB)
	if err != nil {
		return model.ExportBundle{}, fmt.Errorf("get duration filters: %w", err)
	}
	sources, err := db.ListSources(ctx, sqlDB)
	if err != nil {
		return model.ExportBundle{}, fmt.Errorf("list sources: %w", err)
	}
	manualEvents, err := db.ListAllManualEvents(ctx, sqlDB)
	if err != nil {
		return model.ExportBundle{}, fmt.Errorf("list manual events: %w", err)
	}
	filters, err := db.ListFilters(ctx, sqlDB)
	if err != nil {
		return model.ExportBundle{}, fmt.Errorf("list filters: %w", err)
	}
	rules, err := db.ListEventRules(ctx, sqlDB)
	if err != nil {
		return model.ExportBundle{}, fmt.Errorf("list event rules: %w", err)
	}
	exceptions, err := db.ListEventExceptions(ctx, sqlDB)
	if err != nil {
		return model.ExportBundle{}, fmt.Errorf("list event exceptions: %w", err)
	}

	return model.ExportBundle{
		Version:         model.ExportBundleVersion,
		ExportedAt:      time.Now(),
		Config:          cfg,
		DurationFilter:  df,
		Sources:         orEmpty(sources),
		ManualEvents:    orEmpty(manualEvents),
		Filters:         orEmpty(filters),
		EventRules:      orEmpty(rules),
		EventExceptions: orEmpty(exceptions),
	}, nil
}

// orEmpty turns a nil slice into an empty (non-null) one, so exported
// JSON has "[]" instead of "null" for empty collections.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Import restores a snapshot per mode. Not wrapped in a single DB
// transaction (the db package's functions operate on *sql.DB, not a
// shared *sql.Tx) — a failure partway through can leave a partially
// imported state; Stats reflects only what completed before any error.
func Import(ctx context.Context, sqlDB *sql.DB, bundle model.ExportBundle, mode Mode) (Stats, error) {
	switch mode {
	case ModeReplace:
		return importReplace(ctx, sqlDB, bundle)
	case ModeMerge:
		return importMerge(ctx, sqlDB, bundle)
	default:
		return Stats{}, fmt.Errorf("unknown import mode %q", mode)
	}
}

func importReplace(ctx context.Context, sqlDB *sql.DB, bundle model.ExportBundle) (Stats, error) {
	var stats Stats

	if err := db.DeleteAllSources(ctx, sqlDB); err != nil {
		return stats, err
	}
	if err := db.DeleteAllFilters(ctx, sqlDB); err != nil {
		return stats, err
	}

	for _, s := range bundle.Sources {
		if err := db.ImportSourceWithID(ctx, sqlDB, s); err != nil {
			return stats, err
		}
		stats.Sources++
	}
	if err := importChildRows(ctx, sqlDB, bundle, sourceIDIdentity, &stats); err != nil {
		return stats, err
	}

	if err := db.UpdateConfig(ctx, sqlDB, bundle.Config); err != nil {
		return stats, fmt.Errorf("update config: %w", err)
	}
	if err := db.UpdateDurationFilters(ctx, sqlDB, bundle.DurationFilter); err != nil {
		return stats, fmt.Errorf("update duration filters: %w", err)
	}

	return stats, nil
}

func importMerge(ctx context.Context, sqlDB *sql.DB, bundle model.ExportBundle) (Stats, error) {
	var stats Stats

	idMap := make(map[int64]int64, len(bundle.Sources))
	for _, s := range bundle.Sources {
		newID, err := db.CreateSource(ctx, sqlDB, s)
		if err != nil {
			return stats, err
		}
		idMap[s.ID] = newID
		stats.Sources++
	}
	remap := func(oldSourceID int64) (int64, bool) {
		newID, ok := idMap[oldSourceID]
		return newID, ok
	}
	if err := importChildRows(ctx, sqlDB, bundle, remap, &stats); err != nil {
		return stats, err
	}

	// config/duration_filters intentionally left untouched in merge mode.
	return stats, nil
}

// sourceIDIdentity is the "no remapping" identity function used by
// replace mode, where source ids are preserved as-is.
func sourceIDIdentity(sourceID int64) (int64, bool) { return sourceID, true }

// importChildRows restores manual_events/event_rules/event_exceptions,
// mapping each row's source_id through resolveSourceID (identity for
// replace, old->new lookup for merge). Rows whose source wasn't part of
// this import (shouldn't happen for a well-formed export) are skipped.
func importChildRows(ctx context.Context, sqlDB *sql.DB, bundle model.ExportBundle, resolveSourceID func(int64) (int64, bool), stats *Stats) error {
	for _, me := range bundle.ManualEvents {
		sourceID, ok := resolveSourceID(me.SourceID)
		if !ok {
			continue
		}
		if err := db.UpsertManualEvent(ctx, sqlDB, sourceID, me.UID, me.RawVEvent); err != nil {
			return err
		}
		stats.ManualEvents++
	}

	for _, r := range bundle.EventRules {
		sourceID, ok := resolveSourceID(r.SourceID)
		if !ok {
			continue
		}
		if err := db.UpsertEventRule(ctx, sqlDB, sourceID, r.TitleNormalized, r.Action); err != nil {
			return err
		}
		stats.EventRules++
	}

	for _, e := range bundle.EventExceptions {
		sourceID, ok := resolveSourceID(e.SourceID)
		if !ok {
			continue
		}
		if err := db.CreateEventException(ctx, sqlDB, sourceID, e.TitleNormalized, e.OccurrenceDate); err != nil {
			return err
		}
		stats.EventExceptions++
	}

	for _, f := range bundle.Filters {
		if _, err := db.CreateFilter(ctx, sqlDB, f); err != nil {
			return err
		}
		stats.Filters++
	}

	return nil
}
