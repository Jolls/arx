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
			"templates/shared/layout.html",
			"templates/pm/partials.html",
			page,
		); err != nil {
			t.Errorf("parse %s: %v", base, err)
		}
	}
}

// TestTRTemplatesParse parses every tr page template the way renderTR does (with layout),
// and standalone print templates the way renderPrintTR does.
func TestTRTemplatesParse(t *testing.T) {
	pages, err := fs.Glob(templatesFS, "templates/tr/*.html")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, page := range pages {
		base := strings.TrimPrefix(page, "templates/tr/")
		if base == "layout.html" {
			continue
		}
		if strings.HasSuffix(base, "_print.html") {
			if _, err := template.New("").Funcs(trTemplateFuncs()).ParseFS(templatesFS, page); err != nil {
				t.Errorf("parse %s: %v", base, err)
			}
			continue
		}
		if _, err := template.New("").Funcs(trTemplateFuncs()).ParseFS(templatesFS,
			"templates/shared/layout.html", page,
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
