// Package filtersvc resolves, per event/occurrence, whether it belongs in
// the published feed — see CLAUDE.md pipeline step 7 for the full
// precedence order: exceptions beat rules beat defaults beat filters.
package filtersvc

import (
	"strings"

	"permeable/internal/model"
)

// Input bundles everything Resolve/ResolveAll needs beyond the event
// list itself.
type Input struct {
	// Sources must be keyed by Source.ID (event.SourceID).
	Sources    map[int64]model.Source
	Rules      []model.EventRule      // event_rules: per-source, per-title, all future occurrences
	Exceptions []model.EventException // event_exceptions: per-source, per-title, single occurrence
	Filters    []model.Filter         // global, cross-source, field-based
	Duration   model.DurationFilter
}

// Decision is one event's resolved inclusion state, for surfaces (the
// /events page) that need to show excluded events too, not just filter
// them out.
type Decision struct {
	Event    model.Event
	Included bool
}

// Resolve returns the subset of events that should appear in the feed.
func Resolve(events []model.Event, in Input) []model.Event {
	var included []model.Event
	for _, e := range events {
		if resolveOne(e, in) {
			included = append(included, e)
		}
	}
	return included
}

// ResolveAll returns a Decision for every event, included or not — used
// by the /events page so users can find and act on excluded events too.
func ResolveAll(events []model.Event, in Input) []Decision {
	decisions := make([]Decision, len(events))
	for i, e := range events {
		decisions[i] = Decision{Event: e, Included: resolveOne(e, in)}
	}
	return decisions
}

// resolveOne applies CLAUDE.md pipeline step 7 in order:
//
//   - Step 1, source disabled: excluded, stop.
//   - Step 2, event_exceptions match (this exact occurrence): excluded, stop.
//   - Step 3, event_rules match (this title, any occurrence): the rule's
//     action, stop — this and step 2 short-circuit steps 4-6 entirely.
//   - Step 4, otherwise: source.default_include is the baseline.
//   - Step 5: a duration filter failure forces the baseline to excluded.
//   - Step 6: global filters are applied last, in list order, last match
//     wins — so an include filter can rescue an event a duration filter
//     or a default_include=false source would otherwise have excluded.
func resolveOne(e model.Event, in Input) bool {
	source, ok := in.Sources[e.SourceID]
	if !ok || !source.Enabled {
		return false
	}

	title := model.NormalizeTitle(e.Title)
	occurrenceDate := e.Start.UTC().Format("2006-01-02")

	for _, exc := range in.Exceptions {
		if exc.SourceID == e.SourceID && exc.TitleNormalized == title && exc.OccurrenceDate == occurrenceDate {
			return false
		}
	}
	for _, r := range in.Rules {
		if r.SourceID == e.SourceID && r.TitleNormalized == title {
			return r.Action == model.FilterActionInclude
		}
	}

	include := source.DefaultInclude

	if !passesDuration(e, in.Duration) {
		include = false
	}

	for _, f := range in.Filters {
		if filterMatches(e, f) {
			include = f.Type == model.FilterActionInclude
		}
	}

	return include
}

func passesDuration(e model.Event, df model.DurationFilter) bool {
	minutes := e.End.Sub(e.Start).Minutes()
	if df.MinMinutes != nil && minutes < float64(*df.MinMinutes) {
		return false
	}
	if df.MaxMinutes != nil && minutes > float64(*df.MaxMinutes) {
		return false
	}
	return true
}

func filterMatches(e model.Event, f model.Filter) bool {
	var fieldValue string
	switch f.Field {
	case model.FilterFieldTitle:
		fieldValue = e.Title
	case model.FilterFieldLocation:
		fieldValue = e.Location
	case model.FilterFieldDescription:
		fieldValue = e.Description
	default:
		return false
	}
	if f.Value == "" {
		return false
	}
	return strings.Contains(strings.ToLower(fieldValue), strings.ToLower(f.Value))
}
