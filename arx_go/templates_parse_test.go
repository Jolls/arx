package main

import (
	"html/template"
	"io/fs"
	"strings"
	"testing"
)

// TestPMTemplatesParse parses every pm page template together with the layout
// and partials, catching missing/renamed template definitions (e.g. shared
// partials referenced via {{template ...}}) before they fail at render time.
func TestPMTemplatesParse(t *testing.T) {
	pages, err := fs.Glob(templatesFS, "templates/pm/*.html")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, page := range pages {
		base := page[len("templates/pm/"):]
		if base == "layout.html" || base == "partials.html" {
			continue
		}
		if _, err := template.New("").Funcs(pmTemplateFuncs()).ParseFS(templatesFS,
			"templates/pm/layout.html",
			"templates/pm/partials.html",
			page,
		); err != nil {
			t.Errorf("parse %s: %v", base, err)
		}
	}
}

// TestPMPrintTemplatesParse parses standalone print templates the way renderPrint does.
func TestPMPrintTemplatesParse(t *testing.T) {
	pages, err := fs.Glob(templatesFS, "templates/pm/*_print.html")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, page := range pages {
		base := strings.TrimPrefix(page, "templates/pm/")
		if _, err := template.New("").Funcs(pmTemplateFuncs()).ParseFS(templatesFS, page); err != nil {
			t.Errorf("parse %s: %v", base, err)
		}
	}
}
