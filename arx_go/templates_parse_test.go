package main

import (
	"html/template"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"arx/arx_go/models"
	"arx/internal/contacts"
	"arx/internal/inventory"
	"arx/internal/parts"
	"arx/internal/purchasing"
	"github.com/shopspring/decimal"
)

// TestParseTemplates checks parseTemplates succeeds and yields a template for
// every key the render functions look up.
func TestParseTemplates(t *testing.T) {
	m, err := parseTemplates(templatesFS)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"login", "recordsprint:record_print.html", "records:yield.html", "layout:shared/error.html", "layout:shared/not_found.html", "layout:shared/local_dir.html", "popouts"}
	for _, p := range corePrintPages {
		want = append(want, "print:"+p)
	}
	for _, dir := range coreTabDirs {
		pages, _ := fs.Glob(templatesFS, "templates/"+dir+"/*.html")
		for _, page := range pages {
			rel := strings.TrimPrefix(page, "templates/")
			if slices.Contains(corePrintPages, rel) {
				continue
			}
			want = append(want, "layout:"+rel)
		}
	}
	for _, k := range want {
		if m[k] == nil {
			t.Errorf("missing template key %q", k)
		}
	}
}

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
	applySectionBar(data) // the nav comes from MainTabs, which render() adds the same way
	return data
}

// TestContactDetailTemplateRenders renders templates/contacts/contact_detail.html
// (a representative core tab page) with fake data and asserts the bound value
// actually appears in the output, catching wrong data binding that parse-only
// checks miss (#825).
func TestContactDetailTemplateRenders(t *testing.T) {
	contact := contacts.Contact{ID: 1, DisplayName: "Acme Test Contact", Email: "acme@example.com", IsActive: true}
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
		"ActiveTab":  "suppliers",
		"SectionTab": "contacts",
		"TestMode":   false,
	})
	out := renderToString(t, tmpl, "layout", data)
	if !strings.Contains(out, "Acme Test Contact") {
		t.Errorf("contact_detail.html output missing bound contact name; got:\n%s", out)
	}
	// The nav comes from mainTabs: the Vendors tab is active and reads as the selected
	// section tab, and the section bars are laid out against the number of main tabs.
	for _, want := range []string{`--main-tabs:5`, `data-section="suppliers"`, `class="tab active"`, `>Contacts</a>`} {
		if !strings.Contains(out, want) {
			t.Errorf("layout nav missing %q", want)
		}
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
	items := []models.PurchaseOrderLine{{LineNumber: 1, PartNumberSnapshot: "PN-100", Qty: d(2), UnitCost: d(5.00)}}
	tmpl, err := template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS, "templates/pos/po_print.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := map[string]any{
		"PO":           po,
		"POItems":      items,
		"LineTotal":    d(10.0),
		"SupplierCode": "",
		"TestMode":     false,
		"POFolderPath": "",
		"IsRFQ":        false,
	}
	out := renderToString(t, tmpl, "po_print.html", data)
	// Decimal money renders through the decimal-aware printf: unit cost to 4 places, extension to 2.
	for _, want := range []string{"$5.0000", "$10.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("po_print.html output missing formatted money %q; got:\n%s", want, out)
		}
	}
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

// renderPartsList renders a parts/<page> list template with data under the shared layout.
func renderPartsList(t *testing.T, page string, data map[string]any) string {
	t.Helper()
	tmpl, err := template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS,
		"templates/shared/layout.html",
		"templates/shared/partials.html",
		"templates/parts/"+page,
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data["UserLoc"] = time.UTC
	data["ActiveTab"] = "parts"
	return renderToString(t, tmpl, "layout", coreLayoutFakeData(data))
}

// The All Lots/Units/Builds pages render their rows client-side from /api/*/rows;
// the row mapping is covered by TestLotListRows_UserZone, TestUnitListRows and
// TestBuildListRows, and the cross-links by TestFilterTable_NewPagesHaveFilterRow.
func TestAllListPagesLinkEachOther(t *testing.T) {
	for page, want := range map[string][]string{
		"all_units.html":  {`href="/lots"`, `href="/builds"`},
		"all_builds.html": {`href="/lots"`, `href="/units"`},
	} {
		out := renderPartsList(t, page, map[string]any{})
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: missing %q", page, w)
			}
		}
	}
}

// TestPopoutPartWhereUsedThumb checks the "Used in" popout (#385) carries the part's
// thumbnail when it has one and omits the <img> otherwise.
func TestPopoutPartWhereUsedThumb(t *testing.T) {
	m, err := parseTemplates(templatesFS)
	if err != nil {
		t.Fatal(err)
	}
	used := []parts.WhereUsed{{ParentPartID: 7, PartNumber: "ASM-1", Qty: decimal.NewFromInt(2)}}
	cases := []struct {
		name    string
		data    map[string]any
		wantImg bool
		want    string
	}{
		{"with thumb", map[string]any{"Thumb": "/local/t.png", "Used": used}, true, `href="/part/7"`},
		{"no thumb", map[string]any{"Thumb": "", "Used": used}, false, `href="/part/7"`},
		{"empty", map[string]any{"Thumb": "", "Used": []parts.WhereUsed(nil)}, false, "Not used in any assemblies."},
	}
	for _, c := range cases {
		var sb strings.Builder
		if err := m["popouts"].ExecuteTemplate(&sb, "popout_part_where_used", c.data); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		out := sb.String()
		if got := strings.Contains(out, `<img class="popout-thumb" src="/local/t.png"`); got != c.wantImg {
			t.Errorf("%s: img present = %v, want %v\n%s", c.name, got, c.wantImg, out)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: missing %q\n%s", c.name, c.want, out)
		}
	}
}

// TestPopoutFragmentsExecute checks the stock/PO/supplier/contact popouts (#386) render their key output.
func TestPopoutFragmentsExecute(t *testing.T) {
	m, err := parseTemplates(templatesFS)
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	price := decimal.RequireFromString("0.03")
	pid := 3002
	co := 1002
	cases := []struct {
		name, tmpl string
		data       map[string]any
		want       []string
		notWant    []string
	}{
		{"below min", "popout_part_below_min", map[string]any{"OnOrder": decimal.NewFromInt(30), "More": true,
			"Lines": []parts.OpenPOLine{{PONumber: "5003", SupplierName: "Acme", Remaining: decimal.NewFromInt(30), Expected: &exp}}},
			[]string{`href="/po/5003"`, "30 remaining", "2026-05-21", "and more"}, nil},
		{"below min empty", "popout_part_below_min", map[string]any{"OnOrder": decimal.Zero, "Lines": []parts.OpenPOLine(nil)},
			[]string{"Nothing on order."}, nil},
		{"stock", "popout_part_stock", map[string]any{"PartID": 3007, "Total": decimal.NewFromInt(16), "Unlotted": decimal.NewFromInt(-4),
			"Lots": []inventory.LotStock{{ID: 8301, LotNumber: "L-1", Qty: decimal.NewFromInt(20)}}},
			[]string{`href="/part/3007/lots/8301"`, "Not assigned to a lot"}, nil},
		{"stock no unlotted", "popout_part_stock", map[string]any{"PartID": 3007, "Total": decimal.NewFromInt(20), "Unlotted": decimal.Zero,
			"Lots": []inventory.LotStock{{ID: 8301, LotNumber: "L-1", Qty: decimal.NewFromInt(20)}}},
			[]string{"L-1"}, []string{"Not assigned to a lot"}},
		{"stock empty", "popout_part_stock", map[string]any{"PartID": 1, "Total": decimal.Zero, "Unlotted": decimal.Zero, "Lots": []inventory.LotStock(nil)},
			[]string{"No stock recorded."}, nil},
		{"bom component", "popout_part_bom_component", map[string]any{"Thumb": "", "Used": []parts.WhereUsed{{ParentPartID: 7, PartNumber: "ASM-1", Qty: decimal.NewFromInt(2)}},
			"Stock": decimal.NewFromInt(5), "Supplier": "Precision", "Price": &price},
			[]string{"Used in", "On hand: 5", "Precision", "$0.0300"}, nil},
		{"bom component no supplier", "popout_part_bom_component", map[string]any{"Thumb": "", "Used": []parts.WhereUsed(nil),
			"Stock": decimal.Zero, "Supplier": "", "Price": (*decimal.Decimal)(nil)},
			[]string{"No preferred supplier"}, nil},
		{"supplier", "popout_supplier", map[string]any{"ID": 1002, "Name": "Precision", "ContactName": "Bob Lee", "ContactPhone": "555-0201",
			"ContactEmail": "bob@x.test", "OpenCount": 1, "OpenValue": decimal.RequireFromString("72.8"), "LastOrdered": &exp},
			[]string{`href="/supplier/1002"`, "mailto:bob@x.test", "Open POs: 1", "72.80", "2026-05-21"}, nil},
		{"supplier no contact", "popout_supplier", map[string]any{"ID": 1, "Name": "X", "ContactName": "", "ContactPhone": "", "ContactEmail": "",
			"OpenCount": 0, "OpenValue": decimal.Zero, "LastOrdered": (*time.Time)(nil)},
			[]string{"No primary contact", "Last order: none"}, nil},
		{"po", "popout_po", map[string]any{"Number": "5002", "Label": "Open", "Total": decimal.RequireFromString("72.8"), "ReceivedLines": 1, "TotalLines": 2, "More": true,
			"Lines": []purchasing.POLine{{LineNumber: 1, PartNumberSnapshot: "RAW-1", PartID: &pid, Qty: decimal.NewFromInt(20)}}},
			[]string{"PO 5002", "Open", "72.80", "1 of 2 lines received", `href="/part/3002"`, "and more"}, nil},
		{"contact", "popout_contact", map[string]any{"ID": 2003, "DisplayName": "Bob Lee", "Employer": "Precision", "CompanyID": &co,
			"Phone": "555-0201", "Email": "bob@x.test", "OpenPOs": []contacts.PO{{Number: "5002", Role: "Supplier", Counterparty: "Precision"}}},
			[]string{`href="/supplier/1002"`, "mailto:bob@x.test", `href="/po/5002"`}, nil},
		{"contact no pos", "popout_contact", map[string]any{"ID": 1, "DisplayName": "A", "Employer": "", "CompanyID": (*int)(nil),
			"Phone": "", "Email": "", "OpenPOs": []contacts.PO(nil)},
			[]string{"No open POs."}, nil},
	}
	for _, c := range cases {
		var sb strings.Builder
		if err := m["popouts"].ExecuteTemplate(&sb, c.tmpl, c.data); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		out := sb.String()
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: missing %q\n%s", c.name, w, out)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(out, w) {
				t.Errorf("%s: unexpected %q", c.name, w)
			}
		}
	}
}
