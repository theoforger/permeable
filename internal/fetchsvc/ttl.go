package fetchsvc

import (
	"fmt"
	"regexp"
	"strconv"

	ics "github.com/arran4/golang-ical"
)

// iso8601Duration matches the subset of ISO 8601 durations used by
// calendar feeds: weeks, or days/hours/minutes/seconds, e.g. "PT30M",
// "PT1H", "P1D", "P1W". Years/months are deliberately unsupported (their
// length is ambiguous and feeds don't use them for TTL/refresh hints).
var iso8601Duration = regexp.MustCompile(
	`^P(?:(\d+)W|(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?)$`)

// parseISO8601DurationMinutes parses an ISO 8601 duration string into
// whole minutes, rounding down. Returns an error if s doesn't match the
// supported subset.
func parseISO8601DurationMinutes(s string) (int, error) {
	m := iso8601Duration.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("unsupported ISO 8601 duration %q", s)
	}

	weeks := atoiOrZero(m[1])
	days := atoiOrZero(m[2])
	hours := atoiOrZero(m[3])
	minutes := atoiOrZero(m[4])
	seconds := atoiOrZero(m[5])

	total := weeks*7*24*60 + days*24*60 + hours*60 + minutes + seconds/60
	return total, nil
}

func atoiOrZero(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// sourceTTLMinutes extracts a poll-interval hint from a parsed calendar's
// X-PUBLISHED-TTL or REFRESH-INTERVAL;VALUE=DURATION property, preferring
// X-PUBLISHED-TTL when both are present. Returns nil if neither is set or
// parseable.
func sourceTTLMinutes(cal *ics.Calendar) *int {
	var xPublishedTTL, refreshInterval string
	for _, p := range cal.CalendarProperties {
		switch p.IANAToken {
		case "X-PUBLISHED-TTL":
			xPublishedTTL = p.Value
		case "REFRESH-INTERVAL":
			refreshInterval = p.Value
		}
	}

	for _, raw := range []string{xPublishedTTL, refreshInterval} {
		if raw == "" {
			continue
		}
		if minutes, err := parseISO8601DurationMinutes(raw); err == nil {
			return &minutes
		}
	}
	return nil
}
