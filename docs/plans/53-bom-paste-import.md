# Issue #53 — Paste-import BOM rows from Excel (TSV paste, with preview)

## Open questions

None. The issue body plus the prior `/update-issue` comment (exact qty match,
header-row sniffed off a non-numeric qty column) fully specify behavior. All
choices below that aren't spelled out in the issue (e.g. malformed-row
handling, no-op row styling) are ordinary parsing/rendering defaults, not
business-logic judgment calls.

## Summary

Add a "Paste BOM rows" affordance to the existing BOM edit page
(`/part/{id}/bom/edit`). User pastes tab-separated `part number<TAB>qty` rows
copied from Excel into a textarea, clicks **Preview**, which POSTs to a new
`/part/{id}/bom/preview` endpoint that parses/resolves/diffs the pasted rows
against the part's current BOM (read-only) and returns an HTML preview
fragment. Clicking **Confirm Import** then injects the validated rows into
the *existing* on-screen BOM table (as `new_pl[...]`/updated `pl[...]`
fields) and submits the same `<form>` that already posts to
`POST /part/{id}/bom` (`PartBOMSave`) — no new commit endpoint, no new
transaction code.

## Backend

### 1. New route — `arx_go/main.go`

Add immediately after the existing BOM routes (currently lines 233–236):

```go
r.Get("/part/{id}/bom", h.PartBOM)
r.Get("/part/{id}/bom/edit", h.PartBOMEdit)
r.Get("/part/{id}/bom/export.csv", h.BOMExportCSV)
r.Post("/part/{id}/bom", h.PartBOMSave)
r.Post("/part/{id}/bom/preview", h.PartBOMPastePreview)   // new
```

CSRF: `RequireCsrfOnPost` is mounted globally in `main.go` (line 128, before
`RequireAuth`), so this route is protected automatically as long as the POST
carries a `csrf_token` field readable by `r.FormValue` — i.e. the request
body must be `application/x-www-form-urlencoded` (or multipart), not raw
JSON. See JS section below.

### 2. New handler + struct — `arx_go/parts.go`

Add a new section right after `PartBOMSave` (after line 1144, before the
`// ── BOM cost rollup ──` comment):

```go
// ── BOM paste-import preview ────────────────────────────────────────────────

// bomPastePreviewRow is one parsed/resolved row of a pasted BOM paste,
// carrying everything the preview template and the Confirm-Import JS need.
type bomPastePreviewRow struct {
	PartNumber  string  // canonical part_number from the DB match, or the raw pasted text on error
	Description string
	Qty         float64
	Status      string // "new" | "update" | "noop" | "error"
	RowClass    string // Bootstrap row class for the status
	StatusLabel string
	PLID        int    // existing bom.id for "update"/"noop" rows, 0 otherwise
	PNID        int    // resolved component_part_id, 0 on error
	RawText     string // original pasted line, shown for error rows
}

// parseBOMPasteText splits pasted TSV text into (partNumber, qtyText, rawLine)
// triples, sniffing off row 1 as a header when its qty column doesn't parse
// as a number (issue #53). Blank lines are skipped. Pure/no I/O — kept
// separate from PartBOMPastePreview so the parsing rule is unit-testable
// without a DB.
type bomPasteLine struct {
	PartNumber string
	QtyText    string
	Qty        float64
	QtyOK      bool
	RawText    string
}

func parseBOMPasteText(text string) []bomPasteLine {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	rawLines := strings.Split(text, "\n")
	var out []bomPasteLine
	first := true
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		cols := strings.SplitN(trimmed, "\t", 2)
		partNumber := strings.TrimSpace(cols[0])
		qtyText := ""
		if len(cols) > 1 {
			qtyText = strings.TrimSpace(cols[1])
		}
		qty, err := strconv.ParseFloat(qtyText, 64)
		if first {
			first = false
			if err != nil {
				continue // header row: qty column isn't numeric — skip it
			}
		}
		out = append(out, bomPasteLine{
			PartNumber: partNumber, QtyText: qtyText, Qty: qty, QtyOK: err == nil, RawText: trimmed,
		})
	}
	return out
}

// PartBOMPastePreview — POST /part/{id}/bom/preview. Parses pasted TSV
// part-number+qty rows, resolves each part number against the parts table,
// and diffs against the part's current BOM by component_part_id. Writes
// nothing; returns an HTML preview fragment (parts/part_bom_paste_preview.html).
func (h *Handler) PartBOMPastePreview(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.requireTab(w, r, id, "bom"); !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}

	pl, pn := h.cfg.BOMTable(), h.cfg.PartsTable()

	type existingLine struct {
		ID  int
		Qty float64
	}
	existing := map[int]existingLine{}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(
		`SELECT id, component_part_id, qty FROM %s WHERE parent_part_id = @p1`, pl,
	), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving BOM: "+err.Error())
		return
	}
	for rows.Next() {
		var plid, cpid int
		var qty float64
		if err := rows.Scan(&plid, &cpid, &qty); err != nil {
			rows.Close()
			h.renderError(w, r, "Error reading BOM: "+err.Error())
			return
		}
		existing[cpid] = existingLine{ID: plid, Qty: qty}
	}
	rows.Close()

	var preview []bomPastePreviewRow
	hasError := false
	for _, line := range parseBOMPasteText(r.FormValue("paste_text")) {
		row := bomPastePreviewRow{PartNumber: line.PartNumber, RawText: line.RawText}

		var pnid int
		var partNumber, description sql.NullString
		if line.PartNumber != "" {
			h.queryRowContext(r.Context(), fmt.Sprintf(
				`SELECT id, part_number, description FROM %s WHERE part_number = @p1`, pn,
			), line.PartNumber).Scan(&pnid, &partNumber, &description)
		}

		switch {
		case pnid == 0:
			row.Status, row.RowClass, row.StatusLabel = "error", "table-danger", "Error"
			hasError = true
		case !line.QtyOK:
			row.Status, row.RowClass, row.StatusLabel = "error", "table-danger", "Error"
			hasError = true
		default:
			row.PartNumber = partNumber.String
			row.Description = description.String
			row.Qty = line.Qty
			row.PNID = pnid
			if ex, ok := existing[pnid]; ok {
				row.PLID = ex.ID
				if ex.Qty == line.Qty {
					row.Status, row.RowClass, row.StatusLabel = "noop", "table-secondary text-muted", "No change"
				} else {
					row.Status, row.RowClass, row.StatusLabel = "update", "table-warning", "Update"
				}
			} else {
				row.Status, row.RowClass, row.StatusLabel = "new", "table-success", "New"
			}
		}
		preview = append(preview, row)
	}

	h.renderPrint(w, "parts/part_bom_paste_preview.html", map[string]any{
		"Rows": preview, "HasError": hasError,
	})
}
```

Notes on choices that are plain implementation defaults, not open questions:
- Per-row `SELECT ... WHERE part_number = @p1` lookup mirrors the existing
  N+1 pattern already used in `PartBOMSave` (lines 1096–1098, 1122–1125) —
  not a new performance pattern, kept consistent rather than introducing a
  batched-lookup style nothing else in this file uses.
- A malformed line (no tab, i.e. a single pasted column) yields `pnid == 0`
  (empty or garbage "part number") and falls into the `error` branch with
  the raw text shown, same as an unresolved part number — no special-cased
  message, matching the issue's "shown in red with the raw text" spec.
- A non-numeric qty on a **non-header** data row is likewise an `error` row
  (blocks commit) rather than silently defaulting to 0 — consistent with
  "every pasted row must resolve ... before anything can be applied."
- Duplicate part numbers within one paste are not deduplicated; each line is
  diffed independently against the same pre-paste BOM snapshot. `PartBOMSave`
  itself has no dedup logic for manually-added rows either, so this doesn't
  regress anything — not introducing new behavior to solve an edge case the
  issue doesn't raise.
- Reuses `h.renderPrint` (already used by `pos.go:1086` for `po_print.html`)
  to execute the fragment template standalone, with no `layout.html` wrapper
  — the correct existing mechanism for a non-page HTML response, vs. `h.render`
  which always wraps in the nav layout.

### 3. New template — `arx_go/templates/parts/part_bom_paste_preview.html`

No `{{define}}` wrapper (matching `pos/po_print.html`'s convention for a
standalone fragment parsed via `renderPrint`, which executes the template by
its file basename):

```html
<div class="table-wrapper mb-2" style="overflow-x:auto;">
    <table class="table table-bordered table-sm">
        <thead class="table-dark">
            <tr>
                <th>Part Number</th>
                <th>Description</th>
                <th style="text-align:right;">Qty</th>
                <th>Status</th>
            </tr>
        </thead>
        <tbody id="bom-paste-preview-tbody">
            {{range .Rows}}
            <tr class="{{.RowClass}}"
                data-status="{{.Status}}"
                {{if .PLID}}data-plid="{{.PLID}}"{{end}}
                {{if .PNID}}data-pnid="{{.PNID}}"{{end}}
                data-part-number="{{.PartNumber}}"
                data-description="{{.Description}}"
                data-qty="{{printf "%g" .Qty}}">
                <td>{{.PartNumber}}</td>
                <td>{{.Description}}</td>
                <td style="text-align:right;">{{if ne .Status "error"}}{{printf "%g" .Qty}}{{end}}</td>
                <td>{{.StatusLabel}}{{if eq .Status "error"}}: <code>{{.RawText}}</code>{{end}}</td>
            </tr>
            {{end}}
        </tbody>
    </table>
</div>
{{if not .Rows}}
<p class="no-results">No rows to import.</p>
{{else}}
{{if .HasError}}
<p class="text-danger">Every pasted row must match an existing part number before importing. Fix or remove the error rows above and preview again.</p>
{{end}}
<div class="d-flex gap-2 mb-3">
    <button type="button" class="btn btn-primary" id="bom-paste-confirm-btn" {{if .HasError}}disabled{{end}} onclick="confirmBOMPasteImport()">Confirm Import</button>
    <button type="button" class="btn btn-secondary" onclick="cancelBOMPaste()">Cancel</button>
</div>
{{end}}
```

Row-highlight classes reuse Bootstrap's existing `table-success`/
`table-warning`/`table-danger` (same classes already used for the "Best"
cell highlight in `templates/pos/rfq_compare.html:42`) plus `table-secondary
text-muted` for the no-op/grayed-out case, which the issue calls out
separately from the green/yellow/red trio.

## Frontend — `arx_go/templates/parts/part_bom_edit.html`

### Markup

Insert a "Paste BOM rows" button next to the existing "+ Add Row" button
(replacing lines 42–44):

```html
<div style="margin-bottom:16px;">
    <button type="button" class="btn btn-secondary" onclick="addNewRow()" style="font-size:0.9em;">+ Add Row</button>
    <button type="button" class="btn btn-outline-secondary" onclick="togglePasteBox()" style="font-size:0.9em;">Paste BOM rows</button>
</div>

<div id="bom-paste-box" class="mb-3" style="display:none;">
    <label for="bom-paste-textarea" class="form-label">Paste part number + qty rows copied from Excel (tab-separated):</label>
    <textarea id="bom-paste-textarea" class="form-control" rows="6" placeholder="Part Number&#9;Qty"></textarea>
    <div class="mt-2 d-flex gap-2">
        <button type="button" class="btn btn-primary btn-sm" onclick="previewBOMPaste()">Preview</button>
        <button type="button" class="btn btn-secondary btn-sm" onclick="cancelBOMPaste()">Cancel</button>
    </div>
    <div id="bom-paste-error" class="text-danger mt-2" style="display:none;"></div>
</div>

<div id="bom-paste-preview"></div>
```

(`btn btn-outline-secondary` and `btn btn-primary`/`btn btn-secondary` pairs
follow the base+variant rule; `mb-3`/`mt-2`/`d-flex`/`gap-2` are Bootstrap
utilities used in place of inline margin styles, per the frontend
conventions — the surrounding pre-existing code in this file still uses
inline `style=` for things Bootstrap has no utility for, e.g. `min-width`,
so this plan doesn't touch those.)

### JS — added to the existing inline `<script>` block

This file keeps all its BOM-edit JS inline (no separate `static/parts/*.js`
file for this page, unlike the attachments page's `paste_attachment.js`), so
the new functions are added alongside `addNewRow`/`removeExistingRow`/etc.
rather than as a new static file — matching this template's own convention.

```js
function togglePasteBox() {
    const box = document.getElementById('bom-paste-box');
    box.style.display = box.style.display === 'none' ? '' : 'none';
}

function cancelBOMPaste() {
    document.getElementById('bom-paste-box').style.display = 'none';
    document.getElementById('bom-paste-textarea').value = '';
    document.getElementById('bom-paste-preview').innerHTML = '';
    document.getElementById('bom-paste-error').style.display = 'none';
}

function previewBOMPaste() {
    const errEl = document.getElementById('bom-paste-error');
    errEl.style.display = 'none';
    const text = document.getElementById('bom-paste-textarea').value;
    const csrfToken = document.querySelector('form input[name="csrf_token"]').value;
    fetch('/part/{{.Part.ID}}/bom/preview', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({ csrf_token: csrfToken, paste_text: text })
    })
        .then(r => {
            if (!r.ok) { throw new Error('Preview failed (' + r.status + ')'); }
            return r.text();
        })
        .then(html => { document.getElementById('bom-paste-preview').innerHTML = html; })
        .catch(err => { errEl.textContent = err.message; errEl.style.display = ''; });
}

function confirmBOMPasteImport() {
    const tbody = document.getElementById('bom-paste-preview-tbody');
    if (!tbody) return;
    const bomTbody = document.getElementById('bom-tbody');
    const existingItems = [...bomTbody.querySelectorAll('[name$="[PLItem]"]')]
        .map(el => parseInt(el.value) || 0);
    let nextItem = existingItems.length ? Math.max(...existingItems) + 1 : 1;

    tbody.querySelectorAll('tr').forEach(tr => {
        const status = tr.dataset.status;
        if (status === 'error' || status === 'noop') return;
        if (status === 'update') {
            const row = bomTbody.querySelector('tr[data-plid="' + tr.dataset.plid + '"]');
            if (row) { row.querySelector('[name$="[PLQty]"]').value = tr.dataset.qty; }
            return;
        }
        if (status === 'new') {
            const tpl = document.getElementById('new-row-tpl');
            const clone = tpl.content.cloneNode(true);
            const idx = newRowIdx++;
            clone.querySelectorAll('[name]').forEach(el => { el.name = el.name.replace('IDX', idx); });
            bomTbody.appendChild(clone);
            const row = bomTbody.lastElementChild;
            row.querySelector('[name$="[PLItem]"]').value = nextItem++;
            row.querySelector('[name$="[PLPartNumber]"]').value = tr.dataset.partNumber;
            row.querySelector('[name$="[PLPNID]"]').value = tr.dataset.pnid;
            row.querySelector('[name$="[PLQty]"]').value = tr.dataset.qty;
            const titleSpan = row.querySelector('.bom-title');
            if (titleSpan) { titleSpan.textContent = tr.dataset.description || ''; }
        }
    });

    cancelBOMPaste();
    document.querySelector('form[action$="/bom"]').requestSubmit();
}
```

Behavior notes (all directly specified by the issue, restated for
implementer clarity):
- **Update** rows locate the existing on-screen `<tr data-plid="...">` (same
  attribute `part_bom_edit.html:22` already renders) and overwrite its qty
  input — this also preserves any unrelated in-progress edits already sitting
  in that row's other fields, since only the qty input is touched.
- **New** rows reuse the page's own `<template id="new-row-tpl">` /
  `newRowIdx` counter exactly as `addNewRow()` does, so line-numbering for
  paste-added rows is indistinguishable from manually-added rows.
- **Confirm Import** finishes by calling `requestSubmit()` on the same
  `<form method="post" action="/part/{{.Part.ID}}/bom">` — this is the
  literal "re-submits ... to the existing POST /part/{id}/bom" behavior the
  issue specifies; it is a normal full-page submit/redirect (to
  `GET /part/{id}/bom`, matching `PartBOMSave`'s existing
  `http.Redirect(...)` at the end), not an AJAX commit. No new commit
  endpoint, no changes to `PartBOMSave`'s transaction or line-numbering code.
- No-op rows are inert by construction (skipped in the loop above), so
  re-pasting an unchanged BOM and clicking Confirm Import submits the form
  with no new/updated fields — a no-op save, matching "no DB writes" for the
  unchanged-BOM case (the existing on-screen rows still round-trip through
  `PartBOMSave`'s `UPDATE ... SET qty=@p2` with the same qty, which is a
  harmless no-op write, not a "no rows touched at all" guarantee — but no
  extra writes happen because of the paste specifically).

## PR-description note (not part of acceptance criteria)

`PartBOMSave` silently `continue`s past an unresolved part number on manual
row edits too — confirmed still present, now at `arx_go/parts.go:1100`
(`pl[...]` update loop) and `arx_go/parts.go:1127` (`new_pl[...]` insert
loop), both `if pnid == 0 { continue }`. The paste-preview flow in this issue
avoids the failure mode by blocking commit before submit, but this adjacent
pre-existing behavior is unchanged — call it out in the PR description per
the issue's "Note for implementer," don't fix it here.

## Schema / SQL

No schema changes. `bom` table (`SQL/schema.md` table-reference, line 142):
`id` PK, `parent_part_id` → `part.id`, `component_part_id` → `part.id`,
`line_number`, `qty`. The diff query joins on `component_part_id` exactly as
the issue specifies, scoped by `parent_part_id = @p1` (the part being
edited) — matches the existing `PartBOMEdit` query shape
(`arx_go/parts.go:1005-1012`).

## Testing

1. **`TestParseBOMPasteText`** (new, `arx_go/parts_test.go`) — table-driven
   unit test over the pure `parseBOMPasteText` function, no DB needed:
   - header row present (`"Part Number\tQty\nABC-100\t4"`) → header skipped,
     1 data line.
   - header row absent (`"ABC-100\t4\nDEF-200\t2"`) → first row's numeric
     qty means it's treated as data, 2 data lines.
   - blank lines interspersed → skipped.
   - a line with no tab (`"GARBAGEONLY"`) → 1 line with empty `QtyText`,
     `QtyOK=false`.
   - trailing `\r\n` (Windows clipboard line endings) → handled like `\n`.
2. Manual verification (per repo convention, not run by the implementer):
   paste a mix of new/updated/unchanged/bad part numbers into a real BOM,
   confirm the preview's four visual states match, confirm Confirm Import is
   disabled while an error row is present, confirm a re-paste of an
   unchanged BOM produces an all-no-op preview, confirm Confirm Import
   correctly lands the new/updated rows in the saved BOM after redirect.

## File change summary

- `arx_go/main.go` — add one route.
- `arx_go/parts.go` — add `bomPastePreviewRow`, `bomPasteLine`,
  `parseBOMPasteText`, `PartBOMPastePreview`.
- `arx_go/templates/parts/part_bom_paste_preview.html` — new fragment
  template.
- `arx_go/templates/parts/part_bom_edit.html` — new button/textarea/preview
  container markup, new inline JS functions.
- `arx_go/parts_test.go` — new `TestParseBOMPasteText` (pending user
  agreement per CLAUDE.md's "suggest a regression test" rule).
- `CHANGELOG.md` — new `### Added` entry under a `0.7.46` version bump.
