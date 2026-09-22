package model

// FilterField names which event field a global Filter matches against.
type FilterField string

const (
	FilterFieldTitle       FilterField = "title"
	FilterFieldLocation    FilterField = "location"
	FilterFieldDescription FilterField = "description"
)

// FilterAction is what a matching Filter (or event rule, Stage 6) does.
type FilterAction string

const (
	FilterActionInclude FilterAction = "include"
	FilterActionExclude FilterAction = "exclude"
)

// Filter is one global, cross-source, field-based include/exclude rule.
// Value matches as a case-insensitive substring of the field.
type Filter struct {
	ID    int64
	Field FilterField
	Type  FilterAction
	Value string
}

// DurationFilter is the single global min/max event length setting
// (duration_filters table, id = 1). Nil bounds mean "no limit".
type DurationFilter struct {
	MinMinutes *int
	MaxMinutes *int
}
