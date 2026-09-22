package fetchsvc_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"permeable/internal/db"
	"permeable/internal/fetchsvc"
	"permeable/internal/model"
)

// icsUTC formats a time offset from now as an ICS UTC timestamp. Tests
// use offsets (not hardcoded dates) so fixtures stay inside the default
// fetch window (7 days back, 90 days forward) no matter when they run.
func icsUTC(offset time.Duration) string {
	return time.Now().UTC().Add(offset).Format("20060102T150405Z")
}

// mixedGoodOffset is shared between mixedICS's "mixed-good" event and the
// manual-source duplicate of it in TestRun — both must land on the exact
// same instant for the cross-source (UID, start) dedup case to exercise
// what it's meant to.
const mixedGoodOffset = 72 * time.Hour

// goodICS has two well-formed VEVENTs and a declared TTL, so it exercises
// normalization plus X-PUBLISHED-TTL parsing.
func goodICS() string {
	return "BEGIN:VCALENDAR\r\n" +
		"VERSION:2.0\r\n" +
		"PRODID:-//test//test//EN\r\n" +
		"X-PUBLISHED-TTL:PT30M\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:good-1@example.com\r\n" +
		"DTSTAMP:" + icsUTC(0) + "\r\n" +
		"DTSTART:" + icsUTC(24*time.Hour) + "\r\n" +
		"DTEND:" + icsUTC(25*time.Hour) + "\r\n" +
		"SUMMARY:Good Event One\r\n" +
		"END:VEVENT\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:good-2@example.com\r\n" +
		"DTSTAMP:" + icsUTC(0) + "\r\n" +
		"DTSTART:" + icsUTC(48*time.Hour) + "\r\n" +
		"DTEND:" + icsUTC(49*time.Hour) + "\r\n" +
		"SUMMARY:Good Event Two\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
}

// mixedICS has one well-formed VEVENT and one malformed (no DTSTART), to
// verify malformed VEVENTs are skipped without failing the whole source.
func mixedICS() string {
	return "BEGIN:VCALENDAR\r\n" +
		"VERSION:2.0\r\n" +
		"PRODID:-//test//test//EN\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:mixed-good@example.com\r\n" +
		"DTSTAMP:" + icsUTC(0) + "\r\n" +
		"DTSTART:" + icsUTC(mixedGoodOffset) + "\r\n" +
		"DTEND:" + icsUTC(mixedGoodOffset+time.Hour) + "\r\n" +
		"SUMMARY:Mixed Good Event\r\n" +
		"END:VEVENT\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:mixed-bad@example.com\r\n" +
		"DTSTAMP:" + icsUTC(0) + "\r\n" +
		"SUMMARY:Missing DTSTART\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

func mustCreateSource(t *testing.T, ctx context.Context, sqlDB *sql.DB, s model.Source) int64 {
	t.Helper()
	id, err := db.CreateSource(ctx, sqlDB, s)
	if err != nil {
		t.Fatalf("CreateSource(%s): %v", s.Name, err)
	}
	return id
}

func mustUpsertManualEvent(t *testing.T, ctx context.Context, sqlDB *sql.DB, sourceID int64, uid, raw string) {
	t.Helper()
	if err := db.UpsertManualEvent(ctx, sqlDB, sourceID, uid, raw); err != nil {
		t.Fatalf("UpsertManualEvent(%s): %v", uid, err)
	}
}

func assertHealth(t *testing.T, ctx context.Context, sqlDB *sql.DB, sourceID int64, wantStatus string, wantCount int, wantTTL *int) {
	t.Helper()
	s, err := db.GetSource(ctx, sqlDB, sourceID)
	if err != nil {
		t.Fatalf("GetSource(%d): %v", sourceID, err)
	}
	if s.LastFetchAt == nil {
		t.Errorf("source %d: LastFetchAt is nil, want set", sourceID)
	}
	if s.LastFetchStatus != wantStatus {
		t.Errorf("source %d: LastFetchStatus = %q, want %q (error: %q)", sourceID, s.LastFetchStatus, wantStatus, s.LastFetchError)
	}
	if s.LastEventCount == nil || *s.LastEventCount != wantCount {
		t.Errorf("source %d: LastEventCount = %v, want %d", sourceID, s.LastEventCount, wantCount)
	}
	switch {
	case wantTTL == nil && s.SourceTTLMinutes != nil:
		t.Errorf("source %d: SourceTTLMinutes = %d, want nil", sourceID, *s.SourceTTLMinutes)
	case wantTTL != nil && (s.SourceTTLMinutes == nil || *s.SourceTTLMinutes != *wantTTL):
		t.Errorf("source %d: SourceTTLMinutes = %v, want %d", sourceID, s.SourceTTLMinutes, *wantTTL)
	}
}

func ptr(i int) *int { return &i }

func TestRun(t *testing.T) {
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(goodICS()))
	}))
	defer goodServer.Close()

	mixedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mixedICS()))
	}))
	defer mixedServer.Close()

	sqlDB := openTestDB(t)
	ctx := context.Background()

	goodSourceID := mustCreateSource(t, ctx, sqlDB, model.Source{
		Name: "Good URL Source", Type: model.SourceTypeURL, URL: goodServer.URL, Enabled: true,
	})
	mixedSourceID := mustCreateSource(t, ctx, sqlDB, model.Source{
		Name: "Mixed URL Source", Type: model.SourceTypeURL, URL: mixedServer.URL, Enabled: true,
	})
	disabledSourceID := mustCreateSource(t, ctx, sqlDB, model.Source{
		Name: "Disabled URL Source", Type: model.SourceTypeURL, URL: goodServer.URL, Enabled: false,
	})
	manualSourceID := mustCreateSource(t, ctx, sqlDB, model.Source{
		Name: "Manual Source", Type: model.SourceTypeManual, Enabled: true,
	})

	// Seed the manual source with the same good/malformed VEVENT pair.
	// "mixed-good" intentionally shares its UID and start time with the
	// mixed URL source's event, to exercise cross-source dedup too.
	mustUpsertManualEvent(t, ctx, sqlDB, manualSourceID, "mixed-good@example.com",
		"BEGIN:VEVENT\r\nUID:mixed-good@example.com\r\nDTSTAMP:"+icsUTC(0)+"\r\n"+
			"DTSTART:"+icsUTC(mixedGoodOffset)+"\r\nDTEND:"+icsUTC(mixedGoodOffset+time.Hour)+"\r\nSUMMARY:Mixed Good Event\r\nEND:VEVENT")
	mustUpsertManualEvent(t, ctx, sqlDB, manualSourceID, "mixed-bad@example.com",
		"BEGIN:VEVENT\r\nUID:mixed-bad@example.com\r\nDTSTAMP:"+icsUTC(0)+"\r\n"+
			"SUMMARY:Missing DTSTART\r\nEND:VEVENT")

	result, err := fetchsvc.Run(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// A malformed VEVENT must never abort its whole source: 2 (good) + 1
	// (mixed, one skipped) + 1 (manual, one skipped) = 4 before dedup;
	// "mixed-good" is a duplicate (same UID+start) across the mixed URL
	// and manual sources, so the deduped total is 3.
	wantEventCount := 3
	if len(result.Events) != wantEventCount {
		t.Errorf("len(result.Events) = %d, want %d", len(result.Events), wantEventCount)
	}

	assertHealth(t, ctx, sqlDB, goodSourceID, "ok", 2, ptr(30))
	assertHealth(t, ctx, sqlDB, mixedSourceID, "ok", 1, nil) // mixedICS declares no TTL
	assertHealth(t, ctx, sqlDB, manualSourceID, "ok", 1, nil)

	// The disabled source must not be touched at all this run.
	disabled, err := db.GetSource(ctx, sqlDB, disabledSourceID)
	if err != nil {
		t.Fatalf("GetSource(disabled): %v", err)
	}
	if disabled.LastFetchAt != nil {
		t.Errorf("disabled source was fetched: LastFetchAt = %v, want nil", disabled.LastFetchAt)
	}
}

func TestRun_SourceErrorIsolated(t *testing.T) {
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(goodICS()))
	}))
	defer goodServer.Close()

	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer errServer.Close()

	sqlDB := openTestDB(t)
	ctx := context.Background()

	goodSourceID := mustCreateSource(t, ctx, sqlDB, model.Source{
		Name: "Good Source", Type: model.SourceTypeURL, URL: goodServer.URL, Enabled: true,
	})
	errSourceID := mustCreateSource(t, ctx, sqlDB, model.Source{
		Name: "Erroring Source", Type: model.SourceTypeURL, URL: errServer.URL, Enabled: true,
	})

	result, err := fetchsvc.Run(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Run: %v (a source error must not fail the whole run)", err)
	}
	if len(result.Events) != 2 {
		t.Errorf("len(result.Events) = %d, want 2 (only from the good source)", len(result.Events))
	}

	assertHealth(t, ctx, sqlDB, goodSourceID, "ok", 2, ptr(30))

	errSource, err := db.GetSource(ctx, sqlDB, errSourceID)
	if err != nil {
		t.Fatalf("GetSource(err source): %v", err)
	}
	if errSource.LastFetchStatus != "error" {
		t.Errorf("LastFetchStatus = %q, want %q", errSource.LastFetchStatus, "error")
	}
	if errSource.LastFetchError == "" {
		t.Error("LastFetchError is empty, want a message")
	}
}

// TestRun_GarbageContentIsolated covers a realistic real-world failure
// mode distinct from an HTTP error status: a source returns 200 OK with
// something that isn't ICS at all (an expired share link's HTML error
// page, a login redirect page, etc.). This must be recorded as a
// per-source error, not crash or abort the run.
func TestRun_GarbageContentIsolated(t *testing.T) {
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(goodICS()))
	}))
	defer goodServer.Close()

	garbageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>You must sign in to view this calendar.</body></html>"))
	}))
	defer garbageServer.Close()

	sqlDB := openTestDB(t)
	ctx := context.Background()

	goodSourceID := mustCreateSource(t, ctx, sqlDB, model.Source{
		Name: "Good Source", Type: model.SourceTypeURL, URL: goodServer.URL, Enabled: true,
	})
	garbageSourceID := mustCreateSource(t, ctx, sqlDB, model.Source{
		Name: "Garbage Source", Type: model.SourceTypeURL, URL: garbageServer.URL, Enabled: true,
	})

	result, err := fetchsvc.Run(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Run: %v (garbage content from one source must not fail the whole run)", err)
	}
	if len(result.Events) != 2 {
		t.Errorf("len(result.Events) = %d, want 2 (only from the good source)", len(result.Events))
	}

	assertHealth(t, ctx, sqlDB, goodSourceID, "ok", 2, ptr(30))

	garbageSource, err := db.GetSource(ctx, sqlDB, garbageSourceID)
	if err != nil {
		t.Fatalf("GetSource(garbage source): %v", err)
	}
	if garbageSource.LastFetchStatus != "error" {
		t.Errorf("LastFetchStatus = %q, want %q", garbageSource.LastFetchStatus, "error")
	}
	if garbageSource.LastFetchError == "" {
		t.Error("LastFetchError is empty, want a message")
	}
}
