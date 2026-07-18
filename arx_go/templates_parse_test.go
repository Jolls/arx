package main

import (
	"html/template"
	"io/fs"
	"strings"
	"testing"
)

// coreTabDirs are the tab-based template subfolders rendered via render()/renderPrint()
// (i.e. everything except templates/shared and templates/records).
var coreTabDirs = []string{"parts", "suppliers", "pos", "contacts", "settings", "reports"}

// TestCoreTemplatesParse parses every core page template together with the layout
// and partials, catching missing/renamed template definitions (e.g. shared
// partials referenced via {{template ...}}) before they fail at render time.
func TestCoreTemplatesParse(t *testing.T) {
	for _, dir := range coreTabDirs {
		pages, err := fs.Glob(templatesFS, "templates/"+dir+"/*.html")
		if err != nil {
			t.Fatalf("glob: %v", err)
		}
		for _, page := range pages {
			base := strings.TrimPrefix(page, "templates/")
			if strings.HasSuffix(base, "_print.html") {
				continue
			}
			if _, err := template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS,
				"templates/shared/layout.html",
				"templates/shared/partials.html",
				page,
			); err != nil {
				t.Errorf("parse %s: %v", base, err)
			}
		}
	}
}

// TestRecordsTemplatesParse parses every records page template the way renderRecords does
// (with layout), and standalone print templates the way renderPrintRecords does.
func TestRecordsTemplatesParse(t *testing.T) {
	pages, err := fs.Glob(templatesFS, "templates/records/*.html")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, page := range pages {
		base := strings.TrimPrefix(page, "templates/records/")
		if base == "layout.html" {
			continue
		}
		if strings.HasSuffix(base, "_print.html") {
			if _, err := template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS, page); err != nil {
				t.Errorf("parse %s: %v", base, err)
			}
			continue
		}
		if _, err := template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS,
			"templates/shared/layout.html", page,
		); err != nil {
			t.Errorf("parse %s: %v", base, err)
		}
	}
}

// TestCorePrintTemplatesParse parses standalone print templates the way renderPrint does.
func TestCorePrintTemplatesParse(t *testing.T) {
	for _, dir := range coreTabDirs {
		pages, err := fs.Glob(templatesFS, "templates/"+dir+"/*_print.html")
		if err != nil {
			t.Fatalf("glob: %v", err)
		}
		for _, page := range pages {
			base := strings.TrimPrefix(page, "templates/")
			if _, err := template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS, page); err != nil {
				t.Errorf("parse %s: %v", base, err)
			}
		}
	}
}
