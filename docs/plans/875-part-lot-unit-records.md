# #875 — Records lookup by Part Number, Lot, and Unit

## Goal

Add filterable/sortable, linkable tables of test records:
1. A new **Records** subtab on the Part detail page — every active `form_record`
   across every test form where `part_id` = this part.
2. A records table on the existing per-lot genealogy page
   (`part_lot_trace.html`) — every active `form_record` where `lot_id` = this lot.
3. A records table on the existing per-unit genealogy page
   (`part_unit_trace.html`) — every active `form_record` where `unit_id` = this unit.

All three reuse the existing shared client-side table engine
(`arx_go/static/shared/app.js`, `data-rows-url` + `ROW_BUILDER_PATTERNS`) — the
same mechanism `records_index.html` uses for the per-form Records table. No new
JS library, no schema change (`form_record.part_id/lot_id/unit_id` already exist
with FKs — see `SQL/azure/form_record.sql`).

## Resolved decisions (from user)

- Lot/Unit tables live on the existing trace pages, not new pages.
- All three tables are full sortable/filterable client-side tables (not static
  lists), same engine as the per-form Records table.
- Columns match the existing per-form Records table (Serial Number, Part
  Number, Description, Date, Type, Status, Form Rev), **plus a Form/Template
  column** on all three (not just Part-level) — a lot/unit can accumulate
  records from more than one test form too (e.g. Incoming Inspection + Final
  Test), so the Form column carries the same value there.
- No bulk-select/lock column — these are read-only cross-form views (bulk lock
  is a per-form operation, doesn't apply when rows span forms).

## 1. Backend — one shared query helper, three thin handlers

### `arx_go/records.go` — add a shared row struct + query function

Add near `RecordsRows` (after line 355):

```go
// scopedRecordRow is one row for the Part/Lot/Unit-scoped records tables
// (records.go PartRecordsRows/LotRecordsRows/UnitRecordsRows) — like RecordsRows'
// row but spans multiple forms, so it carries the Form (test template) too.
type scopedRecordRow struct {
	ID           int    `json:"id"`
	PartNumberID int    `json:"pnId"`
	SN           string `json:"sn"`
	SNPN         string `json:"snPN"`
	SNDesc       string `json:"snDesc"`
	Date         string `json:"date"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	FormRev      string `json:"formRev"`
	FormID       int    `json:"formId"`
	FormLabel    string `json:"formLabel"` // "<form part number> — <form title>"
}

// scopedRecordsRows returns every active form_record matching whereCol = id,
// across all forms, for the Part/Lot/Unit records tables (#875). whereCol must
// be "part_id", "lot_id", or "unit_id" — always a caller-supplied constant,
// never request input.
func (h *Handler) scopedRecordsRows(ctx context.Context, whereCol string, id int) ([]scopedRecordRow, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT r.id, COALESCE(r.part_id,0), r.serial_number, r.subject_part_number, r.subject_pn_description,
		       r.record_date, r.comments, r.is_locked, r.is_approved, r.form_revision,
		       r.form_id, fp.part_number, fp.title
		FROM %s r
		JOIN %s f ON f.id = r.form_id
		JOIN %s fp ON fp.id = f.part_number_id
		WHERE r.%s = @p1 AND r.is_active = %s
		ORDER BY r.record_date DESC`,
		h.cfg.RecordsTable(), h.cfg.FormsTable(), h.cfg.PartsTable(),
		whereCol, h.dia().BoolLiteral(true)), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]scopedRecordRow, 0)
	for rows.Next() {
		var rec scopedRecordRow
		var recordDate *time.Time
		var formRev *int
		var locked, approved bool
		var formPN, formTitle string
		if err := rows.Scan(&rec.ID, &rec.PartNumberID, &rec.SN, &rec.SNPN, &rec.SNDesc,
			&recordDate, &rec.Type, &locked, &approved, &formRev,
			&rec.FormID, &formPN, &formTitle); err != nil {
			return nil, err
		}
		if recordDate != nil {
			rec.Date = recordDate.Format("2006-01-02 15:04")
		}
		switch {
		case approved:
			rec.Status = "approved"
		case locked:
			rec.Status = "complete"
		default:
			rec.Status = "wip"
		}
		rec.FormRev = models.TestRecord{FormRevision: formRev}.FormRevLabel()
		rec.FormLabel = formPN
		if formTitle != "" {
			rec.FormLabel += " — " + formTitle
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// scopedRecordTypeOptions returns distinct non-empty comments (Type) values for
// the filter-row datalist, scoped the same way as scopedRecordsRows.
func (h *Handler) scopedRecordTypeOptions(ctx context.Context, whereCol string, id int) ([]string, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT DISTINCT comments FROM %s
		WHERE %s = @p1 AND is_active = %s AND comments <> ''
		ORDER BY comments`, h.cfg.RecordsTable(), whereCol, h.dia().BoolLiteral(true)), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
```

Notes:
- `whereCol` is always one of three hardcoded string literals passed by the
  three call sites below — never derived from request input — so the
  `fmt.Sprintf` into the column position is safe (same pattern already used
  elsewhere in this file for table names).
- Reuses `h.cfg.RecordsTable()`, `FormsTable()`, `PartsTable()` — never
  hardcode table names (per CLAUDE.md).

### `arx_go/parts.go` — Part-level Records subtab

Add after `PartOrders` (around line 1779, following the existing subtab-handler
pattern — see `PartLots`/`PartUnits` for the shape):

```go
// PartRecords — GET /part/{id}/records. Lists every active test record across
// every form where subject part_id = this part (#875).
func (h *Handler) PartRecords(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "records")
	if !ok {
		return
	}
	typeOptions, err := h.scopedRecordTypeOptions(r.Context(), "part_id", p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving record types: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_records.html", map[string]any{
		"Part": p, "TypeOptions": typeOptions,
		"ActiveTab": "parts", "ActiveSubTab": "records",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

// PartRecordsRows — GET /api/part/{id}/records/rows. JSON rows for PartRecords'
// table (#875).
func (h *Handler) PartRecordsRows(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out, err := h.scopedRecordsRows(r.Context(), "part_id", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}
```

`tabVisible` in `parts.go` (~line 71) needs no change — "records" isn't listed,
so it falls to the `default: return true` case, same as attachments/where-used
(applies to every part category).

### `arx_go/lot.go` — Lot records table (on the existing trace page)

Add near `PartLotTrace` (after line 410):

```go
// LotRecordsRows — GET /api/part/{id}/lots/{lotID}/records/rows. JSON rows for
// the records table on the lot trace page (#875).
func (h *Handler) LotRecordsRows(w http.ResponseWriter, r *http.Request) {
	lotID, err := strconv.Atoi(chi.URLParam(r, "lotID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out, err := h.scopedRecordsRows(r.Context(), "lot_id", lotID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}
```

`PartLotTrace` itself (line 375) needs one addition: fetch `TypeOptions` for the
lot and pass it into the existing render call, e.g.:

```go
typeOptions, err := h.scopedRecordTypeOptions(r.Context(), "lot_id", lotID)
if err != nil {
	h.renderError(w, r, "Error retrieving record types: "+err.Error())
	return
}
```

...and add `"TypeOptions": typeOptions,` to the `h.render(w, r, "parts/part_lot_trace.html", map[string]any{...})` call (line 405-409).

### `arx_go/unit.go` — Unit records table (on the existing trace page)

Same shape, mirroring `PartUnitTrace` (line 181-232):

```go
// UnitRecordsRows — GET /api/part/{id}/units/{unitID}/records/rows. JSON rows
// for the records table on the unit trace page (#875).
func (h *Handler) UnitRecordsRows(w http.ResponseWriter, r *http.Request) {
	unitID, err := strconv.Atoi(chi.URLParam(r, "unitID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out, err := h.scopedRecordsRows(r.Context(), "unit_id", unitID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}
```

`PartUnitTrace` gets the same `TypeOptions` addition (`"unit_id"`, `unitID`) as
`PartLotTrace` above, added to its existing render map.

## 2. Routes — `arx_go/main.go`

Add after line 244 (`r.Post("/part/{id}/lots/{lotID}", h.LotUpdate)`):
```go
r.Get("/api/part/{id}/lots/{lotID}/records/rows", h.LotRecordsRows)
```

Add after line 250 (`r.Post("/part/{id}/units/{unitID}", h.UnitUpdate)`):
```go
r.Get("/api/part/{id}/units/{unitID}/records/rows", h.UnitRecordsRows)
```

Add a new Part-level route near the other `/part/{id}/...` subtab routes (next
to `/part/{id}/attachments`, ~line 225):
```go
r.Get("/part/{id}/records", h.PartRecords)
r.Get("/api/part/{id}/records/rows", h.PartRecordsRows)
```

## 3. Templates

### `arx_go/templates/shared/partials.html` — new subtab link

In `part_tabs` (line 162-...), add after the Attachments tab (line 204), before
Pricing:
```html
<a href="/part/{{$id}}/records" class="sub-tab{{if eq $sub "records"}} active{{end}}">Records</a>
```
Always visible (no `{{if}}` gate) — same treatment as Attachments/Where Used.

### `arx_go/templates/parts/part_records.html` — new file

Model directly on `records_index.html` (`arx_go/templates/records/records_index.html`)
but: no breadcrumb-to-form/New-Record/View-Definition/Yield/Failure-Modes
buttons (those are per-form actions), no select column, no bulk-lock script —
just `{{template "part_tabs" .}}`, a heading, and the table:

```html
{{define "content"}}
<div class="content">
    {{template "part_tabs" .}}

    <h4 class="mb-3">{{.Part.PartNumber}} &mdash; Records</h4>

    <div class="table-wrapper">
        <table class="table table-bordered table-hover table-sm" id="part-records-table" data-rows-url="/api/part/{{.Part.ID}}/records/rows">
            <thead>
                <tr class="table-dark">
                    <th data-col="col-sn">Serial Number</th>
                    <th data-col="col-pn">Part Number</th>
                    <th data-col="col-desc">Description</th>
                    <th data-col="col-date">Date</th>
                    <th data-col="col-type">Type</th>
                    <th data-col="col-status" class="text-center">Status</th>
                    <th data-col="col-form-rev" class="text-center">Form Rev</th>
                    <th data-col="col-form">Form</th>
                </tr>
                <tr class="filter-row">
                    <th data-col="col-sn"><input type="text" class="form-control form-control-sm" placeholder="Filter" autocomplete="off" aria-label="Filter serial number"></th>
                    <th data-col="col-pn"><input type="text" class="form-control form-control-sm" placeholder="Filter" autocomplete="off" aria-label="Filter part number"></th>
                    <th data-col="col-desc"><input type="text" class="form-control form-control-sm" placeholder="Filter" autocomplete="off" aria-label="Filter description"></th>
                    <th data-col="col-date" data-filter-type="date"></th>
                    <th data-col="col-type">
                        <input type="text" list="type-options" class="form-control form-control-sm" placeholder="All" autocomplete="off" aria-label="Type filter">
                        <datalist id="type-options">{{range .TypeOptions}}<option value="{{.}}"></option>{{end}}</datalist>
                    </th>
                    <th data-col="col-status">
                        <select id="status-filter" class="form-select form-select-sm" aria-label="Status filter">
                            <option value="">All</option>
                        </select>
                    </th>
                    <th data-col="col-form-rev"></th>
                    <th data-col="col-form"><input type="text" class="form-control form-control-sm" placeholder="Filter" autocomplete="off" aria-label="Filter form"></th>
                </tr>
            </thead>
            <tbody>
                <tr><td colspan="8" class="no-results">Loading&hellip;</td></tr>
            </tbody>
        </table>
    </div>
    <div class="pagination-controls">
        <span class="pagination-info"></span>
        <div class="pagination-buttons">
            <button type="button" class="page-nav prev" onclick="prevPage()">&#8592; Previous</button>
            <button type="button" class="page-nav next" onclick="nextPage()">Next &#8594;</button>
        </div>
    </div>
    <p style="margin-top:15px; color:#666; text-align:right;">
        Total matching: <strong><span class="record-count">0</span></strong>
    </p>
</div>
{{end}}
```

Note: unlike `records_index.html`, the status filter here defaults to `""`
(All) rather than `"wip"` — this is a cross-form lookup view, not a WIP work
queue, so showing everything by default reads correctly. No JS block needed —
sorting/filtering/pagination is entirely handled by the shared engine once the
`ROW_BUILDER_PATTERNS` entry below exists.

### Records table partial on the trace pages

Add the same 8-column table (different `id`/`data-rows-url`, same column
layout) into `part_lot_trace.html` and `part_unit_trace.html`, appended after
the "Downstream lots" section (after line 112 in `part_lot_trace.html`, after
line 105 in `part_unit_trace.html`):

```html
{{/* ── Test records ────────────────────────────────────────── */}}
<h6 class="mt-4">Test records</h6>
<div class="table-wrapper">
    <table class="table table-bordered table-hover table-sm" id="lot-records-table" data-rows-url="/api/part/{{.Part.ID}}/lots/{{.Lot.ID}}/records/rows">
        <!-- same thead as part_records.html above -->
    </table>
</div>
<div class="pagination-controls">
    <span class="pagination-info"></span>
    <div class="pagination-buttons">
        <button type="button" class="page-nav prev" onclick="prevPage()">&#8592; Previous</button>
        <button type="button" class="page-nav next" onclick="nextPage()">Next &#8594;</button>
    </div>
</div>
<p style="margin-top:15px; color:#666; text-align:right;">
    Total matching: <strong><span class="record-count">0</span></strong>
</p>
```

For `part_unit_trace.html`, same block with `id="unit-records-table"` and
`data-rows-url="/api/part/{{.Part.ID}}/units/{{.Unit.ID}}/records/rows"`.

**Caveat the implementer must handle**: `loadListRows()` in `app.js` (line
451-452) does `document.querySelector('table[data-rows-url]')` — singular,
first match only. The trace pages only have one such table each (the
Ancestors/Descendants tables are plain, no `data-rows-url`), so this is safe
as-is. Do not add a second `data-rows-url` table to the same page.

Also pass `TypeOptions` into both trace page render calls (see backend section
above) for the `datalist` to populate.

## 4. `arx_go/static/shared/app.js` — one new row-builder pattern

Add to `ROW_BUILDER_PATTERNS` (after the existing `/api/forms/.../records/rows`
entry, ~line 267):

```js
{
    // /api/part/{id}/records/rows, /api/part/{id}/lots/{lotID}/records/rows,
    // /api/part/{id}/units/{unitID}/records/rows — #875. Same column shape as
    // the per-form records table, minus the select checkbox (read-only,
    // cross-form view), plus a trailing Form column (records span multiple
    // forms in these scoped views).
    test: /\/api\/part\/\d+\/(?:lots\/\d+\/|units\/\d+\/)?records\/rows$/,
    build: r => {
        const pn = r.pnId
            ? `<a href="/part/${r.pnId}">${escHtml(r.snPN)}</a>`
            : escHtml(r.snPN);
        return `<tr>
            <td data-col="col-sn"><a href="/records/${r.id}" class="fw-semibold">${escHtml(r.sn)}</a></td>
            <td data-col="col-pn">${pn}</td>
            <td data-col="col-desc">${escHtml(r.snDesc)}</td>
            <td data-col="col-date" class="text-nowrap">${r.date || ''}</td>
            <td data-col="col-type">${escHtml(r.type)}</td>
            <td data-col="col-status" class="text-center">${TR_STATUS_BADGE[r.status] || ''}</td>
            <td data-col="col-form-rev" class="text-center">${escHtml(r.formRev)}</td>
            <td data-col="col-form"><a href="/forms/${r.formId}/records">${escHtml(r.formLabel)}</a></td>
        </tr>`;
    },
    cellText: r => [r.sn, r.snPN, r.snDesc, r.date, r.type, r.status, r.formRev, r.formLabel],
},
```

## Verification

- `go build ./...` / `go vet ./...` from `arx_go/` (per `build.bat`).
- `go test ./...` (existing suite; no new Go tests required — this is
  read-only UI/query plumbing mirroring an already-tested pattern).
- Manual: open a lot-tracked or serial-tracked part with existing test
  records (seeded ArxDev data), check the new Records subtab; open a lot's
  trace page and a unit's trace page, confirm each records table loads,
  sorts, and filters, and that Serial Number/Part/Form links navigate
  correctly.
- No SQL/schema changes — no migration needed.
