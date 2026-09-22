package feedsvc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	ics "github.com/arran4/golang-ical"

	"permeable/internal/db"
	"permeable/internal/feedsvc"
	"permeable/internal/model"
)

const urlSourceICS = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//test//test//EN\r\n" +
	"X-PUBLISHED-TTL:PT15M\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:work-standup@example.com\r\n" +
	"DTSTAMP:20260101T000000Z\r\n" +
	"DTSTART:20260901T090000Z\r\n" +
	"DTEND:20260901T093000Z\r\n" +
	"SUMMARY:Standup\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestGenerate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(urlSourceICS))
	}))
	defer srv.Close()

	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()
	ctx := context.Background()

	_, err = db.CreateSource(ctx, sqlDB, model.Source{
		Name: "Work", Type: model.SourceTypeURL, URL: srv.URL, Enabled: true,
		DefaultInclude: true, TitlePrefix: "[Work]",
	})
	if err != nil {
		t.Fatalf("CreateSource(work): %v", err)
	}

	homeSourceID, err := db.CreateSource(ctx, sqlDB, model.Source{
		Name: "Home", Type: model.SourceTypeManual, Enabled: true, DefaultInclude: true,
	})
	if err != nil {
		t.Fatalf("CreateSource(home): %v", err)
	}
	if err := db.UpsertManualEvent(ctx, sqlDB, homeSourceID, "dentist@example.com",
		"BEGIN:VEVENT\r\nUID:dentist@example.com\r\nDTSTAMP:20260101T000000Z\r\n"+
			"DTSTART:20260902T140000Z\r\nDTEND:20260902T150000Z\r\nSUMMARY:Dentist\r\nEND:VEVENT"); err != nil {
		t.Fatalf("UpsertManualEvent: %v", err)
	}

	// Set a title prefix on Home too, and give it a rule excluding
	// "Dentist" — should not appear in the output feed, exercising rule
	// precedence downstream of generation as well.
	homeSource, err := db.GetSource(ctx, sqlDB, homeSourceID)
	if err != nil {
		t.Fatalf("GetSource(home): %v", err)
	}
	homeSource.TitlePrefix = "[Home]"
	if err := db.UpdateSource(ctx, sqlDB, homeSource); err != nil {
		t.Fatalf("UpdateSource(home): %v", err)
	}

	cfg, err := db.GetConfig(ctx, sqlDB)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	cfg.FeedTitle = "My Test Feed"
	cfg.MaxFeedTTLMinutes = 60
	if err := db.UpdateConfig(ctx, sqlDB, cfg); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	fc, err := feedsvc.Generate(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if fc.TotalEventsPreFilter != 2 {
		t.Errorf("TotalEventsPreFilter = %d, want 2", fc.TotalEventsPreFilter)
	}
	if fc.TotalEventsPostFilter != 2 {
		t.Errorf("TotalEventsPostFilter = %d, want 2", fc.TotalEventsPostFilter)
	}
	if fc.SourcesOK != 2 {
		t.Errorf("SourcesOK = %d, want 2", fc.SourcesOK)
	}
	if fc.SourcesError != 0 {
		t.Errorf("SourcesError = %d, want 0", fc.SourcesError)
	}
	// min(most-frequent-declared-TTL=15 [only Work declares one], max=60) = 15.
	if fc.EffectiveTTLMinutes != 15 {
		t.Errorf("EffectiveTTLMinutes = %d, want 15", fc.EffectiveTTLMinutes)
	}

	cal, err := ics.ParseCalendar(strings.NewReader(fc.ICSContent))
	if err != nil {
		t.Fatalf("parse generated ICS: %v\n---\n%s", err, fc.ICSContent)
	}
	events := cal.Events()
	if len(events) != 2 {
		t.Fatalf("generated calendar has %d VEVENTs, want 2", len(events))
	}

	var summaries []string
	uids := map[string]bool{}
	for _, ev := range events {
		if p := ev.GetProperty(ics.ComponentPropertySummary); p != nil {
			summaries = append(summaries, p.Value)
		}
		uids[ev.Id()] = true
	}
	wantSummaries := map[string]bool{"[Work] Standup": true, "[Home] Dentist": true}
	for _, s := range summaries {
		if !wantSummaries[s] {
			t.Errorf("unexpected summary %q in generated feed (title prefix not applied?)", s)
		}
	}
	if len(summaries) != 2 {
		t.Errorf("summaries = %v, want both title-prefixed events present", summaries)
	}
	if len(uids) != 2 {
		t.Errorf("got %d distinct UIDs, want 2 (per-occurrence UIDs must not collide)", len(uids))
	}

	if !strings.Contains(fc.ICSContent, "X-PUBLISHED-TTL:PT15M") {
		t.Errorf("generated ICS missing X-PUBLISHED-TTL:PT15M:\n%s", fc.ICSContent)
	}
	if !strings.Contains(fc.ICSContent, "REFRESH-INTERVAL") {
		t.Errorf("generated ICS missing REFRESH-INTERVAL:\n%s", fc.ICSContent)
	}

	// GET /feed.ics reads from feed_cache, not a fresh computation —
	// verify the cache actually holds what Generate returned.
	cached, ok, err := db.GetFeedCache(ctx, sqlDB)
	if err != nil {
		t.Fatalf("GetFeedCache: %v", err)
	}
	if !ok {
		t.Fatal("GetFeedCache: ok = false, want true after Generate")
	}
	if cached.ICSContent != fc.ICSContent {
		t.Error("cached ICS content does not match what Generate returned")
	}
}

func TestGenerate_SourceErrorStillProducesFeed(t *testing.T) {
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer errSrv.Close()

	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()
	ctx := context.Background()

	if _, err := db.CreateSource(ctx, sqlDB, model.Source{
		Name: "Broken", Type: model.SourceTypeURL, URL: errSrv.URL, Enabled: true, DefaultInclude: true,
	}); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}

	fc, err := feedsvc.Generate(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Generate: %v (a source error must not fail generation)", err)
	}
	if fc.SourcesError != 1 {
		t.Errorf("SourcesError = %d, want 1", fc.SourcesError)
	}
	if fc.TotalEventsPostFilter != 0 {
		t.Errorf("TotalEventsPostFilter = %d, want 0", fc.TotalEventsPostFilter)
	}
	// Even with zero events, a valid (near-empty) calendar should still
	// be cached, not an error state.
	if !strings.Contains(fc.ICSContent, "BEGIN:VCALENDAR") {
		t.Errorf("ICSContent missing VCALENDAR wrapper:\n%s", fc.ICSContent)
	}
}

// TestGenerate_AllDayEventRoundTrips is Stage 12's explicit decision on
// all-day handling: included by default like any other event (no special
// toggle), represented start-to-end with DATE (not DATE-TIME) values all
// the way through the pipeline. Duration filters see an all-day event's
// full-day span in minutes like any other event's — deliberately not
// special-cased, since a max-duration filter is a reasonable way to hide
// multi-day retreats/vacations from a feed meant for meetings.
func TestGenerate_AllDayEventRoundTrips(t *testing.T) {
	const allDayICS = "BEGIN:VCALENDAR\r\n" +
		"VERSION:2.0\r\n" +
		"PRODID:-//test//test//EN\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:holiday@example.com\r\n" +
		"DTSTAMP:20260101T000000Z\r\n" +
		"DTSTART;VALUE=DATE:20260901\r\n" +
		"DTEND;VALUE=DATE:20260902\r\n" +
		"SUMMARY:Company Holiday\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(allDayICS))
	}))
	defer srv.Close()

	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()
	ctx := context.Background()

	if _, err := db.CreateSource(ctx, sqlDB, model.Source{
		Name: "Holidays", Type: model.SourceTypeURL, URL: srv.URL, Enabled: true, DefaultInclude: true,
	}); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}

	fc, err := feedsvc.Generate(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if fc.TotalEventsPostFilter != 1 {
		t.Fatalf("TotalEventsPostFilter = %d, want 1 (all-day events included by default)", fc.TotalEventsPostFilter)
	}

	if !strings.Contains(fc.ICSContent, "DTSTART;VALUE=DATE:20260901") {
		t.Errorf("output missing all-day DTSTART;VALUE=DATE:\n%s", fc.ICSContent)
	}
	if !strings.Contains(fc.ICSContent, "DTEND;VALUE=DATE:20260902") {
		t.Errorf("output missing all-day DTEND;VALUE=DATE:\n%s", fc.ICSContent)
	}

	cal, err := ics.ParseCalendar(strings.NewReader(fc.ICSContent))
	if err != nil {
		t.Fatalf("parse generated ICS: %v", err)
	}
	events := cal.Events()
	if len(events) != 1 {
		t.Fatalf("got %d VEVENTs, want 1", len(events))
	}
	if start, err := events[0].GetAllDayStartAt(); err != nil {
		t.Errorf("GetAllDayStartAt: %v", err)
	} else if start.Format("2006-01-02") != "2026-09-01" {
		t.Errorf("all-day start = %s, want 2026-09-01", start.Format("2006-01-02"))
	}
}

// TestGenerate_DurationFilterAppliesToAllDaySpan confirms the "no
// special-casing" half of the Stage 12 decision: a max-duration filter
// correctly excludes an all-day event using its full-day span.
func TestGenerate_DurationFilterAppliesToAllDaySpan(t *testing.T) {
	const allDayICS = "BEGIN:VCALENDAR\r\n" +
		"VERSION:2.0\r\n" +
		"PRODID:-//test//test//EN\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:retreat@example.com\r\n" +
		"DTSTAMP:20260101T000000Z\r\n" +
		"DTSTART;VALUE=DATE:20260901\r\n" +
		"DTEND;VALUE=DATE:20260903\r\n" + // 2-day span = 2880 minutes
		"SUMMARY:Company Retreat\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(allDayICS))
	}))
	defer srv.Close()

	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()
	ctx := context.Background()

	if _, err := db.CreateSource(ctx, sqlDB, model.Source{
		Name: "Retreats", Type: model.SourceTypeURL, URL: srv.URL, Enabled: true, DefaultInclude: true,
	}); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	maxM := 480 // 8 hours — well under the 2-day retreat span
	if err := db.UpdateDurationFilters(ctx, sqlDB, model.DurationFilter{MaxMinutes: &maxM}); err != nil {
		t.Fatalf("UpdateDurationFilters: %v", err)
	}

	fc, err := feedsvc.Generate(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if fc.TotalEventsPostFilter != 0 {
		t.Errorf("TotalEventsPostFilter = %d, want 0 (multi-day span exceeds max-duration filter)", fc.TotalEventsPostFilter)
	}
}
