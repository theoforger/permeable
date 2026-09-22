package model

import "time"

// Event is a normalized calendar event, parsed from either a fetched URL
// source or a manual source's stored VEVENTs. It represents a single
// VEVENT as found in the source — RRULE is carried as its raw value for
// fetchsvc's expansion in Stage 4, not yet expanded into occurrences.
type Event struct {
	SourceID int64
	UID      string

	Title       string
	Description string
	Location    string

	Start  time.Time
	End    time.Time
	AllDay bool

	// RRule is the raw RRULE property value (e.g. "FREQ=WEEKLY;BYDAY=MO"),
	// empty if the event does not recur. Expanded into occurrences by
	// fetchsvc/filtersvc in Stage 4.
	RRule string
	// ExDates are EXDATE occurrences to exclude from an expanded RRULE.
	ExDates []time.Time
}
