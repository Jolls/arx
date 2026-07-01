# Browse-and-import file attachments (#547)

**Date:** 2026-07-01
**Issue:** [#547](https://github.com/Jolls/arx-legacy/issues/547) — "Add Browse button to Attachments and have it auto-move it into the right folder, and rename it per a convention"
**Milestone:** v0.6 Major Feature Release

## Summary

On a part's **Attachments** tab, add a **Browse…** button that opens a native
file picker. When the user picks a file, Arx copies it into `DOC_CONTROL_ROOT`,
renaming it to a convention derived from the part, and stores a `LOCAL:<name>`
attachment row. This removes the need to manually stage a file and hand-type a
`LOCAL:` path.

## Decisions (confirmed with user)

| Question | Decision |
|---|---|
| Target folder | `DOC_CONTROL_ROOT` root — no subfolder. Stored as `LOCAL:<name>`. |
| Naming convention | `<PartNumber>_<Category>_<Rev>.<ext>` |
| Blank Category/Rev | Skip blanks and collapse the separators (no empty `_`). |
| Move vs copy | **Copy** — leave the source file in place. |
| Name collision | **Reject the copy**, then offer to link the attachment row to the file already present. |
| Link-to-existing behavior | Create the attachment row (Category/Rev/Sort from the form) pointing at `LOCAL:<name>`; skip the copy only. |

## Scope

**In scope:** Part Attachments add form (`templates/pm/part_attachments.html` →
`PartAttachmentCreate` in `parts.go`).

**Out of scope:** Supplier attachments (`supplier_attachments.html`). The naming
and copy helpers are written package-level and reusable, but wiring them into
supplier attachments is deferred. The existing manual "File / URL" text box is
unchanged — Browse is an additive path alongside it, still supporting `http(s)://`
and hand-typed `LOCAL:` values.

## Components

### 1. Native file picker — `arxlib/folderpick`

Add `BrowseFileContext(ctx context.Context) string`, mirroring the existing
`BrowseFolderContext` but using `System.Windows.Forms.OpenFileDialog` instead of
`FolderBrowserDialog`. Returns the selected absolute path, or `""` on cancel,
timeout, or error. Add a `BrowseFile()` convenience wrapper with the same
5-minute timeout as `BrowseFolder()`.

> Note: the package is named `folderpick` but already owns native dialog
> concerns; adding a file variant here is consistent and avoids a new package.

### 2. API endpoint

`GET /api/browse-file` → `APIBrowseFile` (in `arx_go/api.go`), returning
`{"path": "<abs path>"}` via `writeJSON`. Registered in `main.go` next to
`/api/browse-folder`.

### 3. Filename helper — pure & unit-tested

```go
// buildAttachmentFileName joins the non-empty parts with "_" and appends ext.
// Each part is sanitised of filesystem-illegal characters; blanks are skipped
// so separators never double up. ext includes the leading dot, taken from the
// source file.
func buildAttachmentFileName(partNumber, category, rev, ext string) string
```

- Parts order: `partNumber`, `category`, `rev`.
- Sanitise each part against `<>:"/\|?*` and path separators (replace with `-`),
  trim surrounding whitespace. `partNumber` is always present; category/rev may
  be empty and are dropped.
- Result is a **bare base name** (no directory component), guaranteeing it cannot
  escape `DOC_CONTROL_ROOT`.

Examples:
- `("1234-567", "Drawing", "B", ".pdf")` → `1234-567_Drawing_B.pdf`
- `("1234-567", "Drawing", "", ".pdf")` → `1234-567_Drawing.pdf`
- `("1234-567", "", "", ".pdf")` → `1234-567.pdf`

### 4. Copy helper

A small helper that copies `src` → `DOC_CONTROL_ROOT/<name>` and reports whether
the target already existed, without overwriting:

```go
// copyIntoDocControl copies src to <root>/<name>.
// Returns (existed=true, nil) WITHOUT copying if the target already exists.
// Returns an error if the copy itself fails.
func copyIntoDocControl(root, name, src string) (existed bool, err error)
```

Tested against a temp dir: fresh copy, collision (no overwrite, `existed=true`),
source-missing error.

### 5. Add-form UX (`part_attachments.html`)

Below the existing File/URL row, add:

- A **Browse…** button + read-only display of the picked file's basename, backed
  by a hidden `source_path` input (empty by default).
- A helper note that previews the target name:
  *"Will be copied to Doc Control as `<PartNumber>_<Category>_<Rev>.<ext>`."*
- A **clear** link that resets `source_path` and the display back to manual entry.
- If `DOC_CONTROL_ROOT` is unconfigured, the Browse button is disabled with a
  hint (parallels the Settings gating).

JS `browseFile(...)` mirrors the existing `browseFolder(...)`: disable button,
fetch `/api/browse-file`, populate `source_path` + display on success.

Because the File/URL field is `required`, when a file is browsed the JS fills the
File/URL box with the picked basename (read-only) so the form still submits; the
server ignores that text when `source_path` is set.

### 6. Server flow — `PartAttachmentCreate`

```
if source_path == "":
    # existing behaviour, unchanged
    insert row with FILFileName = form "FILFileName"
    return

# import flow
name = buildAttachmentFileName(part.PartNumber, category, rev, ext(source_path))

if link_existing == "1":
    insert row with FILFileName = "LOCAL:" + name   # skip copy
    return

existed, err = copyIntoDocControl(DocControlRoot, name, source_path)
if err:
    re-render add form with error, no row
    return
if existed:
    re-render add form with:
      - warning "A file named <name> already exists in Doc Control."
      - a "Link to existing file" button that re-POSTs the same fields
        plus link_existing=1
    return   # no row yet

insert row with FILFileName = "LOCAL:" + name
```

The re-rendered collision form must round-trip `source_path`, `category`, `rev`,
and `order_id` so the "Link to existing file" button submits identical fields.

Guard: if `source_path` is set but `DocControlRoot == ""`, re-render with an
error rather than proceeding.

## Data flow

```
User → Browse… → /api/browse-file → OpenFileDialog → abs path
     → hidden source_path
Submit → PartAttachmentCreate
     → buildAttachmentFileName → copyIntoDocControl(DOC_CONTROL_ROOT)
        ├─ ok      → INSERT attachment (FILFileName = LOCAL:<name>)
        ├─ exists  → re-render with "Link to existing file" → INSERT (no copy)
        └─ error   → re-render with error
```

Stored `FILFileName` (`LOCAL:<name>`) is served by the existing `GET /local/*`
(`ServeLocalFile`) with no changes.

## Error handling

- Cancelled picker → `source_path` stays empty → normal add behaviour.
- Copy failure (unreadable source, permission) → add form re-rendered with the
  error; no DB row.
- Target already exists → reject + offer link (above); no overwrite ever.
- `DOC_CONTROL_ROOT` unset → Browse disabled client-side; server guard rejects a
  stray `source_path` submission.
- Sanitised bare filename cannot contain path separators → no traversal out of
  `DOC_CONTROL_ROOT`.

## Testing

Unit (default `go test ./...`):
- `buildAttachmentFileName`: full name; blank rev; blank category+rev;
  illegal-char sanitisation in part number/category.
- `copyIntoDocControl`: successful copy into temp dir; collision returns
  `existed=true` and leaves the original untouched; missing source errors.

Manual smoke:
- Browse a PDF into a part with category+rev → row appears, file opens via
  `/local/...`, source still present.
- Re-import same file → collision warning → "Link to existing file" creates the
  row without a second copy.

## Files touched

- `arxlib/folderpick/folderpick.go` — `BrowseFileContext`, `BrowseFile`
- `arx_go/api.go` — `APIBrowseFile`
- `arx_go/main.go` — route `GET /api/browse-file`
- `arx_go/parts.go` — import branch in `PartAttachmentCreate`, `buildAttachmentFileName`, `copyIntoDocControl` (+ their tests)
- `arx_go/templates/pm/part_attachments.html` — Browse row, JS, collision re-render fields
- `CHANGELOG.md` — one entry on PR
- `docs/conventions.md` — document the attachment naming convention
