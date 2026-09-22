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

// EventException excludes a single occurrence ("hide just this one"),
// identified by source, normalized title, and the occurrence's date.
// Takes precedence over EventRule.
type EventException struct {
	ID              int64
	SourceID        int64
	TitleNormalized string
	OccurrenceDate  string // YYYY-MM-DD
	CreatedAt       time.Time
}
