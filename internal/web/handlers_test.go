package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"permeable/internal/db"
	"permeable/internal/scheduler"
	"permeable/internal/web"
)

func newTestServer(t *testing.T) (*httptest.Server, *httptest.Server) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "permeable.db"), 60)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	sched := scheduler.New(sqlDB)

	handler, err := web.NewHandler(sqlDB, 8081, sched)
	if err != nil {
		t.Fatalf("web.NewHandler: %v", err)
	}

	admin := httptest.NewServer(handler.AdminRoutes())
	t.Cleanup(admin.Close)
	feed := httptest.NewServer(handler.FeedRoutes())
	t.Cleanup(feed.Close)

	return admin, feed
}

func postForm(t *testing.T, client *http.Client, target string, form url.Values) *http.Response {
	t.Helper()
	resp, err := client.PostForm(target, form)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	return resp
}

func bodyContains(t *testing.T, resp *http.Response, want string) bool {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return strings.Contains(string(b), want)
}

// noRedirectClient follows manual redirects so we can inspect the
// Location header (which carries our flash-message query param) instead
// of the page it points to.
func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func TestCreateSource_ValidURL(t *testing.T) {
	admin, _ := newTestServer(t)
	client := noRedirectClient()

	resp := postForm(t, client, admin.URL+"/sources", url.Values{
		"name": {"Work"}, "type": {"url"}, "url": {"https://example.com/cal.ics"},
	})
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(loc, "ok=") {
		t.Fatalf("status=%d location=%q, want 303 with an ok= flash", resp.StatusCode, loc)
	}
}

func TestCreateSource_RejectsMalformedURL(t *testing.T) {
	admin, _ := newTestServer(t)
	client := noRedirectClient()

	cases := []string{"not-a-url", "ftp://example.com/cal.ics", "example.com/cal.ics", ""}
	for _, badURL := range cases {
		t.Run(badURL, func(t *testing.T) {
			resp := postForm(t, client, admin.URL+"/sources", url.Values{
				"name": {"Work"}, "type": {"url"}, "url": {badURL},
			})
			loc := resp.Header.Get("Location")
			if !strings.Contains(loc, "err=") {
				t.Errorf("url %q was accepted; Location = %q, want an err= flash", badURL, loc)
			}
		})
	}
}

func TestCreateSource_RequiresName(t *testing.T) {
	admin, _ := newTestServer(t)
	client := noRedirectClient()

	resp := postForm(t, client, admin.URL+"/sources", url.Values{
		"name": {""}, "type": {"manual"},
	})
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "err=") {
		t.Errorf("empty name was accepted; Location = %q, want an err= flash", loc)
	}
}

func TestSettingsPage_ListsCreatedSource(t *testing.T) {
	admin, _ := newTestServer(t)
	client := admin.Client()

	resp := postForm(t, client, admin.URL+"/sources", url.Values{
		"name": {"My Manual Source"}, "type": {"manual"},
	})
	resp.Body.Close()

	resp, err := client.Get(admin.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	if !bodyContains(t, resp, "My Manual Source") {
		t.Error("settings page does not list the created source")
	}
}

func TestFeed_503BeforeFirstGeneration(t *testing.T) {
	_, feed := newTestServer(t)

	resp, err := http.Get(feed.URL + "/feed.ics")
	if err != nil {
		t.Fatalf("GET /feed.ics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d before any generation", resp.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestFeed_ServesAfterRefreshNow(t *testing.T) {
	admin, feed := newTestServer(t)
	client := admin.Client()

	resp := postForm(t, client, admin.URL+"/refresh", url.Values{})
	resp.Body.Close()

	feedResp, err := http.Get(feed.URL + "/feed.ics")
	if err != nil {
		t.Fatalf("GET /feed.ics: %v", err)
	}
	defer feedResp.Body.Close()
	if feedResp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 after refresh", feedResp.StatusCode)
	}
	if ct := feedResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("Content-Type = %q, want text/calendar", ct)
	}
}

func TestFeedRoutes_OnlyExposesFeedIcs(t *testing.T) {
	_, feed := newTestServer(t)

	for _, path := range []string{"/", "/sources", "/stats", "/healthz"} {
		resp, err := http.Get(feed.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("feed port exposed %s with status %d, want 404 (admin routes must not leak onto the public port)", path, resp.StatusCode)
		}
	}
}

func TestHealthz(t *testing.T) {
	admin, _ := newTestServer(t)

	resp, err := http.Get(admin.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !bodyContains(t, resp, "ok") {
		t.Error("healthz body does not contain \"ok\"")
	}
}

func TestUploadManualEvents_RejectsNonManualSource(t *testing.T) {
	admin, _ := newTestServer(t)
	client := noRedirectClient()

	resp := postForm(t, client, admin.URL+"/sources", url.Values{
		"name": {"URL Source"}, "type": {"url"}, "url": {"https://example.com/cal.ics"},
	})
	resp.Body.Close()

	// Upload against source id 1 (the url source just created) — should
	// be rejected, not silently accepted.
	var buf strings.Builder
	buf.WriteString("--boundary\r\nContent-Disposition: form-data; name=\"ics_file\"; filename=\"x.ics\"\r\nContent-Type: text/calendar\r\n\r\n")
	buf.WriteString("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
	buf.WriteString("--boundary--\r\n")

	req, err := http.NewRequest(http.MethodPost, admin.URL+"/sources/1/events", strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")

	uploadResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	loc := uploadResp.Header.Get("Location")
	if !strings.Contains(loc, "err=") {
		t.Errorf("upload to url-type source was accepted; Location = %q, want an err= flash", loc)
	}
}

func TestCreateFilter_AcceptsTitleOrDescriptionField(t *testing.T) {
	admin, _ := newTestServer(t)
	client := noRedirectClient()

	resp := postForm(t, client, admin.URL+"/filters", url.Values{
		"field": {"title_or_description"}, "type": {"exclude"}, "value": {"confidential"},
	})
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(loc, "ok=") {
		t.Fatalf("status=%d location=%q, want 303 with an ok= flash", resp.StatusCode, loc)
	}

	settingsResp, err := admin.Client().Get(admin.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	if !bodyContains(t, settingsResp, "title_or_description") {
		t.Error("settings page does not list the created title_or_description filter")
	}
}

// TestEventException_IncludeOverridesDefaultExclude covers "only include
// this occurrence": an event from a default_include=false source must
// show as included after an include-action exception is set for it, and
// flip back to excluded if the exception is later changed to exclude —
// end to end through real HTTP requests, not just filtersvc's unit tests.
func TestEventException_IncludeOverridesDefaultExclude(t *testing.T) {
	admin, _ := newTestServer(t)
	client := admin.Client()

	resp := postForm(t, client, admin.URL+"/sources", url.Values{
		"name": {"Quiet Source"}, "type": {"manual"}, // default_include unset -> false
	})
	resp.Body.Close()

	occurrenceDate := time.Now().UTC().AddDate(0, 0, 1).Format("20060102")
	var buf strings.Builder
	buf.WriteString("--boundary\r\nContent-Disposition: form-data; name=\"ics_file\"; filename=\"x.ics\"\r\nContent-Type: text/calendar\r\n\r\n")
	buf.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//test//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:standup@example.com\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART:" + occurrenceDate + "T090000Z\r\nDTEND:" + occurrenceDate + "T093000Z\r\n" +
		"SUMMARY:Standup\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	buf.WriteString("--boundary--\r\n")
	req, err := http.NewRequest(http.MethodPost, admin.URL+"/sources/1/events", strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("build upload request: %v", err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	uploadResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	uploadResp.Body.Close()

	occurrenceDateDashed := occurrenceDate[0:4] + "-" + occurrenceDate[4:6] + "-" + occurrenceDate[6:8]

	assertEventsPageStatus := func(t *testing.T, want string) {
		t.Helper()
		eventsResp, err := client.Get(admin.URL + "/events")
		if err != nil {
			t.Fatalf("GET /events: %v", err)
		}
		if !bodyContains(t, eventsResp, `class="badge `+want+`"`) {
			t.Errorf("events page does not show a %q badge for the occurrence", want)
		}
	}

	// Baseline: default_include=false, no override yet -> excluded.
	assertEventsPageStatus(t, "excluded")

	// "Only include this occurrence."
	incResp := postForm(t, client, admin.URL+"/events/exceptions", url.Values{
		"source_id": {"1"}, "title": {"Standup"}, "occurrence_date": {occurrenceDateDashed}, "action": {"include"},
	})
	incResp.Body.Close()
	assertEventsPageStatus(t, "included")

	// Flip back to "hide this occurrence" for the same spot — must
	// update the existing row in place (schema UNIQUE constraint), not
	// add a second exception.
	excResp := postForm(t, client, admin.URL+"/events/exceptions", url.Values{
		"source_id": {"1"}, "title": {"Standup"}, "occurrence_date": {occurrenceDateDashed}, "action": {"exclude"},
	})
	excResp.Body.Close()
	assertEventsPageStatus(t, "excluded")
}
