package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

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
