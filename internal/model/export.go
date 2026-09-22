package model

import "time"

// ExportBundle is the full-config JSON snapshot produced by GET
// /config/export and consumed by POST /config/import. Source health
// fields are included for informational value on export but ignored on
// import — health is operational state, not configuration, and
// naturally repopulates on the next fetch.
type ExportBundle struct {
	Version         int              `json:"version"`
	ExportedAt      time.Time        `json:"exported_at"`
	Config          Config           `json:"config"`
	DurationFilter  DurationFilter   `json:"duration_filter"`
	Sources         []Source         `json:"sources"`
	ManualEvents    []ManualEvent    `json:"manual_events"`
	Filters         []Filter         `json:"filters"`
	EventRules      []EventRule      `json:"event_rules"`
	EventExceptions []EventException `json:"event_exceptions"`
}

// ExportBundleVersion is the current ExportBundle.Version. Bump if the
// shape changes in a way that needs import-time migration.
const ExportBundleVersion = 1
