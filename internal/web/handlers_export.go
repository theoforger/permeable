package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"permeable/internal/exportsvc"
	"permeable/internal/model"
)

// handleConfigExport streams the full config as a downloadable JSON
// file.
func (h *Handler) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	bundle, err := exportsvc.Export(r.Context(), h.db)
	if err != nil {
		h.serverError(w, "export config", err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="permeable-export.json"`)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(bundle); err != nil {
		log.Printf("web: encode export: %v", err)
	}
}

// handleConfigImport restores a previously exported JSON file, in
// replace or merge mode per the form's radio selection.
func (h *Handler) handleConfigImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		h.redirectError(w, r, "could not parse upload: "+err.Error())
		return
	}

	mode := exportsvc.Mode(r.FormValue("mode"))
	if mode != exportsvc.ModeReplace && mode != exportsvc.ModeMerge {
		h.redirectError(w, r, "mode must be replace or merge")
		return
	}

	file, _, err := r.FormFile("import_file")
	if err != nil {
		h.redirectError(w, r, "no file uploaded")
		return
	}
	defer file.Close()

	var bundle model.ExportBundle
	if err := json.NewDecoder(file).Decode(&bundle); err != nil {
		h.redirectError(w, r, "could not parse import file: "+err.Error())
		return
	}

	stats, err := exportsvc.Import(r.Context(), h.db, bundle, mode)
	if err != nil {
		h.serverError(w, "import config", err)
		return
	}
	// A config import (especially "replace") is destructive and rare
	// enough to be worth a server-side record, not just a flash message
	// only the person watching the browser at that moment ever sees.
	log.Printf("web: import (%s) complete: %d source(s), %d manual event(s), %d filter(s), %d rule(s), %d exception(s)",
		mode, stats.Sources, stats.ManualEvents, stats.Filters, stats.EventRules, stats.EventExceptions)

	if h.scheduler != nil {
		h.scheduler.Reload() // config/duration_filters may have changed (replace mode)
	}

	h.redirectOK(w, r, fmt.Sprintf(
		"import (%s) complete: %d source(s), %d manual event(s), %d filter(s), %d rule(s), %d exception(s)",
		mode, stats.Sources, stats.ManualEvents, stats.Filters, stats.EventRules, stats.EventExceptions))
}
