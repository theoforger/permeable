package web

import (
	"embed"
	"fmt"
	"html/template"
)

//go:embed templates/*.html
var templatesFS embed.FS

// parseTemplates parses every embedded template once at startup; there is
// no runtime file I/O for templates.
func parseTemplates() (*template.Template, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return tmpl, nil
}
