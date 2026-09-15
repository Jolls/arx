# #63 — Thumbnail hover on Supplier "Linked Parts" Internal PN column

Goal: reuse the existing /parts hover-thumbnail mechanism (#696, `.pn-thumb` / `.pn-thumb-tip`,
already global in `app.js`/`app.css`) on `arx_go/templates/suppliers/supplier_parts.html`'s
Internal PN column. No new CSS/JS — the hover handler in `app.js` (~line 778-812) is a global
`document` listener keyed off `.pn-thumb` elements anywhere in the DOM, so a server-rendered page
gets it for free just by emitting the same markup `ROW_BUILDERS['/api/parts/rows']` emits.

## 1. `arx_go/models/supplier.go` — add `Thumb` field

In `SupplierPart` struct (line 38), in the "joined — part info (supplier parts view)" block
(lines 47-51, alongside `PartNumber`/`Description`/`Revision`/`Category`), add:

```go
Thumb string // joined — /parts-style hover thumbnail URL (#63), empty when part has none
```

## 2. `arx_go/suppliers.go` — `SupplierParts` handler (starts line 337)

Add the same correlated subquery `parts.go` PartsRows uses (line 180), reusing the existing
`thumbnailCategory` const (`arx_go/api.go` line 399) and `h.cfg.AttachmentsTable()`.

- SQL (lines 355-368): add a new select column right after `sp.uom_id` (or anywhere in the
  column list — order must match the `Scan` below), e.g. after line 359:

  ```go
  (SELECT MIN(file_name) FROM %s a WHERE a.part_id = pn.id AND a.is_active = %s AND a.category = @p2) AS thumb_file,
  ```

  Add `h.cfg.AttachmentsTable()` and `h.dia().BoolLiteral(true)` to the `fmt.Sprintf` args
  (matching parts.go's pattern), and append `thumbnailCategory` as a second query arg after `id`
  in the `h.queryContext(...)` call (becomes `@p2`).

- Scan (lines 383-388): add a `var thumbFile sql.NullString` and include `&thumbFile` in the
  `rows.Scan(...)` call, in the position matching the new column.

- After scan, alongside the other field assignments (~line 402-405), add:

  ```go
  if urlutil.IsLocalFile(thumbFile.String) {
      lk.Thumb = urlutil.LocalFileURL(thumbFile.String, "/local/")
  }
  ```

  (`urlutil` already imported in this file.)

## 3. `arx_go/templates/suppliers/supplier_parts.html` — Internal PN cell (line 33)

Replace:

```html
<td><a href="/part/{{.PartID}}" class="part-number-link">{{.PartNumber}}</a></td>
```

with:

```html
<td>{{if .Thumb}}<span class="pn-thumb" data-thumb="{{.Thumb}}"><a href="/part/{{.PartID}}" class="part-number-link">{{.PartNumber}}</a></span>{{else}}<a href="/part/{{.PartID}}" class="part-number-link">{{.PartNumber}}</a>{{end}}</td>
```

This matches the `/api/parts/rows` row builder in `app.js` (lines 179-181) exactly — same
`pn-thumb`/`data-thumb` wrapper, same inner link markup. `html/template` auto-escapes `{{.Thumb}}`
in the attribute context, so no manual escaping needed (unlike the JS `escHtml()` call, which is
JS-side escaping for the same reason).

## Verification

- `go build ./...` / `go vet ./...` in `arx_go/` (build.bat runs `go test ./...` too — no existing
  test covers this handler's output directly, so build+vet is the bar unless step 4 below finds one).
- Manual (user): open a supplier's Linked Parts tab where at least one linked part has an active
  `Thumbnail`-category attachment (e.g. a part also visible with a thumbnail on `/parts`) and
  confirm hovering the Internal PN shows the same preview box as `/parts`. Also check a part with
  no thumbnail renders the plain link (no `pn-thumb` wrapper, no dead hover target).

## Open questions

None — no shared Go template partial for this markup exists (confirmed: `pn-thumb`/`data-thumb`
only appear in `app.js`/`app.css`, not in any `.html` template, since `/parts` renders its rows
from JS, not `html/template`). Duplicating the ~1-line conditional markup in step 3 is the only
option and is in line with CLAUDE.md's "Go html/template has no partial-with-args story" note in
the task brief — not flagging this as a real ambiguity since there's only one viable path.
