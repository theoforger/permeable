package fetchsvc

import (
	"testing"
	"time"

	"permeable/internal/model"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

func TestExpandEvent_Recurring(t *testing.T) {
	windowStart := mustParse(t, "2026-01-01T00:00:00Z")
	windowEnd := mustParse(t, "2026-02-01T00:00:00Z")

	e := model.Event{
		SourceID: 1,
		UID:      "weekly@example.com",
		Title:    "Weekly Standup",
		Start:    mustParse(t, "2026-01-05T09:00:00Z"), // a Monday
		End:      mustParse(t, "2026-01-05T09:30:00Z"),
		RRule:    "FREQ=WEEKLY;BYDAY=MO",
	}

	got := expandEvent(e, windowStart, windowEnd)

	// Mondays in [2026-01-01, 2026-02-01): Jan 5, 12, 19, 26.
	wantStarts := []string{
		"2026-01-05T09:00:00Z",
		"2026-01-12T09:00:00Z",
		"2026-01-19T09:00:00Z",
		"2026-01-26T09:00:00Z",
	}
	if len(got) != len(wantStarts) {
		t.Fatalf("got %d occurrences, want %d: %+v", len(got), len(wantStarts), got)
	}
	for i, occ := range got {
		if !occ.Start.Equal(mustParse(t, wantStarts[i])) {
			t.Errorf("occurrence %d: Start = %v, want %v", i, occ.Start, wantStarts[i])
		}
		wantEnd := occ.Start.Add(30 * time.Minute)
		if !occ.End.Equal(wantEnd) {
			t.Errorf("occurrence %d: End = %v, want %v (duration preserved)", i, occ.End, wantEnd)
		}
		if occ.UID != e.UID {
			t.Errorf("occurrence %d: UID = %q, want %q", i, occ.UID, e.UID)
		}
	}
}

func TestExpandEvent_ExdateExcluded(t *testing.T) {
	windowStart := mustParse(t, "2026-01-01T00:00:00Z")
	windowEnd := mustParse(t, "2026-02-01T00:00:00Z")

	e := model.Event{
		UID:     "weekly-exdate@example.com",
		Start:   mustParse(t, "2026-01-05T09:00:00Z"),
		End:     mustParse(t, "2026-01-05T09:30:00Z"),
		RRule:   "FREQ=WEEKLY;BYDAY=MO",
		ExDates: []time.Time{mustParse(t, "2026-01-12T09:00:00Z")},
	}

	got := expandEvent(e, windowStart, windowEnd)

	for _, occ := range got {
		if occ.Start.Equal(mustParse(t, "2026-01-12T09:00:00Z")) {
			t.Errorf("EXDATE occurrence 2026-01-12 was not excluded: %+v", got)
		}
	}
	if len(got) != 3 { // 4 Mondays minus 1 excluded
		t.Errorf("got %d occurrences, want 3", len(got))
	}
}

func TestExpandEvent_NonRecurringWindowBounds(t *testing.T) {
	windowStart := mustParse(t, "2026-01-01T00:00:00Z")
	windowEnd := mustParse(t, "2026-02-01T00:00:00Z")

	cases := []struct {
		name  string
		start string
		want  bool
	}{
		{"inside window", "2026-01-15T09:00:00Z", true},
		{"before window", "2025-12-01T09:00:00Z", false},
		{"after window", "2026-03-01T09:00:00Z", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := mustParse(t, tc.start)
			e := model.Event{UID: "single@example.com", Start: start, End: start.Add(time.Hour)}
			got := expandEvent(e, windowStart, windowEnd)
			gotIncluded := len(got) == 1
			if gotIncluded != tc.want {
				t.Errorf("expandEvent() included = %v, want %v", gotIncluded, tc.want)
			}
		})
	}
}

func TestDedupe_KeepsDistinctOccurrencesSameUID(t *testing.T) {
	// Two occurrences of the same recurring UID (different start times)
	// must both survive — dedupe is keyed on (UID, start), not UID alone.
	events := []model.Event{
		{UID: "weekly@example.com", Start: mustParse(t, "2026-01-05T09:00:00Z")},
		{UID: "weekly@example.com", Start: mustParse(t, "2026-01-12T09:00:00Z")},
	}
	got := dedupe(events)
	if len(got) != 2 {
		t.Fatalf("dedupe() = %d events, want 2 (distinct occurrences of the same series)", len(got))
	}
}

func TestDedupe_DropsCrossSourceDuplicate(t *testing.T) {
	// Same UID + same start from two different sources (e.g. two people
	// subscribed to the same public calendar) collapses to one.
	events := []model.Event{
		{SourceID: 1, UID: "dup@example.com", Start: mustParse(t, "2026-01-05T09:00:00Z")},
		{SourceID: 2, UID: "dup@example.com", Start: mustParse(t, "2026-01-05T09:00:00Z")},
	}
	got := dedupe(events)
	if len(got) != 1 {
		t.Fatalf("dedupe() = %d events, want 1 (cross-source duplicate collapsed)", len(got))
	}
}

func TestExpandAll_RecurringPlusCrossSourceDuplicate(t *testing.T) {
	windowStart := mustParse(t, "2026-01-01T00:00:00Z")
	windowEnd := mustParse(t, "2026-02-01T00:00:00Z")

	events := []model.Event{
		// A recurring event from source 1.
		{
			SourceID: 1, UID: "weekly@example.com",
			Start: mustParse(t, "2026-01-05T09:00:00Z"), End: mustParse(t, "2026-01-05T09:30:00Z"),
			RRule: "FREQ=WEEKLY;BYDAY=MO",
		},
		// The exact same one-off event duplicated across two sources.
		{SourceID: 1, UID: "dup@example.com", Start: mustParse(t, "2026-01-10T12:00:00Z"), End: mustParse(t, "2026-01-10T13:00:00Z")},
		{SourceID: 2, UID: "dup@example.com", Start: mustParse(t, "2026-01-10T12:00:00Z"), End: mustParse(t, "2026-01-10T13:00:00Z")},
	}

	got := expandAll(events, windowStart, windowEnd)

	// 4 weekly occurrences + 1 deduped one-off = 5.
	if len(got) != 5 {
		t.Fatalf("expandAll() = %d events, want 5: %+v", len(got), got)
	}
}
