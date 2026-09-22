package filtersvc_test

import (
	"testing"
	"time"

	"permeable/internal/filtersvc"
	"permeable/internal/model"
)

func newEvent(sourceID int64, uid, title, location, description string, minutes int) model.Event {
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	return model.Event{
		SourceID: sourceID, UID: uid, Title: title, Location: location, Description: description,
		Start: start, End: start.Add(time.Duration(minutes) * time.Minute),
	}
}

func TestResolve_SourceDisabledExcluded(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: false, DefaultInclude: true}},
	}
	events := []model.Event{newEvent(1, "a", "Standup", "", "", 30)}

	got := filtersvc.Resolve(events, in)
	if len(got) != 0 {
		t.Errorf("Resolve() = %d events, want 0 (source disabled)", len(got))
	}
}

func TestResolve_DefaultIncludeBaseline(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{
			1: {ID: 1, Enabled: true, DefaultInclude: true},
			2: {ID: 2, Enabled: true, DefaultInclude: false},
		},
	}
	events := []model.Event{
		newEvent(1, "included-by-default", "Standup", "", "", 30),
		newEvent(2, "excluded-by-default", "Standup", "", "", 30),
	}

	got := filtersvc.Resolve(events, in)
	if len(got) != 1 || got[0].UID != "included-by-default" {
		t.Errorf("Resolve() = %+v, want only the default_include=true source's event", got)
	}
}

func TestResolve_DurationFilterExcludes(t *testing.T) {
	in := filtersvc.Input{
		Sources:  map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: true}},
		Duration: model.DurationFilter{MinMinutes: ptr(5), MaxMinutes: ptr(120)},
	}
	events := []model.Event{
		newEvent(1, "too-short", "Reminder", "", "", 2),
		newEvent(1, "too-long", "All Day Retreat", "", "", 600),
		newEvent(1, "just-right", "Standup", "", "", 30),
	}

	got := filtersvc.Resolve(events, in)
	if len(got) != 1 || got[0].UID != "just-right" {
		t.Errorf("Resolve() = %+v, want only 'just-right'", got)
	}
}

func TestResolve_GlobalExcludeFilter(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: true}},
		Filters: []model.Filter{{Field: model.FilterFieldTitle, Type: model.FilterActionExclude, Value: "lunch"}},
	}
	events := []model.Event{
		newEvent(1, "keep", "Team Standup", "", "", 30),
		newEvent(1, "drop", "Lunch Break", "", "", 30),
	}

	got := filtersvc.Resolve(events, in)
	if len(got) != 1 || got[0].UID != "keep" {
		t.Errorf("Resolve() = %+v, want only 'keep'", got)
	}
}

func TestResolve_GlobalIncludeFilterRescuesDefaultExcludedSource(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: false}},
		Filters: []model.Filter{{Field: model.FilterFieldTitle, Type: model.FilterActionInclude, Value: "urgent"}},
	}
	events := []model.Event{
		newEvent(1, "rescued", "URGENT: On-call handoff", "", "", 30),
		newEvent(1, "still-excluded", "Random FYI", "", "", 30),
	}

	got := filtersvc.Resolve(events, in)
	if len(got) != 1 || got[0].UID != "rescued" {
		t.Errorf("Resolve() = %+v, want only 'rescued'", got)
	}
}

func TestResolve_LastMatchingFilterWins(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: true}},
		Filters: []model.Filter{
			{Field: model.FilterFieldTitle, Type: model.FilterActionExclude, Value: "lunch"},
			{Field: model.FilterFieldTitle, Type: model.FilterActionInclude, Value: "standup"}, // added later, wins
		},
	}
	events := []model.Event{newEvent(1, "e", "Team Lunch Standup", "", "", 30)}

	got := filtersvc.Resolve(events, in)
	if len(got) != 1 {
		t.Errorf("Resolve() = %+v, want the event included (later filter wins)", got)
	}
}

func TestResolve_FilterOnLocationAndDescription(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: true}},
		Filters: []model.Filter{
			{Field: model.FilterFieldLocation, Type: model.FilterActionExclude, Value: "remote"},
			{Field: model.FilterFieldDescription, Type: model.FilterActionExclude, Value: "confidential"},
		},
	}
	events := []model.Event{
		newEvent(1, "by-location", "Meeting", "Remote", "", 30),
		newEvent(1, "by-description", "Meeting", "", "confidential", 30),
		newEvent(1, "neither", "Meeting", "Office", "public", 30),
	}

	got := filtersvc.Resolve(events, in)
	if len(got) != 1 || got[0].UID != "neither" {
		t.Errorf("Resolve() = %+v, want only 'neither'", got)
	}
}

// --- Stage 6: exceptions beat rules beat defaults beat filters ---

func TestResolve_ExceptionBeatsEverything(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: true}},
		Exceptions: []model.EventException{
			{SourceID: 1, TitleNormalized: "standup", OccurrenceDate: "2026-01-05", Action: model.FilterActionExclude},
		},
		Rules:   []model.EventRule{{SourceID: 1, TitleNormalized: "standup", Action: model.FilterActionInclude}},
		Filters: []model.Filter{{Field: model.FilterFieldTitle, Type: model.FilterActionInclude, Value: "standup"}},
	}
	events := []model.Event{newEvent(1, "e", "Standup", "", "", 30)} // 2026-01-05 per newEvent

	got := filtersvc.Resolve(events, in)
	if len(got) != 0 {
		t.Errorf("Resolve() = %+v, want excluded (exclude exception beats include rule and include filter)", got)
	}
}

func TestResolve_IncludeExceptionBeatsEverything(t *testing.T) {
	// The symmetric case: "only include this occurrence" must rescue an
	// event that an exclude rule, an exclude filter, and a
	// default_include=false source would all otherwise have dropped.
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: false}},
		Exceptions: []model.EventException{
			{SourceID: 1, TitleNormalized: "standup", OccurrenceDate: "2026-01-05", Action: model.FilterActionInclude},
		},
		Rules:   []model.EventRule{{SourceID: 1, TitleNormalized: "standup", Action: model.FilterActionExclude}},
		Filters: []model.Filter{{Field: model.FilterFieldTitle, Type: model.FilterActionExclude, Value: "standup"}},
	}
	events := []model.Event{newEvent(1, "e", "Standup", "", "", 30)} // 2026-01-05 per newEvent

	got := filtersvc.Resolve(events, in)
	if len(got) != 1 {
		t.Errorf("Resolve() = %+v, want included (include exception beats exclude rule and exclude filter)", got)
	}
}

func TestResolve_ExceptionOnlyAffectsThatOccurrence(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: true}},
		Exceptions: []model.EventException{
			{SourceID: 1, TitleNormalized: "standup", OccurrenceDate: "2026-01-05", Action: model.FilterActionExclude},
		},
	}
	other := newEvent(1, "other-date", "Standup", "", "", 30)
	other.Start = time.Date(2026, 1, 12, 9, 0, 0, 0, time.UTC)
	other.End = other.Start.Add(30 * time.Minute)
	events := []model.Event{
		newEvent(1, "excepted", "Standup", "", "", 30), // 2026-01-05
		other, // 2026-01-12, different occurrence, not excepted
	}

	got := filtersvc.Resolve(events, in)
	if len(got) != 1 || got[0].UID != "other-date" {
		t.Errorf("Resolve() = %+v, want only the non-excepted occurrence", got)
	}
}

func TestResolve_RuleBeatsDefaultAndFilters(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: false}}, // would default-exclude
		Rules:   []model.EventRule{{SourceID: 1, TitleNormalized: "standup", Action: model.FilterActionInclude}},
		Filters: []model.Filter{{Field: model.FilterFieldTitle, Type: model.FilterActionExclude, Value: "standup"}}, // would exclude
	}
	events := []model.Event{newEvent(1, "e", "Standup", "", "", 30)}

	got := filtersvc.Resolve(events, in)
	if len(got) != 1 {
		t.Errorf("Resolve() = %+v, want included (rule beats default and filters)", got)
	}
}

func TestResolve_RuleMatchIsCaseAndWhitespaceInsensitive(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: true}},
		Rules:   []model.EventRule{{SourceID: 1, TitleNormalized: model.NormalizeTitle("  Team STANDUP  "), Action: model.FilterActionExclude}},
	}
	events := []model.Event{newEvent(1, "e", "team standup", "", "", 30)}

	got := filtersvc.Resolve(events, in)
	if len(got) != 0 {
		t.Errorf("Resolve() = %+v, want excluded (normalized title match)", got)
	}
}

func TestResolveAll_ReportsExcludedEventsToo(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: false}},
	}
	events := []model.Event{newEvent(1, "e", "Standup", "", "", 30)}

	got := filtersvc.ResolveAll(events, in)
	if len(got) != 1 {
		t.Fatalf("ResolveAll() = %d decisions, want 1", len(got))
	}
	if got[0].Included {
		t.Error("ResolveAll()[0].Included = true, want false")
	}
	if got[0].Event.UID != "e" {
		t.Errorf("ResolveAll()[0].Event.UID = %q, want %q", got[0].Event.UID, "e")
	}
}

func TestResolve_FilterOnTitleOrDescriptionMatchesEither(t *testing.T) {
	in := filtersvc.Input{
		Sources: map[int64]model.Source{1: {ID: 1, Enabled: true, DefaultInclude: true}},
		Filters: []model.Filter{
			{Field: model.FilterFieldTitleOrDescription, Type: model.FilterActionExclude, Value: "confidential"},
		},
	}
	events := []model.Event{
		newEvent(1, "by-title", "Confidential Review", "", "", 30),
		newEvent(1, "by-description", "Meeting", "", "confidential details inside", 30),
		newEvent(1, "neither", "Meeting", "", "public notes", 30),
	}

	got := filtersvc.Resolve(events, in)
	if len(got) != 1 || got[0].UID != "neither" {
		t.Errorf("Resolve() = %+v, want only 'neither'", got)
	}
}

func ptr(i int) *int { return &i }
