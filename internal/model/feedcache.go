package model

import "time"

// FeedCache is the cached generated output feed plus last-run stats
// (feed_cache table, id = 1). /feed.ics always serves ICSContent as-is —
// nothing is computed on request (CLAUDE.md pipeline step 12).
type FeedCache struct {
	ICSContent            string
	GeneratedAt           time.Time
	TotalEventsPreFilter  int
	TotalEventsPostFilter int
	SourcesOK             int
	SourcesError          int
	EffectiveTTLMinutes   int
}
