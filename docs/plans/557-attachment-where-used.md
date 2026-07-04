# Plan — "Where used" for a file link (issue #557)

## Context

A file link can be attached in more than one place. The same physical `LOCAL:`
document (or the same `http(s)://` URL) may be linked from multiple parts and/or
suppliers, and today there is no way to see everywhere a given link is used.
Issue #557 asks for a "where used" view for a file link.

File links live in two tables, both storing the same three URL formats
(`https://…`, `LOCAL:path\file`, `LOCAL:path\dir\` — see `docs/conventions.md`):

- **`part_attachment.file_name`** (the `FILFileName` field) — links on **parts**
- **`company_attachment.file_path`** (the `FilePath` field) — links on **suppliers/companies**

The delete path already has an embryonic version of this: `deleteAttachmentFileIfUnshared`
(`arx_go/attachments.go`) does a single-table `COUNT(*)` on `file_name` to decide
whether the physical file is still referenced. "Where used" is that same idea,
generalized to **return the owning rows** and to **span both tables**.

Confirmed choices (from the discussion):
- **Option B — cross-table**: query both `part_attachment` and `company_attachment`.
- **Dedicated server-rendered results page** (no modal / no JSON endpoint / no new JS).
- Entry link shown **always** on every attachment row (a link used once just lists one place).

Out of scope: `test_result.result` images live in a separate `IMAGE_ROOT`
namespace as bare filenames (no `LOCAL:` prefix) — a different concept; excluded.

## Approach

One shared route `GET /attachments/where-used?file=<url-encoded link>` backed by a
single `UNION ALL` query across both attachment tables, joined to `part` / `company`
for a display label. Each attachment row (on the part and supplier attachment pages)
gets a small "Where used" action link carrying the row's stored link value. Results
render on a standalone page listing each part/supplier, linking to its attachments.

### Matching semantics (important nuance)

Match is on the **exact stored string** (the stored string *is* the link's
identity). SQL Server's default collation is case-insensitive, so `=` correctly
treats `LOCAL:Eng\spec.pdf` and `LOCAL:eng\spec.pdf` as the same — no `LOWER()` needed.

Caveat to document in the handler comment: part `LOCAL:` files resolve against
`DocControlRoot`, but supplier `LOCAL:` files resolve against `SupplierFilesRoot`
(which **falls back to `DocControlRoot` when unset** — see `files.go:ServeSupplierFile`).
So a `LOCAL:` string unambiguously means the same physical file across parts vs
suppliers only when those two roots coincide (the default). URLs are always
unambiguous. Exact-string match is the simplest defensible identity and is correct
in the common (default) configuration; root-aware matching is a possible follow-up.

## Changes

### 1. Route — `arx_go/main.go`

Inside the `RequireAuth` group, next to the other attachment routes (~line 178):

```go
r.Get("/attachments/where-used", h.AttachmentWhereUsed)
```

No `{id}` param — the file link is a query string so a single flat route serves
both part- and supplier-originated links.

### 2. Handler + struct — `arx_go/attachments.go`

Add `"database/sql"` to the import block (needed for `sql.NullString`).

New unexported result struct + handler:

```go
type attachmentUsage struct {
    Kind    string // "part" or "supplier"
    OwnerID int
    Code    string // part number (empty for suppliers)
    Label   string // part title or supplier name
}

func (h *Handler) AttachmentWhereUsed(w http.ResponseWriter, r *http.Request) {
    file := strings.TrimSpace(r.URL.Query().Get("file"))
    if file == "" {
        h.renderError(w, r, "No file link specified.")
        return
    }
    rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
        SELECT 'part' AS kind, p.id, p.part_number, p.title
        FROM %s fa JOIN %s p ON p.id = fa.part_id
        WHERE fa.is_active = 1 AND fa.file_name = @p1
        UNION ALL
        SELECT 'supplier' AS kind, c.id, '', c.name
        FROM %s ca JOIN %s c ON c.id = ca.supplier_id
        WHERE ca.is_active = 1 AND ca.file_path = @p1
        ORDER BY 1, 4
    `, h.cfg.AttachmentsTable(), h.cfg.PartsTable(),
        h.cfg.CompanyAttachmentsTable(), h.cfg.CompanyTable()), file)
    // scan into []attachmentUsage (part_number/title via sql.NullString),
    // then h.render(w, r, "attachment_where_used.html", map[string]any{
    //     "FileLink": file, "Usages": usages, "TestMode": h.cfg.TestMode})
}
```

Table names come only from `cfg.*Table()` helpers (`AttachmentsTable`,
`PartsTable`, `CompanyAttachmentsTable`, `CompanyTable` — all already exist). No
hardcoded names. `ORDER BY 1, 4` uses result-column ordinals (SQL Server-legal
with `UNION`): group parts before suppliers, then by label.

### 3. Template — `arx_go/templates/pm/attachment_where_used.html` (new)

Standalone page (`{{define "content"}}`, wrapped by `layout.html` via `h.render`).
Named `attachment_where_used.html` to avoid colliding with the existing
`part_where_used.html` (which is the BOM "where a *part* is used" view).

- Breadcrumb: Parts › Where used.
- Header + the file link rendered with the existing funcmap helpers
  (`isHTTPURL` / `isLocalDir` / `isLocalFile` / `localFileURL` / `localDirURL` /
  `fileBaseName`) so it's clickable when possible.
- A `table table-sm table-bordered` with columns **Type** (Bootstrap `badge` —
  "Part" / "Vendor") and **Linked from**:
  - part → `<a href="/part/{{.OwnerID}}/attachments">{{.Code}} — {{.Label}}</a>`
  - supplier → `<a href="/supplier/{{.OwnerID}}/attachments">{{.Label}}</a>`
- Footer: "Linked in N place(s)".
- Empty state (`{{else}}`): "This file link is not attached anywhere."

Note: the file-link display at the top always uses the part opener
(`localFileURL` → `/local/`). A supplier-only `LOCAL:` file whose root differs
would open against the part root — acceptable for a header convenience link;
the row links (to each owner's attachments page) are always correct.

### 4. Entry links — both attachment templates

Add a "Where used" action to each row, passing the row's link value URL-encoded
via the `urlquery` builtin. Shown always.

- **`templates/pm/part_attachments.html`** — add one `<th style="width:80px;"></th>`
  to the `<thead>` and, per row (where `{{$f := .FILFileName}}` is already in scope),
  a `<td>` action:
  ```html
  <td class="text-center">
      <a href="/attachments/where-used?file={{$f | urlquery}}"
         class="btn btn-secondary text-decoration-none" style="padding:2px 8px; font-size:0.8em;"
         title="Find where this file is linked">Where used</a>
  </td>
  ```
- **`templates/pm/supplier_attachments.html`** — same, with `{{$f := .FilePath}}`
  (already in scope at line 28) and a matching new `<th>`.

Match the existing small-action button styling used by the adjacent Edit link.

### 5. CHANGELOG.md

One entry under a new `## [0.x.y]` version:
`- Add a "where used" view showing every part and vendor that links a given file ([#557](https://github.com/Jolls/arx-legacy/issues/557))`.

## Verification

- `cd arx_go && go build ./... && go vet ./... && go test ./...` — must pass
  (`templates_parse_test.go` covers the new + edited templates; it will fail if the
  new template has a parse error or references an undefined func).
- Manual (user-run, per CLAUDE.md — I won't run the app):
  1. On a part with an attachment, click "Where used"; confirm the part is listed.
  2. Add the **same** link value (URL is easiest — e.g. `8101`'s URL) to a second
     part and/or a supplier, click "Where used" from either row, and confirm both
     the part and the vendor appear, each linking to its attachments page.
  3. Confirm a link used in exactly one place shows "Linked in 1 place".
- Integration (ArxDev only, if run): the seed already has URL-only attachments
  `8101` (part 3002) and `8102` (part 3004). A test could add a matching
  `company_attachment` fixture and assert `AttachmentWhereUsed`'s query returns both
  owner kinds — **requires an ArxDev reseed** (human action) if a new fixture is added.

## Notes / risks

- **Root ambiguity for `LOCAL:` links** (see Matching semantics): exact-string
  cross-table match can theoretically over-report if `SupplierFilesRoot` is set to
  a directory different from `DocControlRoot`. Correct in the default config; noted
  in the handler comment. Root-aware matching is a clean follow-up if needed.
- No schema change, no new table, no migration — read-only feature over existing data.
- Suggested regression test: the `UNION` query / cross-table scan is the one piece
  that could silently break (e.g. a column-order or NULL-scan mistake) — worth the
  integration test in the item above.
