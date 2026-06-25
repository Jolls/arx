# Plan: Issues #242 · #258 · #259 · #286 — Edit UX + Exports

> Created: 2026-06-24

## Overview

Four issues, three PRs. None block each other; all can branch off main in parallel.

| PR | Issues | Files touched | DB? |
|---|---|---|---|
| `feature/pdf-export-242` | #242 | `records_show.html` | No |
| `feature/edit-ux-258-259` | #258, #259 | `record_edit.html` | No |
| `feature/csv-export-286` | #286 | `parts.go`, `pos.go`, `main.go`, 3 templates | No |

---

## PR 1 — #242: PDF export button

**Finding:** `/records/{id}/print` already exists and satisfies every acceptance criterion
(record header, all result rows, section headings, pass/fail badges, printable).
The button is already on `records_show.html:58` — it just says "Print" instead of "PDF".

**Change:** `arx_go/templates/tr/records_show.html` line ~58:

```html
<!-- before -->
<a href="/records/{{.Record.ID}}/print" target="_blank" class="btn btn-sm btn-outline-secondary ms-2">
  <i class="bi bi-printer"></i> Print
</a>

<!-- after -->
<a href="/records/{{.Record.ID}}/print" target="_blank" class="btn btn-sm btn-outline-secondary ms-2">
  <i class="bi bi-file-pdf"></i> Export PDF
</a>
```

No route change, no backend change.

---

## PR 2 — #258 + #259: Keyboard nav + Auto-save

Both are pure JS added to `arx_go/templates/tr/record_edit.html`.

### #258 — Keyboard navigation

Tab already works correctly (DOM order, heading rows have no inputs so they're skipped
naturally). The only needed behavior is Enter → advance to next input (not submit form).

Add at end of `record_edit.html`:

```javascript
// Collect all focusable result/comment inputs in DOM order
function editInputs() {
  return [...document.querySelectorAll(
    '#edit-form input.form-control:not([type=hidden]), #edit-form select.form-select'
  )];
}

document.addEventListener('keydown', function(e) {
  if (e.key !== 'Enter') return;
  if (!e.target.matches('#edit-form input.form-control, #edit-form select.form-select')) return;
  e.preventDefault();
  const inputs = editInputs();
  const idx = inputs.indexOf(e.target);
  if (idx >= 0 && idx < inputs.length - 1) {
    inputs[idx + 1].focus();
  }
});
```

### #259 — Auto-save / draft recovery

LocalStorage only. Draft key includes record ID so drafts don't bleed across records.

```javascript
const DRAFT_KEY = 'arx-record-draft-{{.Record.ID}}';

function draftData() {
  const data = { _saved: new Date().toISOString() };
  document.querySelectorAll(
    '#edit-form input[name^="result_"], #edit-form select[name^="result_"], ' +
    '#edit-form input[name^="comment_"], #edit-form textarea[name^="comment_"]'
  ).forEach(el => { data[el.name] = el.value; });
  // Also capture header fields
  ['record_date', 'comments', 'instrument_type'].forEach(n => {
    const el = document.querySelector(`#edit-form [name="${n}"]`);
    if (el) data[n] = el.value;
  });
  return data;
}

function saveDraft() {
  localStorage.setItem(DRAFT_KEY, JSON.stringify(draftData()));
  const ts = new Date().toLocaleTimeString();
  const ind = document.getElementById('draft-indicator');
  if (ind) ind.textContent = 'Draft saved ' + ts;
}

function restoreDraft(draft) {
  Object.entries(draft).forEach(([name, val]) => {
    if (name.startsWith('_')) return;
    const el = document.querySelector(`#edit-form [name="${name}"]`);
    if (el) el.value = val;
  });
  // Trigger change events so P/F re-evaluates
  document.querySelectorAll('#edit-form input[data-pf-target]').forEach(el => {
    el.dispatchEvent(new Event('input'));
  });
}

// Restore banner
const _saved = localStorage.getItem(DRAFT_KEY);
if (_saved) {
  const draft = JSON.parse(_saved);
  const ts = new Date(draft._saved).toLocaleTimeString();
  const banner = document.createElement('div');
  banner.className = 'alert alert-warning d-flex align-items-center gap-3 mb-3';
  banner.id = 'draft-banner';
  banner.innerHTML =
    `<span>Unsaved draft from ${ts}</span>` +
    `<button type="button" class="btn btn-sm btn-warning" id="draft-restore">Restore</button>` +
    `<button type="button" class="btn btn-sm btn-outline-secondary" id="draft-discard">Discard</button>`;
  document.getElementById('edit-form').insertAdjacentElement('beforebegin', banner);

  document.getElementById('draft-restore').addEventListener('click', () => {
    restoreDraft(draft);
    banner.remove();
  });
  document.getElementById('draft-discard').addEventListener('click', () => {
    localStorage.removeItem(DRAFT_KEY);
    banner.remove();
  });
}

// Auto-save every 30s
setInterval(saveDraft, 30000);

// Clear on successful submit
document.getElementById('edit-form').addEventListener('submit', () => {
  localStorage.removeItem(DRAFT_KEY);
});
```

Add a draft indicator span near the Save button in the button row:

```html
<span id="draft-indicator" class="small text-muted"></span>
```

---

## PR 3 — #286: CSV export (Parts · POs · BOM)

No DB migration. Three new handler functions, three new routes, three template buttons.

### Routes (add to `arx_go/main.go`)

```go
r.Get("/parts/export.csv",        h.PartsExportCSV)
r.Get("/pos/export.csv",          h.POsExportCSV)
r.Get("/part/{id}/bom/export.csv", h.BOMExportCSV)
```

### `PartsExportCSV` — add to `arx_go/parts.go`

Same query as `PartsRows`. Columns: Part Number, Revision, Title, Detail, Requested By,
Created Date, Category, Modified Date, Active.

```go
func (h *Handler) PartsExportCSV(w http.ResponseWriter, r *http.Request) {
    rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
        SELECT part_number, revision, title, detail,
               requested_by, created_date, category, modified_date, is_active
        FROM %s ORDER BY part_number
    `, h.cfg.PartsTable()))
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    defer rows.Close()
    w.Header().Set("Content-Type", "text/csv")
    w.Header().Set("Content-Disposition", `attachment; filename="parts.csv"`)
    cw := csv.NewWriter(w)
    _ = cw.Write([]string{"Part Number","Revision","Title","Detail","Requested By","Created Date","Category","Modified Date","Active"})
    for rows.Next() {
        var pn, rev, title, detail, reqBy, cat sql.NullString
        var created, modified sql.NullTime
        var active sql.NullBool
        if err := rows.Scan(&pn, &rev, &title, &detail, &reqBy, &created, &cat, &modified, &active); err != nil {
            return
        }
        activeStr := "true"
        if active.Valid && !active.Bool { activeStr = "false" }
        createdStr := ""
        if created.Valid { createdStr = created.Time.Format("2006-01-02") }
        modifiedStr := ""
        if modified.Valid { modifiedStr = modified.Time.Format("2006-01-02") }
        _ = cw.Write([]string{pn.String, rev.String, title.String, detail.String, reqBy.String, createdStr, cat.String, modifiedStr, activeStr})
    }
    cw.Flush()
}
```

### `POsExportCSV` — add to `arx_go/pos.go`

One row per PO line item. JOIN `po_line` so each line is a separate row.
Columns: PO Number, Status, Supplier, Date Ordered, Date Closed, Orderer, Total Cost,
Line #, Part Number, Description, Qty, Unit Cost, Vendor PN.

```go
func (h *Handler) POsExportCSV(w http.ResponseWriter, r *http.Request) {
    rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
        SELECT p.number, p.status, p.supplier_name, p.date_ordered, p.date_closed,
               p.orderer, p.total_cost,
               l.line_number, l.part_number, l.description, l.qty, l.unit_cost, l.vendor_pn
        FROM %s p
        LEFT JOIN %s l ON l.po_number = p.number
        ORDER BY p.number DESC, l.line_number
    `, h.cfg.POTable(), h.cfg.POLineTable()))
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    defer rows.Close()
    w.Header().Set("Content-Type", "text/csv")
    w.Header().Set("Content-Disposition", `attachment; filename="purchase-orders.csv"`)
    cw := csv.NewWriter(w)
    _ = cw.Write([]string{"PO Number","Status","Supplier","Date Ordered","Date Closed","Orderer","PO Total","Line #","Part Number","Description","Qty","Unit Cost","Vendor PN"})
    for rows.Next() {
        var num, status, supplier, orderer sql.NullString
        var dateOrdered, dateClosed sql.NullTime
        var totalCost sql.NullFloat64
        var lineNum sql.NullInt64
        var linePN, lineDesc, lineVendorPN sql.NullString
        var lineQty, lineUnitCost sql.NullFloat64
        if err := rows.Scan(&num, &status, &supplier, &dateOrdered, &dateClosed,
            &orderer, &totalCost, &lineNum, &linePN, &lineDesc, &lineQty, &lineUnitCost, &lineVendorPN); err != nil {
            return
        }
        orderedStr := ""
        if dateOrdered.Valid { orderedStr = dateOrdered.Time.Format("2006-01-02") }
        closedStr := ""
        if dateClosed.Valid { closedStr = dateClosed.Time.Format("2006-01-02") }
        lineNumStr := ""
        if lineNum.Valid { lineNumStr = fmt.Sprintf("%d", lineNum.Int64) }
        _ = cw.Write([]string{
            num.String, status.String, supplier.String, orderedStr, closedStr,
            orderer.String, fmt.Sprintf("%.2f", totalCost.Float64),
            lineNumStr, linePN.String, lineDesc.String,
            fmt.Sprintf("%.4g", lineQty.Float64), fmt.Sprintf("%.2f", lineUnitCost.Float64),
            lineVendorPN.String,
        })
    }
    cw.Flush()
}
```

**Note:** `h.cfg.POLineTable()` — verify the correct config helper name before using.

### `BOMExportCSV` — add to `arx_go/parts.go`

Same query as `PartBOM`. Columns: Line #, Qty, Part Number, Title, Revision, Category,
Unit Cost, Ext Cost, Cost Source.

```go
func (h *Handler) BOMExportCSV(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    pl, pn := h.cfg.BOMTable(), h.cfg.PartsTable()
    rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
        SELECT pl.line_number, pl.qty, pn.part_number, pn.title, pn.revision, pn.category,
               pn.current_cost, pn.last_rollup_cost,
               CAST(CASE WHEN EXISTS(SELECT 1 FROM %s c WHERE c.parent_part_id = pn.id) THEN 1 ELSE 0 END AS BIT)
        FROM %s pl
        JOIN %s pn ON pl.component_part_id = pn.id
        WHERE pl.parent_part_id = @p1
        ORDER BY pl.line_number
    `, pl, pl, pn), id)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    // Fetch parent PN for filename
    var parentPN string
    _ = h.queryRowContext(r.Context(), fmt.Sprintf(
        `SELECT part_number FROM %s WHERE id = @p1`, h.cfg.PartsTable()), id).Scan(&parentPN)
    if parentPN == "" { parentPN = id }

    w.Header().Set("Content-Type", "text/csv")
    w.Header().Set("Content-Disposition", `attachment; filename="`+parentPN+`-bom.csv"`)
    cw := csv.NewWriter(w)
    _ = cw.Write([]string{"Line #","Qty","Part Number","Title","Revision","Category","Unit Cost","Ext Cost","Cost Source"})
    for rows.Next() {
        var lineNum sql.NullInt64
        var qty sql.NullFloat64
        var partNum, title, rev, cat sql.NullString
        var currentCost, rollupCost sql.NullFloat64
        var childHasBOM sql.NullBool
        if err := rows.Scan(&lineNum, &qty, &partNum, &title, &rev, &cat,
            &currentCost, &rollupCost, &childHasBOM); err != nil {
            return
        }
        var unitCost float64
        var costSrc string
        if childHasBOM.Bool {
            unitCost = rollupCost.Float64
            if rollupCost.Float64 > 0 { costSrc = "rollup" } else { costSrc = "missing" }
        } else {
            unitCost = currentCost.Float64
            if currentCost.Float64 > 0 { costSrc = "current_cost" } else { costSrc = "missing" }
        }
        extCost := unitCost * qty.Float64
        _ = cw.Write([]string{
            fmt.Sprintf("%d", lineNum.Int64),
            fmt.Sprintf("%.4g", qty.Float64),
            partNum.String, title.String, rev.String, cat.String,
            fmt.Sprintf("%.2f", unitCost),
            fmt.Sprintf("%.2f", extCost),
            costSrc,
        })
    }
    cw.Flush()
}
```

### Template buttons

- Parts list (`arx_go/templates/pm/index.html`): add export link in the toolbar near the top
- PO list (`arx_go/templates/pm/pos.html`): same
- BOM tab (`arx_go/templates/pm/part_bom.html` or inline in part detail): add link in BOM tab header

Find the correct template paths by checking which templates the `PartsList`, `POList`, and `PartBOM` handlers render.

---

## Verify before merging each PR

1. `cd arx_go && .\build.bat` — must pass (includes `go test ./...`)
2. Smoke-test: open the relevant page, confirm button present, download works
3. BOM export: check filename uses parent PN, not raw ID
