// Package model holds the plain data types shared across permeable's
// packages (Event, Source, FilterRule, etc.) — no business logic, no DB
// access, just shapes.
package model

import "time"

// SourceType distinguishes a fetched ICS URL source from a manually
// curated one (events added via file upload, no fetch step).
type SourceType string

const (
	SourceTypeURL    SourceType = "url"
	SourceTypeManual SourceType = "manual"
)

// Source is one calendar source, either polled from a URL or fed manual
// events via upload. Health fields are populated by fetchsvc and are zero
// values until the first fetch run.
type Source struct {
	ID             int64
	Name           string
	Type           SourceType
	URL            string // empty for manual sources
	Enabled        bool
	DefaultInclude bool
	TitlePrefix    string // empty means no prefix

	LastFetchAt      *time.Time
	LastFetchStatus  string // "" | "ok" | "error"
	LastFetchError   string
	LastEventCount   *int
	SourceTTLMinutes *int // parsed from X-PUBLISHED-TTL / REFRESH-INTERVAL; url sources only

	CreatedAt time.Time
}

// ManualEvent is one VEVENT attached directly to a manual source via file
// upload.
type ManualEvent struct {
	ID        int64
	SourceID  int64
	UID       string
	RawVEvent string
	AddedAt   time.Time
}
