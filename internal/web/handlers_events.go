package web

import (
	"net/http"
	"sort"
	"strings"

	"permeable/internal/db"
	"permeable/internal/fetchsvc"
	"permeable/internal/filtersvc"
	"permeable/internal/model"
)

// eventDecisionView adds display fields to a filtersvc.Decision.
type eventDecisionView struct {
	filtersvc.Decision
	SourceName     string
	OccurrenceDate string // YYYY-MM-DD, matches event_exceptions.occurrence_date
}

type ruleView struct {
	model.EventRule
	SourceName string
}

type exceptionView struct {
	model.EventException
	SourceName string
}

// eventsPageData is the data passed to templates/events.html.
type eventsPageData struct {
	Decisions  []eventDecisionView
	Rules      []ruleView
	Exceptions []exceptionView
	Error      string
	Message    string
}

// handleEventsPage runs a live fetch+expand+resolve (there's no
// background scheduler or feed cache yet — those land in Stages 7 and 9)
// and shows every occurrence, included or not, so users can act on
// events that are currently excluded too.
func (h *Handler) handleEventsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sources, err := db.ListSources(ctx, h.db)
	if err != nil {
		h.serverError(w, "list sources", err)
		return
	}
	sourceNames := make(map[int64]string, len(sources))
	sourcesByID := make(map[int64]model.Source, len(sources))
	for _, s := range sources {
		sourceNames[s.ID] = s.Name
		sourcesByID[s.ID] = s
	}

	result, err := fetchsvc.Run(ctx, h.db)
	if err != nil {
		h.serverError(w, "fetch sources", err)
		return
	}

	filters, err := db.ListFilters(ctx, h.db)
	if err != nil {
		h.serverError(w, "list filters", err)
		return
	}
	df, err := db.GetDurationFilters(ctx, h.db)
	if err != nil {
		h.serverError(w, "get duration filters", err)
		return
	}
	rules, err := db.ListEventRules(ctx, h.db)
	if err != nil {
		h.serverError(w, "list event rules", err)
		return
	}
	exceptions, err := db.ListEventExceptions(ctx, h.db)
	if err != nil {
		h.serverError(w, "list event exceptions", err)
		return
	}

	decisions := filtersvc.ResolveAll(result.Events, filtersvc.Input{
		Sources: sourcesByID, Rules: rules, Exceptions: exceptions, Filters: filters, Duration: df,
	})
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].Event.Start.Before(decisions[j].Event.Start) })

	data := eventsPageData{
		Error:   r.URL.Query().Get("err"),
		Message: r.URL.Query().Get("ok"),
	}
	for _, d := range decisions {
		data.Decisions = append(data.Decisions, eventDecisionView{
			Decision:       d,
			SourceName:     sourceNames[d.Event.SourceID],
			OccurrenceDate: d.Event.Start.UTC().Format("2006-01-02"),
		})
	}
	for _, ru := range rules {
		data.Rules = append(data.Rules, ruleView{EventRule: ru, SourceName: sourceNames[ru.SourceID]})
	}
	for _, ex := range exceptions {
		data.Exceptions = append(data.Exceptions, exceptionView{EventException: ex, SourceName: sourceNames[ex.SourceID]})
	}

	if err := h.tmpl.ExecuteTemplate(w, "events.html", data); err != nil {
		logRenderError("events.html", err)
	}
}

// handleCreateEventException hides a single occurrence ("hide this
// occurrence").
func (h *Handler) handleCreateEventException(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.redirectTo(w, r, "/events", "err", "could not parse form: "+err.Error())
		return
	}

	sourceID, err := strconv64(r.FormValue("source_id"))
	if err != nil {
		h.redirectTo(w, r, "/events", "err", "invalid source_id")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	occurrenceDate := strings.TrimSpace(r.FormValue("occurrence_date"))
	if title == "" || occurrenceDate == "" {
		h.redirectTo(w, r, "/events", "err", "title and occurrence_date are required")
		return
	}

	err = db.CreateEventException(r.Context(), h.db, sourceID, model.NormalizeTitle(title), occurrenceDate)
	if err != nil {
		h.serverError(w, "create event exception", err)
		return
	}
	h.redirectTo(w, r, "/events", "ok", "occurrence hidden")
}

func (h *Handler) handleDeleteEventException(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.redirectTo(w, r, "/events", "err", err.Error())
		return
	}
	if err := db.DeleteEventException(r.Context(), h.db, id); err != nil {
		h.serverError(w, "delete event exception", err)
		return
	}
	h.redirectTo(w, r, "/events", "ok", "exception removed")
}

// handleUpsertEventRule sets ("hide all future like this" / "always show
// future like this") a per-source, per-title rule.
func (h *Handler) handleUpsertEventRule(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.redirectTo(w, r, "/events", "err", "could not parse form: "+err.Error())
		return
	}

	sourceID, err := strconv64(r.FormValue("source_id"))
	if err != nil {
		h.redirectTo(w, r, "/events", "err", "invalid source_id")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	action := model.FilterAction(r.FormValue("action"))
	if title == "" {
		h.redirectTo(w, r, "/events", "err", "title is required")
		return
	}
	if action != model.FilterActionInclude && action != model.FilterActionExclude {
		h.redirectTo(w, r, "/events", "err", "action must be include or exclude")
		return
	}

	err = db.UpsertEventRule(r.Context(), h.db, sourceID, model.NormalizeTitle(title), action)
	if err != nil {
		h.serverError(w, "upsert event rule", err)
		return
	}
	h.redirectTo(w, r, "/events", "ok", "rule saved")
}

func (h *Handler) handleDeleteEventRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.redirectTo(w, r, "/events", "err", err.Error())
		return
	}
	if err := db.DeleteEventRule(r.Context(), h.db, id); err != nil {
		h.serverError(w, "delete event rule", err)
		return
	}
	h.redirectTo(w, r, "/events", "ok", "rule removed")
}
