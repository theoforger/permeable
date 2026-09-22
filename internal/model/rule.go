package model

import (
	"strings"
	"time"
)

// NormalizeTitle canonicalizes an event title for rule/exception/filter
// matching: trimmed and case-folded, so "Team Standup" and "team standup"
// are the same rule target.
func NormalizeTitle(title string) string {
	return strings.ToLower(strings.TrimSpace(title))
}

// EventRule is a per-source, per-title include/exclude applied to all
// future occurrences ("hide all future like this" / "always show").
type EventRule struct {
	ID              int64
	SourceID        int64
	TitleNormalized string
	Action          FilterAction
	CreatedAt       time.Time
}

// EventException is a per-source, per-title, per-date include/exclude
// override for a single occurrence ("hide just this one" / "only include
// this one"). Takes precedence over EventRule.
type EventException struct {
	ID              int64
	SourceID        int64
	TitleNormalized string
	OccurrenceDate  string // YYYY-MM-DD
	Action          FilterAction
	CreatedAt       time.Time
}
