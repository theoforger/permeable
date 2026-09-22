package fetchsvc

import (
	"log"
	"time"

	"github.com/teambition/rrule-go"

	"permeable/internal/model"
)

// expandEvent turns one parsed model.Event into its concrete occurrences
// within [windowStart, windowEnd]. Non-recurring events pass through as a
// single occurrence if they overlap the window at all; recurring events
// are expanded via rrule-go, with EXDATE occurrences removed.
//
// Known v1 gap (see CLAUDE.md pipeline step 5): RECURRENCE-ID overrides
// for a single moved/modified occurrence are not applied — a rescheduled
// instance may still show at its original time.
func expandEvent(e model.Event, windowStart, windowEnd time.Time) []model.Event {
	duration := e.End.Sub(e.Start)

	if e.RRule == "" {
		if e.End.Before(windowStart) || e.Start.After(windowEnd) {
			return nil
		}
		return []model.Event{e}
	}

	opt, err := rrule.StrToROption(e.RRule)
	if err != nil {
		log.Printf("fetchsvc: skipping event %s: unparseable RRULE %q: %v", e.UID, e.RRule, err)
		return nil
	}
	opt.Dtstart = e.Start

	rr, err := rrule.NewRRule(*opt)
	if err != nil {
		log.Printf("fetchsvc: skipping event %s: invalid RRULE %q: %v", e.UID, e.RRule, err)
		return nil
	}

	excluded := make(map[int64]bool, len(e.ExDates))
	for _, d := range e.ExDates {
		excluded[d.UTC().Unix()] = true
	}

	starts := rr.Between(windowStart, windowEnd, true)
	occurrences := make([]model.Event, 0, len(starts))
	for _, start := range starts {
		if excluded[start.UTC().Unix()] {
			continue
		}
		occ := e
		occ.Start = start
		occ.End = start.Add(duration)
		occurrences = append(occurrences, occ)
	}
	return occurrences
}

// expandAll expands every event against the window and merges the result
// across sources, deduping exact-duplicate occurrences.
func expandAll(events []model.Event, windowStart, windowEnd time.Time) []model.Event {
	var expanded []model.Event
	for _, e := range events {
		expanded = append(expanded, expandEvent(e, windowStart, windowEnd)...)
	}
	return dedupe(expanded)
}

// dedupe drops duplicate occurrences across sources. Keyed on (UID,
// start): UID alone would wrongly collapse a recurring event's distinct
// occurrences into one, since they legitimately share a UID — see
// CLAUDE.md pipeline step 6 ("dedupe on UID") read together with step 5's
// RECURRENCE-ID gap note; (UID, start) is the correct v1 approximation of
// "same occurrence" until RECURRENCE-ID is handled (Stage 12).
func dedupe(events []model.Event) []model.Event {
	type key struct {
		uid   string
		start int64
	}
	seen := make(map[key]bool, len(events))
	result := make([]model.Event, 0, len(events))
	for _, e := range events {
		k := key{uid: e.UID, start: e.Start.UTC().Unix()}
		if seen[k] {
			continue
		}
		seen[k] = true
		result = append(result, e)
	}
	return result
}
