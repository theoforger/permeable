package model

// Config is the single-row global settings (config table, id = 1).
// RefreshMinutes is how often the scheduler polls sources (Stage 9);
// MaxFeedTTLMinutes ceilings the published feed TTL (Stage 7);
// DaysBack/DaysForward bound recurrence expansion and which events are
// included at all; FeedTitle names the generated feed.
type Config struct {
	RefreshMinutes    int
	MaxFeedTTLMinutes int
	DaysBack          int
	DaysForward       int
	FeedTitle         string
}
