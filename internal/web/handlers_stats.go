package web

import (
	"net/http"

	"permeable/internal/db"
	"permeable/internal/model"
)

// statsPageData is the data passed to templates/stats.html. Everything
// here is read from feed_cache/sources as last written by the most
// recent run — nothing is computed on request.
type statsPageData struct {
	HasGenerated          bool
	GeneratedAt           string
	TotalEventsPreFilter  int
	TotalEventsPostFilter int
	SourcesOK             int
	SourcesError          int
	EffectiveTTLMinutes   int
	Sources               []model.Source
}

func (h *Handler) handleStatsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sources, err := db.ListSources(ctx, h.db)
	if err != nil {
		h.serverError(w, "list sources", err)
		return
	}

	data := statsPageData{Sources: sources}

	fc, ok, err := db.GetFeedCache(ctx, h.db)
	if err != nil {
		h.serverError(w, "get feed cache", err)
		return
	}
	if ok {
		data.HasGenerated = true
		data.GeneratedAt = fc.GeneratedAt.Local().Format("2006-01-02 15:04:05 MST")
		data.TotalEventsPreFilter = fc.TotalEventsPreFilter
		data.TotalEventsPostFilter = fc.TotalEventsPostFilter
		data.SourcesOK = fc.SourcesOK
		data.SourcesError = fc.SourcesError
		data.EffectiveTTLMinutes = fc.EffectiveTTLMinutes
	}

	if err := h.tmpl.ExecuteTemplate(w, "stats.html", data); err != nil {
		logRenderError("stats.html", err)
	}
}

// handleHealthz is a plain up/DB-reachable check for Docker healthcheck —
// deliberately minimal, no feed-state opinion.
func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := h.db.PingContext(r.Context()); err != nil {
		http.Error(w, "db unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}
