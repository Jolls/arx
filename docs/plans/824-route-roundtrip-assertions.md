# Plan: TestIntegration_RouteRoundTrips correctness assertions (#824)

File: `arx_go/integration_test.go`, function `TestIntegration_RouteRoundTrips` (currently lines 1321-1373).

## 1. Extend the `cases` struct and table (lines 1334-1358)

Add a `wantBody string` field (a substring that must appear in `rec.Body.String()` for that route to be considered a correct render). Populate per case using values confirmed from `SQL/seed_test_data.sql`:

- Part 3005: `part_number` = `ASM-1001`, `title` = `Widget Assembly`.
- Part 3002: `part_number` = `BUY-1001`.
- Company 1001: `name` = `Acme Fasteners`.
- PO 5002: `po_number` = `5002` (rendered in `po_detail.html` as `PO #{{.PO.Number}}` → `PO #5002`).
- Form 6001: `part_number_id` = 3010 → `part_number` = `FORM-1001`, `title` = `Widget Housing Test Form`.

Per-case values (all confirmed by reading the actual templates, not guessed):

| case | wantBody | basis |
|---|---|---|
| parts list | `id="parts-table"` | `templates/parts/index.html` — table is populated client-side via `data-rows-url="/api/parts/rows"`; seeded row is not in the SSR body, so only the table shell is a safe assertion. |
| part detail | `ASM-1001` | `templates/parts/part_detail.html` renders `{{.Part.PartNumber}}` in breadcrumb and detail row. |
| part BOM | `ASM-1001` (and keep existing structural marker `id="bom-table"` if desired) | `templates/parts/part_bom.html` breadcrumb + `<table ... id="bom-table">`. |
| part build-cost | `ASM-1001` | `templates/parts/part_build_cost.html`: `Cost to Build ... {{.Part.PartNumber}}`. Target string needs `?qty=1` appended (`PartBuildCost` requires a valid `qty` query param or it renders an error page instead) — verified against a live run. |
| part price-history | `BUY-1001` | Uses `seedPriceHistoryPartID` (3002), same breadcrumb pattern as other part pages. |
| part orders | `ASM-1001` | Same breadcrumb pattern; page also has `<table class="table table-bordered table-hover table-sm">`. |
| suppliers list | `data-rows-url="/api/suppliers/rows"` | `templates/suppliers/suppliers.html` — client-fetched table, same as parts list. |
| supplier detail | `Acme Fasteners` | `templates/suppliers/supplier_detail.html`: `{{.Supplier.Name}}` in breadcrumb + detail row. |
| PO list | `data-rows-url="/api/pos/rows"` | `templates/pos/pos.html` — client-fetched table. |
| PO detail | `PO #5002` | `templates/pos/po_detail.html` breadcrumb: `PO #{{.PO.Number}}`. |
| contacts list | `data-rows-url="/api/contacts/rows"` | `templates/contacts/contacts.html` — client-fetched table. |
| records/forms list | `FORM-1001` | `templates/records/index.html` server-renders `{{range .Forms}}` with `pn.part_number`/`pn.title`; unlike the list pages above, `FormsList` (`records.go`) queries and renders forms server-side, so seeded data is actually in the body. |
| records yield summary | `FORM-1001` | `templates/records/yield.html`: `{{.Form.PartNumber}} — {{.Form.Title}}`. |
| reports yield picker | `FORM-1001` | `templates/reports/yield_picker.html` `{{range .Forms}}` renders `{{.PartNumber}}`/`{{.Title}}` from `h.loadActiveFormOptions` (`reports.go` `ReportsYieldPicker`), same server-rendered pattern as forms list. |

## 2. Extend the loop body (lines 1360-1372)

After the existing `c.fn(rec, req)` call and the existing `t.Logf(...)` profiling line (keep it unchanged, do not remove/reorder), add:

```go
if rec.Code != http.StatusOK {
    t.Errorf("%s: got status %d, want 200. body: %s", c.name, rec.Code, rec.Body.String())
}
if !strings.Contains(rec.Body.String(), c.wantBody) {
    t.Errorf("%s: body missing expected marker %q", c.name, c.wantBody)
}
```

Use `t.Errorf` (not `t.Fatalf`) so one broken route doesn't abort profiling/assertion of the remaining cases in the same run — matches the existing loop's "run every case" structure. `strings` and `http` are already imported (used elsewhere in the file).

## 3. No changes needed to
- Build tag (`//go:build integration`) — untouched.
- `liveHandler`, `withID`, `sqlStats`/`ctxSQLStatsKey` plumbing — untouched.
- The existing seed-ID constants block (lines 1329-1332) — reused as-is, no new IDs needed.

## Open questions
None — all substring values were confirmed against `SQL/seed_test_data.sql` and the actual `.html` templates, including the important nuance that `parts list` / `suppliers list` / `PO list` / `contacts list` are populated client-side via `data-rows-url` fetches and therefore cannot assert seeded row content in the server-rendered body — only `FormsList` and `ReportsYieldPicker` render their list data server-side.

### Critical files for implementation
- arx_go/integration_test.go
- SQL/seed_test_data.sql
- arx_go/templates/parts/part_detail.html
- arx_go/templates/pos/po_detail.html
- arx_go/templates/suppliers/supplier_detail.html
- arx_go/templates/records/yield.html
- arx_go/templates/reports/yield_picker.html
