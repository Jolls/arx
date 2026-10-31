package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// routeBudget is one row of the round-trip budget table (#430): a registered GET route, a
// concrete request that dispatches to it, and the most critical-path round trips it may use.
type routeBudget struct {
	name     string
	pattern  string // the GET route pattern (as registered, no method) this row covers
	fn       http.HandlerFunc
	target   string // concrete request URI; must dispatch to pattern
	status   int    // expected status; 0 means 200
	wantBody string // substring of the body; "" = status only
	budget   int    // max critical-path round trips
}

// seedAdminUserID is the admin in SQL/seed_test_data.sql; the budget harness puts the
// real user on the request context, as RequireAuth does.
const seedAdminUserID = 8001

func row(name, pattern string, fn http.HandlerFunc, target, wantBody string, budget int) routeBudget {
	return routeBudget{name: name, pattern: pattern, fn: fn, target: target, wantBody: wantBody, budget: budget}
}

// routeBudgets is the budget table. Budgets are the measured count: raising one is a
// deliberate decision, lowering one locks in a win.
func routeBudgets(h *Handler) []routeBudget {
	return []routeBudget{
		row("parts list", "/parts", h.PartsList, "/parts", `id="parts-table"`, 0),
		row("part detail", "/part/{id}/summary", h.PartDetail, "/part/3005/summary", "ASM-1001", 2),
		row("part BOM", "/part/{id}/bom", h.PartBOM, "/part/3005/bom", "ASM-1001", 1),
		row("part build-cost", "/part/{id}/build-cost", h.PartBuildCost, "/part/3005/build-cost?qty=1", "ASM-1001", 4),
		row("part price-history", "/part/{id}/price-history", h.PartPriceHistory, "/part/3002/price-history", "BUY-1001", 2),
		row("part orders", "/part/{id}/orders", h.PartOrders, "/part/3005/orders", "ASM-1001", 1),
		row("suppliers list", "/suppliers", h.SuppliersList, "/suppliers", `data-rows-url="/api/suppliers/rows"`, 0),
		row("supplier detail", "/supplier/{id}", h.SupplierDetail, "/supplier/1001", "Acme Fasteners", 4),
		row("PO list", "/pos", h.POList, "/pos", `data-rows-url="/api/pos/rows"`, 0),
		row("PO detail", "/po/{id}", h.PODetail, "/po/5002", "PO #5002", 2),
		row("contacts list", "/contacts", h.ContactsList, "/contacts", `data-rows-url="/api/contacts/rows"`, 0),
		row("records/forms list", "/records", h.FormsList, "/records", `data-rows-url="/api/forms/rows"`, 0),
		row("records yield summary", "/forms/{id}/yield", h.RecordsYieldSummary, "/forms/6001/yield", "FORM-1001", 2),
		row("reports yield picker", "/reports/yield", h.ReportsYieldPicker, "/reports/yield", "FORM-1001", 1),

		// Row feeds: the list pages render an empty shell and the table fills from these
		// fetches, so each is a second request on every tab switch.
		row("parts rows", "/api/parts/rows", h.PartsRows, "/api/parts/rows", "ASM-1001", 1),
		row("suppliers rows", "/api/suppliers/rows", h.SuppliersRows, "/api/suppliers/rows", "Acme Fasteners", 1),
		row("PO rows", "/api/pos/rows", h.PORows, "/api/pos/rows", "5002", 1),
		row("contacts rows", "/api/contacts/rows", h.ContactsRows, "/api/contacts/rows", "John Doe", 1),
		row("forms rows", "/api/forms/rows", h.FormsRows, "/api/forms/rows", "FORM-1001", 1),
		row("lots rows", "/api/lots/rows", h.AllLotsRows, "/api/lots/rows", "8301", 1),
		row("units rows", "/api/units/rows", h.AllUnitsRows, "/api/units/rows", "SN-3005-001", 1),
		row("builds rows", "/api/builds/rows", h.AllBuildsRows, "/api/builds/rows", "8201", 1),
		row("form records rows", "/api/forms/{id}/records/rows", h.RecordsRows, "/api/forms/6001/records/rows", "7001", 1),

		// Other landing / section-tab pages.
		row("lots list", "/lots", h.AllLots, "/lots", "", 0),
		row("units list", "/units", h.AllUnits, "/units", "", 0),
		row("builds list", "/builds", h.AllBuilds, "/builds", "", 0),
		row("RFQ list", "/rfqs", h.RFQList, "/rfqs", "", 0),
		row("reports dashboard", "/reports", h.ReportsDashboard, "/reports", "", 2),
		row("reports spend", "/reports/spend", h.ReportsSpend, "/reports/spend", "", 2),
		row("reports on-time", "/reports/on-time", h.ReportsOnTime, "/reports/on-time", "", 1),
		row("reports cycle-time", "/reports/cycle-time", h.ReportsCycleTime, "/reports/cycle-time", "", 1),
		row("reports data-quality", "/reports/data-quality", h.ReportsDataQuality, "/reports/data-quality", "", 3),
		row("reports failure-modes picker", "/reports/failure-modes", h.ReportsFailureModesPicker, "/reports/failure-modes", "FORM-1001", 1),

		// Detail pages and detail tabs.
		row("part details", "/part/{id}/details", h.PartDetails, "/part/3005/details", "ASM-1001", 2),
		row("part where-used", "/part/{id}/where-used", h.PartWhereUsed, "/part/3002/where-used", "BUY-1001", 1),
		row("part attachments", "/part/{id}/attachments", h.PartAttachments, "/part/3005/attachments", "ASM-1001", 4),
		row("part records", "/part/{id}/records", h.PartRecords, "/part/3005/records", "ASM-1001", 1),
		row("part transactions", "/part/{id}/transactions", h.PartTransactions, "/part/3005/transactions", "ASM-1001", 1),
		row("part build", "/part/{id}/build", h.PartBuild, "/part/3005/build", "ASM-1001", 3),
		row("part lots", "/part/{id}/lots", h.PartLots, "/part/3012/lots", "", 2),
		row("part lot trace", "/part/{id}/lots/{lotID}", h.PartLotTrace, "/part/3012/lots/8302", "", 4),
		row("part units", "/part/{id}/units", h.PartUnits, "/part/3005/units", "", 1),
		row("part unit trace", "/part/{id}/units/{unitID}", h.PartUnitTrace, "/part/3005/units/8503", "", 4),
		row("part pricing", "/part/{id}/pricing", h.PartPricing, "/part/3002/pricing", "", 2),
		row("part mfg-parts", "/part/{id}/mfg-parts", h.PartMfgParts, "/part/3002/mfg-parts", "", 3),
		row("part sourcing", "/part/{id}/suppliers", h.PartSourcing, "/part/3002/suppliers", "", 4),
		row("supplier parts", "/supplier/{id}/parts", h.SupplierParts, "/supplier/1001/parts", "Acme Fasteners", 3),
		row("supplier POs", "/supplier/{id}/pos", h.SupplierPOs, "/supplier/1001/pos", "Acme Fasteners", 2),
		row("supplier attachments", "/supplier/{id}/attachments", h.SupplierAttachments, "/supplier/1001/attachments", "Acme Fasteners", 2),
		row("contact detail", "/contact/{id}", h.ContactDetail, "/contact/2001", "John Doe", 3),
		row("form records list", "/forms/{id}/records", h.RecordsList, "/forms/6001/records", "FORM-1001", 2),
		row("form failure-modes", "/forms/{id}/failure-modes", h.RecordsFailureModes, "/forms/6001/failure-modes", "FORM-1001", 2),
		row("form def", "/forms/{id}/def", h.FormDef, "/forms/6001/def", "FORM-1001", 3),
		row("record detail", "/records/{id}", h.RecordDetail, "/records/7001", "", 2),

		// Auth-gated and remaining pages (#430): Settings, popouts, /find, edit forms, CSV
		// exports and the other /api/* endpoints, run as the seeded admin.
		row("login", "/login", h.LoginGet, "/login", "", 1),
		row("settings", "/settings", h.Settings, "/settings", "tester", 8),
		row("whats new", "/whats-new", h.WhatsNew, "/whats-new", "", 0),
		row("settings backup", "/settings/backup", h.SettingsBackup, "/settings/backup", "", 31),
		row("settings utilities", "/settings/utilities", h.UtilitiesReport, "/settings/utilities", "", 8),
		row("spend by supplier CSV", "/reports/spend/export-suppliers.csv", h.ReportsSpendBySupplierExportCSV, "/reports/spend/export-suppliers.csv", "", 1),
		row("spend by part CSV", "/reports/spend/export-parts.csv", h.ReportsSpendByPartExportCSV, "/reports/spend/export-parts.csv", "", 1),
		row("on-time CSV", "/reports/on-time/export.csv", h.ReportsOnTimeExportCSV, "/reports/on-time/export.csv", "", 1),
		row("cycle-time CSV", "/reports/cycle-time/export.csv", h.ReportsCycleTimeExportCSV, "/reports/cycle-time/export.csv", "", 1),
		row("data-quality no-attachments CSV", "/reports/data-quality/export-no-attachments.csv", h.ReportsDataQualityNoAttachmentsExportCSV, "/reports/data-quality/export-no-attachments.csv", "", 1),
		row("data-quality missing-supplier CSV", "/reports/data-quality/export-missing-supplier.csv", h.ReportsDataQualityMissingSupplierExportCSV, "/reports/data-quality/export-missing-supplier.csv", "", 1),
		row("data-quality stale-rollup CSV", "/reports/data-quality/export-stale-rollup.csv", h.ReportsDataQualityStaleRollupExportCSV, "/reports/data-quality/export-stale-rollup.csv", "", 1),
		{name: "root redirect", pattern: "/{$}", fn: h.RootRedirect, target: "/", status: http.StatusSeeOther, budget: 0},
		row("parts new", "/parts/new", h.PartsNew, "/parts/new", "", 1),
		row("parts export CSV", "/parts/export.csv", h.PartsExportCSV, "/parts/export.csv", "ASM-1001", 1),
		row("find, no query", "/find", h.Find, "/find", "", 0),
		{name: "find, one match", pattern: "/find", fn: h.Find, target: "/find?q=8503", status: http.StatusFound, budget: 1},
		{name: "part detail, BOM redirect", pattern: "/part/{id}", fn: h.PartDetail, target: "/part/3005", status: http.StatusFound, budget: 1},
		row("part edit", "/part/{id}/edit", h.PartEdit, "/part/3005/edit", "ASM-1001", 3),
		row("part duplicate", "/part/{id}/duplicate", h.PartDuplicate, "/part/3005/duplicate", "ASM-1001", 2),
		row("part BOM edit", "/part/{id}/bom/edit", h.PartBOMEdit, "/part/3005/bom/edit", "ASM-1001", 3),
		row("part BOM CSV", "/part/{id}/bom/export.csv", h.BOMExportCSV, "/part/3005/bom/export.csv", "BUY-1001", 2),
		row("create RFQs, form", "/part/{id}/create-rfqs", h.PartCreateRFQs, "/part/3005/create-rfqs", "ASM-1001", 1),
		row("create RFQs, with plan", "/part/{id}/create-rfqs", h.PartCreateRFQs, "/part/3005/create-rfqs?n=10", "ASM-1001", 5),
		row("attachment where-used", "/attachments/where-used", h.AttachmentWhereUsed, "/attachments/where-used?file=https%3A%2F%2Fexample.com%2Fdatasheets%2Fm3x8-shcs.pdf", "m3x8-shcs.pdf", 1),
		row("attachment name", "/api/part/{id}/attachment-name", h.APIPartAttachmentName, "/api/part/3005/attachment-name?rev=A&category=Datasheet&ext=pdf", "ASM-1001", 1),
		row("part records rows", "/api/part/{id}/records/rows", h.PartRecordsRows, "/api/part/3004/records/rows", "7001", 1),
		row("lot edit", "/part/{id}/lots/{lotID}/edit", h.LotEdit, "/part/3012/lots/8302/edit", "/part/3012/lots/8302", 2),
		row("lot records rows", "/api/part/{id}/lots/{lotID}/records/rows", h.LotRecordsRows, "/api/part/3012/lots/8302/records/rows", "7010", 1),
		row("lot sources", "/api/part/{id}/lots/{lotID}/sources", h.APILotSources, "/api/part/3012/lots/8302/sources", "", 2),
		row("unit new", "/part/{id}/units/new", h.UnitNew, "/part/3005/units/new", "ASM-1001", 2),
		row("unit edit", "/part/{id}/units/{unitID}/edit", h.UnitEdit, "/part/3005/units/8503/edit", "", 3),
		row("unit records rows", "/api/part/{id}/units/{unitID}/records/rows", h.UnitRecordsRows, "/api/part/3005/units/8503/records/rows", "7011", 1),
		row("price new", "/part/{id}/pricing/new", h.PriceNew, "/part/3002/pricing/new", "BUY-1001", 1),
		row("price edit", "/part/{id}/pricing/{priceID}/edit", h.PriceEdit, "/part/3002/pricing/4203/edit", "BUY-1001", 2),
		row("mfg part edit", "/part/{id}/mfg-parts/{mid}/edit", h.MfgPartEdit, "/part/3002/mfg-parts/4101/edit", "CX-4471", 5),
		row("supplier part edit", "/part/{id}/suppliers/{spID}/edit", h.SupplierPartEdit, "/part/3002/suppliers/4002/edit", "PMC-M3X8", 6),
		row("part local attachments", "/api/part/{id}/local-attachments", h.APIPartLocalAttachments, "/api/part/3002/local-attachments", "", 1),
		row("part BOM children", "/api/part/{id}/bom-children", h.APIPartBOMChildren, "/api/part/3005/bom-children", "", 1),
		row("popout where-used", "/popout/part/{id}/where-used", h.PopoutPartWhereUsed, "/popout/part/3002/where-used", "ASM-1001", 2),
		row("popout lot consumers", "/popout/part/{id}/lots/{lotID}/consumers", h.PopoutLotConsumers, "/popout/part/3007/lots/8301/consumers", "", 1),
		row("popout unit", "/popout/unit/{id}", h.PopoutUnit, "/popout/unit/8503", "", 4),
		row("popout build", "/popout/build/{id}", h.PopoutBuild, "/popout/build/8201", "", 3),
		row("popout thumb", "/popout/part/{id}/thumb", h.PopoutPartThumb, "/popout/part/3002/thumb", "", 1),
		row("popout below-min", "/popout/part/{id}/below-min", h.PopoutPartBelowMin, "/popout/part/3001/below-min", "", 1),
		row("popout stock", "/popout/part/{id}/stock", h.PopoutPartStock, "/popout/part/3007/stock", "", 3),
		row("popout bom-component", "/popout/part/{id}/bom-component", h.PopoutPartBOMComponent, "/popout/part/3002/bom-component", "", 4),
		row("popout supplier", "/popout/supplier/{id}", h.PopoutSupplier, "/popout/supplier/1001", "", 2),
		row("popout PO", "/popout/po/{id}", h.PopoutPO, "/popout/po/5002", "5002", 2),
		row("popout contact", "/popout/contact/{id}", h.PopoutContact, "/popout/contact/2001", "John Doe", 2),
		row("suppliers new", "/suppliers/new", h.SuppliersNew, "/suppliers/new", "", 0),
		row("supplier edit", "/supplier/{id}/edit", h.SupplierEdit, "/supplier/1001/edit", "Acme Fasteners", 2),
		row("contacts new", "/contacts/new", h.ContactsNew, "/contacts/new", "", 0),
		row("contact edit", "/contact/{id}/edit", h.ContactEdit, "/contact/2001/edit", "John Doe", 1),
		row("PO new", "/pos/new", h.PONew, "/pos/new", "", 2),
		row("RFQ new", "/rfqs/new", h.RFQNew, "/rfqs/new", "", 2),
		row("PO export CSV", "/pos/export.csv", h.POsExportCSV, "/pos/export.csv", "5002", 1),
		row("RFQ rows", "/api/rfqs/rows", h.RFQRows, "/api/rfqs/rows", "5010", 1),
		row("RFQ add supplier", "/rfq/{id}/add-supplier", h.RFQAddSupplier, "/rfq/5010R1/add-supplier", "", 3),
		row("RFQ compare", "/rfq/{group}/compare", h.RFQCompare, "/rfq/5010/compare", "", 1),
		row("PO edit", "/po/{id}/edit", h.POEdit, "/po/5002/edit", "5002", 4),
		row("PO note", "/po/{id}/note", h.PONote, "/po/5002/note", "5002", 1),
		row("PO print", "/po/{id}/print", h.POPrint, "/po/5002/print", "5002", 3),
		row("PO duplicate", "/po/{id}/duplicate", h.PODuplicate, "/po/5002/duplicate", "5002", 4),
		row("PO start RFQ", "/po/{id}/start-rfq", h.POStartRFQ, "/po/5001/start-rfq", "", 4),
		row("API supplier search", "/api/suppliers/search", h.APISupplierSearch, "/api/suppliers/search?q=Acme", "Acme Fasteners", 1),
		row("API supplier contacts", "/api/suppliers/{id}/contacts", h.APISupplierContacts, "/api/suppliers/1001/contacts", "John Doe", 1),
		row("API part search", "/api/parts/search", h.APIPartSearch, "/api/parts/search?q=ASM", "ASM-1001", 1),
		row("API part next-number", "/api/parts/next-number", h.PartsNextNumber, "/api/parts/next-number", "", 2),
		row("API supplier-part", "/api/supplier-part", h.APISupplierPN, "/api/supplier-part?part_id=3002&supplier_id=1002", "PMC-M3X8", 1),
		row("named query", "/api/named-query", h.APINamedQuery, "/api/named-query?spec=query%3Aparts_matching%28%40pattern%3DRAW-%25%29", "RAW-1001", 2),
		row("form new record", "/forms/{id}/records/new", h.NewRecord, "/forms/6001/records/new", "FORM-1001", 3),
		row("form def edit", "/forms/{id}/def/edit", h.EditFormDef, "/forms/6001/def/edit", "", 3),
		row("form duplicate", "/forms/{id}/duplicate", h.DuplicateForm, "/forms/6001/duplicate", "", 3),
		row("form new", "/forms/new", h.NewForm, "/forms/new", "", 2),
		row("def history", "/api/forms/{id}/def/history", h.FormDefHistory, "/api/forms/6001/def/history?at=2026-05-28", "", 1),
		row("test report", "/forms/{id}/tests/{testID}/report", h.TestReport, "/forms/6001/tests/6102/report", "", 2),
		row("test report rows", "/api/forms/{id}/tests/{testID}/report/rows", h.TestReportRows, "/api/forms/6001/tests/6102/report/rows", "", 2),
		row("record print", "/records/{id}/print", h.RecordPrint, "/records/7001/print", "", 4),
		row("record edit", "/records/{id}/edit", h.EditRecord, "/records/7001/edit", "", 7),
	}
}

// routeBudgetExemptions lists GET routes that have no budget row, keyed "GET <pattern>",
// each with the reason.
var routeBudgetExemptions = map[string]string{
	"GET /static/{rest...}": "embedded static assets, no DB",
	"GET /static":           "embedded static assets, no DB",

	// File serving: fileServingPrefixes (handlers.go) already treats these as DB-free.
	"GET /local/{rest...}":              "serves files from disk",
	"GET /local":                        "serves files from disk",
	"GET /local-dir/{rest...}":          "serves files from disk",
	"GET /local-dir":                    "serves files from disk",
	"GET /supplier-local/{rest...}":     "serves files from disk",
	"GET /supplier-local":               "serves files from disk",
	"GET /supplier-local-dir/{rest...}": "serves files from disk",
	"GET /supplier-local-dir":           "serves files from disk",
	"GET /images/{rest...}":             "serves files from disk",
	"GET /images":                       "serves files from disk",

	"GET /api/browse-folder":  "opens a native OS folder-picker dialog; cannot run headless; no DB",
	"GET /api/digikey/lookup": "calls the external DigiKey API",

	// Result depends on the configured filesystem roots; the DB cost is one entity lookup,
	// already budgeted by the detail rows.
	"GET /supplier/{id}/folder/{rest...}": "depends on configured filesystem roots",
	"GET /supplier/{id}/folder":           "depends on configured filesystem roots",
	"GET /supplier/{id}/file/{rest...}":   "depends on configured filesystem roots",
	"GET /supplier/{id}/file":             "depends on configured filesystem roots",
	"GET /po/{id}/folder/{rest...}":       "depends on configured filesystem roots",
	"GET /po/{id}/folder":                 "depends on configured filesystem roots",
	"GET /po/{id}/file/{rest...}":         "depends on configured filesystem roots",
	"GET /po/{id}/file":                   "depends on configured filesystem roots",
}

// routeBudgetProblems returns one message per violation; empty means the table is complete.
func routeBudgetProblems(routes []registeredRoute, budgets []routeBudget, exempt map[string]string) []string {
	const fix = "add a row to routeBudgets or an entry with a reason to routeBudgetExemptions in route_budget_test.go (#430)"
	registered := map[string]bool{}
	for _, rt := range routes {
		if rt.method == http.MethodGet {
			registered["GET "+rt.pattern] = true
		}
	}
	budgeted := map[string]bool{}
	var problems []string
	for _, b := range budgets {
		key := "GET " + b.pattern
		budgeted[key] = true
		if !registered[key] {
			problems = append(problems, "budget row "+b.name+" names "+key+", which is not a registered route")
		}
		if _, ok := exempt[key]; ok {
			problems = append(problems, key+" is both budgeted and exempt")
		}
	}
	for key, reason := range exempt {
		if !registered[key] {
			problems = append(problems, "exemption "+key+" is not a registered route")
		}
		if reason == "" {
			problems = append(problems, "exemption "+key+" has no reason")
		}
	}
	for _, rt := range routes {
		key := rt.method + " " + rt.pattern
		if rt.method != http.MethodGet || budgeted[key] {
			continue
		}
		if _, ok := exempt[key]; !ok {
			problems = append(problems, key+" has no round-trip budget: "+fix)
		}
	}
	return problems
}

// getThroughMux serves target to fn through a one-pattern mux (so path values are set exactly
// as buildRouter sets them), with admin and a fresh sqlStats on the request context.
func getThroughMux(pattern string, fn http.HandlerFunc, target string, admin *User) (*httptest.ResponseRecorder, *sqlStats) {
	st := &sqlStats{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+pattern, fn)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := context.WithValue(req.Context(), ctxUserKey, admin)
	ctx = context.WithValue(ctx, ctxSQLStatsKey, st)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec, st
}

func TestRouteBudgetsCoverAllGETRoutes(t *testing.T) {
	h := testHandlerWithDB()
	_, routes := buildRouter(h)
	for _, p := range routeBudgetProblems(routes, routeBudgets(h), routeBudgetExemptions) {
		t.Error(p)
	}
}

// TestRouteBudgetTargetsMatchPatterns guards data entry: each row's target must dispatch to
// the row's own pattern, so a typo cannot make a row measure the wrong route.
func TestRouteBudgetTargetsMatchPatterns(t *testing.T) {
	for _, b := range routeBudgets(testHandlerWithDB()) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET "+b.pattern, b.fn)
		_, got := mux.Handler(httptest.NewRequest(http.MethodGet, b.target, nil))
		if got != "GET "+b.pattern {
			t.Errorf("%s: target %q dispatches to %q, want %q", b.name, b.target, got, "GET "+b.pattern)
		}
	}
}

func TestRouteBudgetProblems_ReportsUnbudgetedRoute(t *testing.T) {
	routes := []registeredRoute{{http.MethodGet, "/a"}, {http.MethodGet, "/b"}, {http.MethodPost, "/c"}}
	budgets := []routeBudget{{name: "a", pattern: "/a"}}
	got := routeBudgetProblems(routes, budgets, nil)
	if len(got) != 1 || !strings.Contains(got[0], "GET /b") {
		t.Errorf("problems = %q, want exactly one naming GET /b", got)
	}
}

func TestRouteBudgetProblems_ExemptionRules(t *testing.T) {
	routes := []registeredRoute{{http.MethodGet, "/a"}, {http.MethodGet, "/b"}, {http.MethodPost, "/c"}}
	budgets := []routeBudget{{name: "a", pattern: "/a"}}
	for _, c := range []struct {
		name    string
		budgets []routeBudget
		exempt  map[string]string
		want    int
	}{
		{"exemption with reason covers the route", budgets, map[string]string{"GET /b": "no db"}, 0},
		{"empty reason", budgets, map[string]string{"GET /b": ""}, 1},
		{"row for an unregistered pattern", append([]routeBudget{{name: "gone", pattern: "/gone"}}, budgets...), map[string]string{"GET /b": "no db"}, 1},
		{"exemption for an unregistered pattern", budgets, map[string]string{"GET /b": "no db", "GET /gone": "stale"}, 1},
		{"both budgeted and exempt", budgets, map[string]string{"GET /a": "dup", "GET /b": "no db"}, 1},
		{"POST routes are ignored", budgets, map[string]string{"GET /b": "no db"}, 0},
	} {
		if got := routeBudgetProblems(routes, c.budgets, c.exempt); len(got) != c.want {
			t.Errorf("%s: problems = %q, want %d", c.name, got, c.want)
		}
	}
}
