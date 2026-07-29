## Plan: docs/plans/825-template-render-assertions.md

**Target file:** `arx_go/templates_parse_test.go` (add new test functions to the existing file; no new file needed — same package `main`, same imports style).

### Representative templates chosen (one per family)

1. **Core tab page:** `templates/contacts/contact_detail.html`, rendered via `h.render()` pattern (layout+partials+page, execute `"layout"`). Handler: `ContactDetail` in `arx_go/contacts.go:104`, data `map[string]any{"Contact": c, ...}` where `c` is `models.Contact` (`arx_go/models/contact.go`).
2. **Records page:** `templates/records/yield.html`, rendered via `h.renderRecords()` pattern (layout+`templates/records/`+page, execute `"layout"`). Handler: `RecordsYieldSummary` in `arx_go/records_yield.go:147`, data uses `models.TestForm` (`arx_go/models/trmodels.go`) and the unexported `yieldBucket` type (`arx_go/records_yield.go:24`, same package so directly constructible in test).
3. **Core print page:** `templates/pos/po_print.html` (the only core-tab `*_print.html`), rendered via `h.renderPrint()` pattern (single-template ParseFS, execute `path.Base(page)`). Handler: `pos.go:996`, data uses `models.PurchaseOrder` (`arx_go/models/purchase_order.go`) + `[]models.PurchaseOrderLine` (`POItems`) + `LineTotal float64`.
4. **Shared standalone:** `templates/shared/login.html`, rendered via `h.renderLogin()`'s pattern: `template.New("").ParseFS(templatesFS, "templates/shared/login.html")`, execute `"login.html"` (no Funcs, no layout — matches existing `TestSharedStandaloneTemplatesParse` comment). Data is `map[string]any` per `auth.go:555` (`DefaultUsername`, `Bootstrap`, `Notice`, `Error`, `CSRFToken`, `CompanyLogo`, `SchemaMismatch`).

### New test functions to add in `templates_parse_test.go`

**`TestContactDetailTemplateRenders`**
- Build `models.Contact{ID: 1, DisplayName: "Acme Test Contact", Email: "acme@example.com", IsActive: true}` (zero-value `CompanyID`/`UpdatedAt` nil pointers so the `{{if .Contact.CompanyID}}`/`formatDate` branches take their nil-safe paths — do not set them, avoids needing a real `*time.Time`/company row).
- `template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS, "templates/shared/layout.html", "templates/shared/partials.html", "templates/contacts/contact_detail.html")`.
- `data := map[string]any{"Contact": contact, "ActiveTab": "contacts", "TestMode": false}` plus the layout-required zero-value keys (see Resolved decision below for the exact key set).
- Execute `"layout"` into a `strings.Builder`, assert output contains `"Acme Test Contact"`.

**`TestYieldTemplateRenders`**
- Build `models.TestForm{ID: 7, PartNumber: "PN-TEST-YIELD", Title: "Widget"}`.
- Build `total := yieldBucket{Label: "Total", Total: 10, Passed: 8, Failed: 2}` (same package, no export issue).
- `template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS, "templates/shared/layout.html", "templates/records/yield.html")`.
- `data := map[string]any{"Form": form, "Total": total, "Monthly": nil, "Grouped": false, "FromStr": "", "ToStr": "", "ActiveTab": "records", "TestMode": false}` plus the same layout-required zero-value keys.
- Execute `"layout"`, assert output contains `"PN-TEST-YIELD"` and the Passed value rendered in its specific card markup (pin to the actual markup found in yield.html, not a bare "8" substring match).

**`TestPOPrintTemplateRenders`**
- Build `po := models.PurchaseOrder{Number: "PO-TEST-100", Status: "open", SupplierName: "Acme Supply"}` (leave cost pointer fields nil — `deref` func handles nil safely).
- Build `items := []models.PurchaseOrderLine{{LineNumber: 1, PartNumberSnapshot: "PN-100", Qty: 2, UnitCost: 5.00}}`.
- `template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS, "templates/pos/po_print.html")`.
- `data := map[string]any{"PO": po, "POItems": items, "LineTotal": 10.0, "SupplierCode": "", "TestMode": false, "POFolderPath": "", "IsRFQ": false}`.
- Execute template name `"po_print.html"` (matches `path.Base(page)` in real `renderPrint`), assert output contains `"PO-TEST-100"` and `"PN-100"`.

**`TestLoginTemplateRenders`**
- `template.New("").ParseFS(templatesFS, "templates/shared/login.html")` (no Funcs).
- `data := map[string]any{"DefaultUsername": "test.user", "Bootstrap": false, "CSRFToken": "tok123"}`.
- Execute `"login.html"`, assert output contains `value="test.user"`.

### Shared helper
Add a small `renderToString(t *testing.T, tmpl *template.Template, name string, data any) string` helper at top of the file (execute into `strings.Builder`, `t.Fatalf` on error, return `.String()`) to avoid repeating buffer boilerplate across the four new tests.

### Imports to add
Prefer `strings.Builder` over a new `bytes` import (`strings` already imported); add `"arx/arx_go/models"` (confirmed import path, matches `contacts.go`'s import of the same package).

### Resolved decisions (investigated directly, not user judgment calls)
1. **Layout/partials required keys**: read `templates/shared/layout.html` and `templates/shared/partials.html` in full. `contact_detail.html` and `yield.html` don't invoke any of the `{{template "..."}}` partials that need extra context (`po_status_badge` etc. aren't used by either page). `layout.html` itself needs `AppVersion`, `SchemaMismatch`, `CurrentUser`, `CSRFToken`, `CompanyLogo`, `AccentThemeClass`, `Title`, `Favicon`, `FaviconType` supplied (matches exactly what `render()`/`renderRecords()` inject in production) — added as a shared `coreLayoutFakeData()` helper in the test file.
2. **`po_print.html` field usage**: read the full file — only `LineNumber`, `PartNumberSnapshot`, `RevisionSnapshot`, `Description`, `VendorPN`, `Qty`, `UnitCost` are used from `PurchaseOrderLine`; no other line fields are dereferenced. Confirmed via live test run.

### Critical Files for Implementation
- arx_go/templates_parse_test.go
- arx_go/templates/contacts/contact_detail.html
- arx_go/templates/records/yield.html
- arx_go/templates/pos/po_print.html
- arx_go/templates/shared/login.html
- arx_go/templates/shared/layout.html
- arx_go/templates/shared/partials.html
