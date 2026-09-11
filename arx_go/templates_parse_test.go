package main

import (
	"html/template"
	"io/fs"
	"maps"
	"strings"
	"testing"

	"arx/arx_go/models"
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

// TestSharedStandaloneTemplatesParse parses the templates/shared pages that are
// rendered on their own rather than as a tab under coreTabDirs: login.html via
// renderLogin's standalone ParseFS (no layout, no Funcs), and error.html/
// not_found.html/local_dir.html via render()'s usual layout+partials+
// coreTemplateFuncs call (files.go, handlers.go, pos.go, suppliers.go). Zero
// parse coverage here means a renamed {{define}} block would break these
// pages silently while TestCoreTemplatesParse/TestRecordsTemplatesParse stay
// green (#758).
// renderToString executes tmpl (by name) with data and returns the output,
// failing the test on any execution error.
func renderToString(t *testing.T, tmpl *template.Template, name string, data any) string {
	t.Helper()
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return buf.String()
}

// coreLayoutFakeData returns the zero-value keys render()/renderRecords() inject
// into every page (AppVersion, CurrentUser, CSRFToken, etc.) on top of extra,
// so layout.html can execute without a live Handler.
func coreLayoutFakeData(extra map[string]any) map[string]any {
	data := map[string]any{
		"AppVersion":       "",
		"SchemaMismatch":   "",
		"CurrentUser":      nil,
		"CSRFToken":        "",
		"CompanyLogo":      "",
		"AccentThemeClass": "",
		"Title":            "",
		"Favicon":          "",
		"FaviconType":      "",
	}
	maps.Copy(data, extra)
	return data
}

// TestContactDetailTemplateRenders renders templates/contacts/contact_detail.html
// (a representative core tab page) with fake data and asserts the bound value
// actually appears in the output, catching wrong data binding that parse-only
// checks miss (#825).
func TestContactDetailTemplateRenders(t *testing.T) {
	contact := models.Contact{ID: 1, DisplayName: "Acme Test Contact", Email: "acme@example.com", IsActive: true}
	tmpl, err := template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS,
		"templates/shared/layout.html",
		"templates/shared/partials.html",
		"templates/contacts/contact_detail.html",
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := coreLayoutFakeData(map[string]any{
		"Contact":   contact,
		"ActiveTab": "contacts",
		"TestMode":  false,
	})
	out := renderToString(t, tmpl, "layout", data)
	if !strings.Contains(out, "Acme Test Contact") {
		t.Errorf("contact_detail.html output missing bound contact name; got:\n%s", out)
	}
}

// TestYieldTemplateRenders renders templates/records/yield.html (a representative
// records page) with fake data and asserts bound values appear in output (#825).
func TestYieldTemplateRenders(t *testing.T) {
	form := models.TestForm{ID: 7, PartNumber: "PN-TEST-YIELD", Description: "Widget"}
	total := yieldBucket{Label: "Total", Total: 10, Passed: 8, Failed: 2}
	tmpl, err := template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS,
		"templates/shared/layout.html",
		"templates/records/yield.html",
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := coreLayoutFakeData(map[string]any{
		"Form":      form,
		"Total":     total,
		"Monthly":   nil,
		"Grouped":   false,
		"FromStr":   "",
		"ToStr":     "",
		"ActiveTab": "records",
		"TestMode":  false,
	})
	out := renderToString(t, tmpl, "layout", data)
	if !strings.Contains(out, "PN-TEST-YIELD") {
		t.Errorf("yield.html output missing bound form part number; got:\n%s", out)
	}
	if !strings.Contains(out, `<span class="fs-3 fw-bold text-success">8</span>`) {
		t.Errorf("yield.html output missing bound Passed count in its card; got:\n%s", out)
	}
}

// TestPOPrintTemplateRenders renders templates/pos/po_print.html (the only core-tab
// print template) with fake data and asserts bound values appear in output (#825).
func TestPOPrintTemplateRenders(t *testing.T) {
	po := models.PurchaseOrder{Number: "PO-TEST-100", Status: "open", SupplierName: "Acme Supply"}
	items := []models.PurchaseOrderLine{{LineNumber: 1, PartNumberSnapshot: "PN-100", Qty: 2, UnitCost: 5.00}}
	tmpl, err := template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS, "templates/pos/po_print.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := map[string]any{
		"PO":           po,
		"POItems":      items,
		"LineTotal":    10.0,
		"SupplierCode": "",
		"TestMode":     false,
		"POFolderPath": "",
		"IsRFQ":        false,
	}
	out := renderToString(t, tmpl, "po_print.html", data)
	if !strings.Contains(out, "PO-TEST-100") {
		t.Errorf("po_print.html output missing bound PO number; got:\n%s", out)
	}
	if !strings.Contains(out, "PN-100") {
		t.Errorf("po_print.html output missing bound line part number; got:\n%s", out)
	}
}

// TestLoginTemplateRenders renders templates/shared/login.html (a representative
// shared standalone page) with fake data and asserts bound values appear in
// output (#825).
func TestLoginTemplateRenders(t *testing.T) {
	tmpl, err := template.New("").ParseFS(templatesFS, "templates/shared/login.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := map[string]any{
		"DefaultUsername": "test.user",
		"Bootstrap":       false,
		"CSRFToken":       "tok123",
	}
	out := renderToString(t, tmpl, "login.html", data)
	if !strings.Contains(out, `value="test.user"`) {
		t.Errorf("login.html output missing bound default username; got:\n%s", out)
	}
}

func TestSharedStandaloneTemplatesParse(t *testing.T) {
	if _, err := template.New("").ParseFS(templatesFS, "templates/shared/login.html"); err != nil {
		t.Errorf("parse login.html: %v", err)
	}
	for _, page := range []string{"error.html", "not_found.html", "local_dir.html"} {
		if _, err := template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS,
			"templates/shared/layout.html",
			"templates/shared/partials.html",
			"templates/shared/"+page,
		); err != nil {
			t.Errorf("parse %s: %v", page, err)
		}
	}
}
