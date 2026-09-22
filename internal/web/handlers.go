// Package web holds the HTTP handlers and (embedded) templates for
// permeable's server-rendered UI. Handlers stay thin: parse the request,
// delegate to a db.* or *svc function, render or redirect.
package web

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	ics "github.com/arran4/golang-ical"

	"permeable/internal/db"
	"permeable/internal/model"
	"permeable/internal/scheduler"
)

// vEventSerializationConfig is passed to VEvent.Serialize. Unlike
// Calendar.Serialize (variadic, nil-safe), VEvent.Serialize requires a
// non-nil *SerializationConfiguration and panics on nil — these are the
// same defaults the library uses internally.
var vEventSerializationConfig = &ics.SerializationConfiguration{
	MaxLength:         75,
	PropertyMaxLength: 75,
	NewLine:           string(ics.NewLine),
}

// Handler wires the DB to the HTTP routes.
type Handler struct {
	db        *sql.DB
	tmpl      *template.Template
	feedPort  int // for displaying the subscribable feed URL on the settings page
	scheduler *scheduler.Scheduler
}

// NewHandler parses the embedded templates and returns a ready-to-mount
// Handler. feedPort is used only to render the feed URL shown on the
// settings page (see FeedRoutes for the listener it actually describes).
// sched backs "Refresh now" and is nudged to reload its ticker whenever
// config is saved.
func NewHandler(sqlDB *sql.DB, feedPort int, sched *scheduler.Scheduler) (*Handler, error) {
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &Handler{db: sqlDB, tmpl: tmpl, feedPort: feedPort, scheduler: sched}, nil
}

// AdminRoutes returns the settings UI + CRUD + stats route table. Bind
// this to a LAN-only listener — it is never meant to sit behind a public
// reverse proxy (see FeedRoutes, which is the one port safe to expose).
func (h *Handler) AdminRoutes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", h.handleSettingsPage)
	mux.HandleFunc("POST /sources", h.handleCreateSource)
	mux.HandleFunc("POST /sources/{id}", h.handleUpdateSource)
	mux.HandleFunc("POST /sources/{id}/delete", h.handleDeleteSource)
	mux.HandleFunc("POST /sources/{id}/events", h.handleUploadManualEvents)
	mux.HandleFunc("POST /sources/{id}/events/{eventID}/delete", h.handleDeleteManualEvent)
	mux.HandleFunc("POST /filters", h.handleCreateFilter)
	mux.HandleFunc("POST /filters/{id}/delete", h.handleDeleteFilter)
	mux.HandleFunc("POST /duration-filters", h.handleUpdateDurationFilters)
	mux.HandleFunc("POST /config", h.handleUpdateConfig)
	mux.HandleFunc("GET /events", h.handleEventsPage)
	mux.HandleFunc("POST /events/exceptions", h.handleUpsertEventException)
	mux.HandleFunc("POST /events/exceptions/{id}/delete", h.handleDeleteEventException)
	mux.HandleFunc("POST /events/rules", h.handleUpsertEventRule)
	mux.HandleFunc("POST /events/rules/{id}/delete", h.handleDeleteEventRule)
	mux.HandleFunc("GET /stats", h.handleStatsPage)
	mux.HandleFunc("GET /healthz", h.handleHealthz)
	mux.HandleFunc("POST /refresh", h.handleRefreshNow)
	mux.HandleFunc("GET /config/export", h.handleConfigExport)
	mux.HandleFunc("POST /config/import", h.handleConfigImport)

	return mux
}

// FeedRoutes returns the public-facing route table: GET /feed.ics only.
// This is the one listener meant to be reverse-proxied / exposed
// publicly.
func (h *Handler) FeedRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /feed.ics", h.handleFeed)
	return mux
}

// handleFeed always serves feed_cache as-is — no work on request
// (CLAUDE.md pipeline step 12). Until the first successful generation
// (there's no scheduler yet; that's Stage 9) there's nothing cached, so
// this reports 503 rather than serving nothing silently.
func (h *Handler) handleFeed(w http.ResponseWriter, r *http.Request) {
	fc, ok, err := db.GetFeedCache(r.Context(), h.db)
	if err != nil {
		h.serverError(w, "get feed cache", err)
		return
	}
	if !ok {
		http.Error(w, "feed not generated yet", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	_, _ = w.Write([]byte(fc.ICSContent))
}

// sourceView adds manual-event data to model.Source for rendering; empty
// for url-type sources.
type sourceView struct {
	model.Source
	ManualEvents []model.ManualEvent
}

// settingsPageData is the data passed to templates/settings.html.
type settingsPageData struct {
	Sources        []sourceView
	Filters        []model.Filter
	DurationFilter model.DurationFilter
	Config         model.Config
	FeedURL        string
	Error          string
	Message        string
}

func (h *Handler) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sources, err := db.ListSources(ctx, h.db)
	if err != nil {
		h.serverError(w, "list sources", err)
		return
	}

	data := settingsPageData{
		Error:   r.URL.Query().Get("err"),
		Message: r.URL.Query().Get("ok"),
	}
	for _, s := range sources {
		sv := sourceView{Source: s}
		if s.Type == model.SourceTypeManual {
			events, err := db.ListManualEvents(ctx, h.db, s.ID)
			if err != nil {
				h.serverError(w, "list manual events", err)
				return
			}
			sv.ManualEvents = events
		}
		data.Sources = append(data.Sources, sv)
	}

	filters, err := db.ListFilters(ctx, h.db)
	if err != nil {
		h.serverError(w, "list filters", err)
		return
	}
	data.Filters = filters

	df, err := db.GetDurationFilters(ctx, h.db)
	if err != nil {
		h.serverError(w, "get duration filters", err)
		return
	}
	data.DurationFilter = df

	cfg, err := db.GetConfig(ctx, h.db)
	if err != nil {
		h.serverError(w, "get config", err)
		return
	}
	data.Config = cfg
	data.FeedURL = h.feedURL(r)

	if err := h.tmpl.ExecuteTemplate(w, "settings.html", data); err != nil {
		logRenderError("settings.html", err)
	}
}

// feedURL builds the subscribable feed URL for display: same host the
// admin UI was reached on, but the feed port, since the two are separate
// listeners (see FeedRoutes).
func (h *Handler) feedURL(r *http.Request) string {
	host := r.Host
	if hostOnly, _, err := net.SplitHostPort(r.Host); err == nil {
		host = hostOnly
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d/feed.ics", scheme, host, h.feedPort)
}

func (h *Handler) handleCreateSource(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.redirectError(w, r, "could not parse form: "+err.Error())
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	typ := model.SourceType(r.FormValue("type"))
	rawURL := strings.TrimSpace(r.FormValue("url"))
	titlePrefix := strings.TrimSpace(r.FormValue("title_prefix"))
	defaultInclude := r.FormValue("default_include") == "on"

	if name == "" {
		h.redirectError(w, r, "name is required")
		return
	}
	if typ != model.SourceTypeURL && typ != model.SourceTypeManual {
		h.redirectError(w, r, "type must be url or manual")
		return
	}
	if typ == model.SourceTypeURL {
		if err := validateSourceURL(rawURL); err != nil {
			h.redirectError(w, r, err.Error())
			return
		}
	}
	if typ == model.SourceTypeManual {
		rawURL = "" // manual sources never carry a URL
	}

	s := model.Source{
		Name:           name,
		Type:           typ,
		URL:            rawURL,
		Enabled:        true,
		DefaultInclude: defaultInclude,
		TitlePrefix:    titlePrefix,
	}
	if _, err := db.CreateSource(r.Context(), h.db, s); err != nil {
		h.serverError(w, "create source", err)
		return
	}

	h.redirectOK(w, r, "source created")
}

func (h *Handler) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.redirectError(w, r, err.Error())
		return
	}

	existing, err := db.GetSource(r.Context(), h.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.redirectError(w, r, "source not found")
			return
		}
		h.serverError(w, "get source", err)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.redirectError(w, r, "could not parse form: "+err.Error())
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.redirectError(w, r, "name is required")
		return
	}
	existing.Name = name
	existing.Enabled = r.FormValue("enabled") == "on"
	existing.DefaultInclude = r.FormValue("default_include") == "on"
	existing.TitlePrefix = strings.TrimSpace(r.FormValue("title_prefix"))

	if existing.Type == model.SourceTypeURL {
		rawURL := strings.TrimSpace(r.FormValue("url"))
		if err := validateSourceURL(rawURL); err != nil {
			h.redirectError(w, r, err.Error())
			return
		}
		existing.URL = rawURL
	}

	if err := db.UpdateSource(r.Context(), h.db, existing); err != nil {
		h.serverError(w, "update source", err)
		return
	}

	h.redirectOK(w, r, "source updated")
}

func (h *Handler) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.redirectError(w, r, err.Error())
		return
	}
	if err := db.DeleteSource(r.Context(), h.db, id); err != nil {
		h.serverError(w, "delete source", err)
		return
	}
	h.redirectOK(w, r, "source deleted")
}

// handleUploadManualEvents accepts a single- or multi-event .ics upload
// and upserts every VEVENT found by UID.
func (h *Handler) handleUploadManualEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := pathID(r, "id")
	if err != nil {
		h.redirectError(w, r, err.Error())
		return
	}

	source, err := db.GetSource(ctx, h.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.redirectError(w, r, "source not found")
			return
		}
		h.serverError(w, "get source", err)
		return
	}
	if source.Type != model.SourceTypeManual {
		h.redirectError(w, r, "events can only be uploaded to manual sources")
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		h.redirectError(w, r, "could not parse upload: "+err.Error())
		return
	}
	file, _, err := r.FormFile("ics_file")
	if err != nil {
		h.redirectError(w, r, "no file uploaded")
		return
	}
	defer file.Close()

	cal, err := ics.ParseCalendar(file)
	if err != nil {
		h.redirectError(w, r, "could not parse .ics file: "+err.Error())
		return
	}

	events := cal.Events()
	if len(events) == 0 {
		h.redirectError(w, r, "no VEVENT found in uploaded file")
		return
	}

	added := 0
	for _, ev := range events {
		uid := ev.Id()
		if uid == "" {
			log.Printf("web: skipping VEVENT with no UID in upload to source %d", id)
			continue
		}
		raw := ev.Serialize(vEventSerializationConfig)
		if err := db.UpsertManualEvent(ctx, h.db, id, uid, raw); err != nil {
			h.serverError(w, "upsert manual event", err)
			return
		}
		added++
	}

	if added == 0 {
		h.redirectError(w, r, "no VEVENT with a UID found in uploaded file")
		return
	}
	h.redirectOK(w, r, fmt.Sprintf("%d event(s) added/updated", added))
}

func (h *Handler) handleDeleteManualEvent(w http.ResponseWriter, r *http.Request) {
	sourceID, err := pathID(r, "id")
	if err != nil {
		h.redirectError(w, r, err.Error())
		return
	}
	eventID, err := pathID(r, "eventID")
	if err != nil {
		h.redirectError(w, r, err.Error())
		return
	}
	if err := db.DeleteManualEvent(r.Context(), h.db, sourceID, eventID); err != nil {
		h.serverError(w, "delete manual event", err)
		return
	}
	h.redirectOK(w, r, "event removed")
}

func (h *Handler) handleCreateFilter(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.redirectError(w, r, "could not parse form: "+err.Error())
		return
	}

	field := model.FilterField(r.FormValue("field"))
	typ := model.FilterAction(r.FormValue("type"))
	value := strings.TrimSpace(r.FormValue("value"))

	switch field {
	case model.FilterFieldTitle, model.FilterFieldLocation, model.FilterFieldDescription, model.FilterFieldTitleOrDescription:
	default:
		h.redirectError(w, r, "field must be title, location, description, or title_or_description")
		return
	}
	if typ != model.FilterActionInclude && typ != model.FilterActionExclude {
		h.redirectError(w, r, "type must be include or exclude")
		return
	}
	if value == "" {
		h.redirectError(w, r, "value is required")
		return
	}

	if _, err := db.CreateFilter(r.Context(), h.db, model.Filter{Field: field, Type: typ, Value: value}); err != nil {
		h.serverError(w, "create filter", err)
		return
	}
	h.redirectOK(w, r, "filter added")
}

func (h *Handler) handleDeleteFilter(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.redirectError(w, r, err.Error())
		return
	}
	if err := db.DeleteFilter(r.Context(), h.db, id); err != nil {
		h.serverError(w, "delete filter", err)
		return
	}
	h.redirectOK(w, r, "filter deleted")
}

func (h *Handler) handleUpdateDurationFilters(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.redirectError(w, r, "could not parse form: "+err.Error())
		return
	}

	minMinutes, err := parseOptionalNonNegativeInt(r.FormValue("min_minutes"))
	if err != nil {
		h.redirectError(w, r, "min minutes: "+err.Error())
		return
	}
	maxMinutes, err := parseOptionalNonNegativeInt(r.FormValue("max_minutes"))
	if err != nil {
		h.redirectError(w, r, "max minutes: "+err.Error())
		return
	}
	if minMinutes != nil && maxMinutes != nil && *minMinutes > *maxMinutes {
		h.redirectError(w, r, "min minutes must not exceed max minutes")
		return
	}

	df := model.DurationFilter{MinMinutes: minMinutes, MaxMinutes: maxMinutes}
	if err := db.UpdateDurationFilters(r.Context(), h.db, df); err != nil {
		h.serverError(w, "update duration filters", err)
		return
	}
	h.redirectOK(w, r, "duration filter saved")
}

func (h *Handler) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.redirectError(w, r, "could not parse form: "+err.Error())
		return
	}

	refreshMinutes, err := parsePositiveInt(r.FormValue("refresh_minutes"))
	if err != nil {
		h.redirectError(w, r, "poll interval: "+err.Error())
		return
	}
	maxFeedTTLMinutes, err := parsePositiveInt(r.FormValue("max_feed_ttl_minutes"))
	if err != nil {
		h.redirectError(w, r, "max feed TTL: "+err.Error())
		return
	}
	daysBack, err := parseNonNegativeInt(r.FormValue("days_back"))
	if err != nil {
		h.redirectError(w, r, "days back: "+err.Error())
		return
	}
	daysForward, err := parseNonNegativeInt(r.FormValue("days_forward"))
	if err != nil {
		h.redirectError(w, r, "days forward: "+err.Error())
		return
	}
	feedTitle := strings.TrimSpace(r.FormValue("feed_title"))
	if feedTitle == "" {
		h.redirectError(w, r, "feed title is required")
		return
	}

	cfg := model.Config{
		RefreshMinutes:    refreshMinutes,
		MaxFeedTTLMinutes: maxFeedTTLMinutes,
		DaysBack:          daysBack,
		DaysForward:       daysForward,
		FeedTitle:         feedTitle,
	}
	if err := db.UpdateConfig(r.Context(), h.db, cfg); err != nil {
		h.serverError(w, "update config", err)
		return
	}
	if h.scheduler != nil {
		h.scheduler.Reload() // apply a changed refresh_minutes immediately, not at the next tick
	}
	h.redirectOK(w, r, "config saved")
}

// handleRefreshNow triggers an immediate pipeline run outside the
// regular ticker schedule, blocking until it completes.
func (h *Handler) handleRefreshNow(w http.ResponseWriter, r *http.Request) {
	if h.scheduler == nil {
		h.redirectError(w, r, "scheduler not available")
		return
	}
	fc, err := h.scheduler.RefreshNow(r.Context())
	if err != nil {
		h.serverError(w, "refresh now", err)
		return
	}
	h.redirectOK(w, r, fmt.Sprintf("feed refreshed: %d event(s) published", fc.TotalEventsPostFilter))
}

// parseOptionalNonNegativeInt parses a form field that may be blank
// (meaning "no bound"), returning nil in that case.
func parseOptionalNonNegativeInt(s string) (*int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("must be a whole number: %w", err)
	}
	if n < 0 {
		return nil, fmt.Errorf("must not be negative")
	}
	return &n, nil
}

// parsePositiveInt parses a required, strictly-positive form field (poll
// interval, max feed TTL — zero or blank doesn't make sense for either).
func parsePositiveInt(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("must be a whole number: %w", err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be greater than zero")
	}
	return n, nil
}

// parseNonNegativeInt parses a required, non-negative form field (a date
// window of 0 days is valid — it just excludes everything on that side).
func parseNonNegativeInt(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("must be a whole number: %w", err)
	}
	if n < 0 {
		return 0, fmt.Errorf("must not be negative")
	}
	return n, nil
}

// validateSourceURL requires an absolute http(s) URL — enough to catch
// typos and copy-paste mistakes early, without trying to be a full URL
// validator (fetchsvc will surface anything deeper, like DNS failures,
// as a per-source health error at fetch time).
func validateSourceURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("url is required for url-type sources")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("url is not valid: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url must start with http:// or https://")
	}
	if u.Host == "" {
		return fmt.Errorf("url must include a host")
	}
	return nil
}

func pathID(r *http.Request, param string) (int64, error) {
	v := r.PathValue(param)
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q", param, v)
	}
	return id, nil
}

func (h *Handler) redirectOK(w http.ResponseWriter, r *http.Request, msg string) {
	h.redirectTo(w, r, "/", "ok", msg)
}

func (h *Handler) redirectError(w http.ResponseWriter, r *http.Request, msg string) {
	h.redirectTo(w, r, "/", "err", msg)
}

// redirectTo redirects to path with a flash message in the query string
// (key is "ok" or "err") — the same no-session flash pattern used across
// every page.
func (h *Handler) redirectTo(w http.ResponseWriter, r *http.Request, path, key, msg string) {
	http.Redirect(w, r, path+"?"+key+"="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) serverError(w http.ResponseWriter, action string, err error) {
	log.Printf("web: %s: %v", action, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// logRenderError logs a template execution failure. By the time
// ExecuteTemplate fails, headers/partial output may already be flushed,
// so there's nothing more useful to do than log it.
func logRenderError(templateName string, err error) {
	log.Printf("web: render %s: %v", templateName, err)
}

// strconv64 parses a form field expected to be an int64 id.
func strconv64(s string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}
