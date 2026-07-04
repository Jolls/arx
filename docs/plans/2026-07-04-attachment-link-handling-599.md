# Plan — #599: Improve attachment link handling (`LOCAL:` / VBA-historical)

Issue: https://github.com/Jolls/arx-legacy/issues/599

## Context

Attachment links are stored as a raw heterogeneous string in `part_attachment.file_name`
and `company_attachment.file_path`. The **type is never stored** — it is inferred at render
time purely from the string prefix via `arxlib/urlutil` (`IsHTTPURL`, `IsLocalFile`,
`IsLocalDir`). Two problems follow:

1. **Only two shapes are recognized.** `http(s)://` and `LOCAL:` (relative to
   `DOC_CONTROL_ROOT`). Anything else — including the fully-qualified **absolute / UNC paths**
   (`\\server\share\file.pdf`, `C:\...`) that the old VBA app happily hyperlinked via its
   `Case Else` branch (`archive/PartsMaster/Files.bas:34`) — falls into the template
   `{{else}}` and renders as **dead, non-clickable monospace text**
   (`arx_go/templates/pm/part_attachments.html:73`).
2. **No auto-detection on input.** Values are stored verbatim, so the user must hand-type the
   `LOCAL:` prefix; a pasted raw path or a lowercase `local:` just becomes dead text.

### Decisions (confirmed with user)
- **Derived, no new column.** Type stays a pure function of the string — no schema change, no
  migration, nothing to keep in sync. (The value is 100% derivable; a stored column would be
  denormalized and can drift.)
- **Third link kind = `abspath`** — UNC (`\\...`) and drive-letter (`C:\`, `Z:\`) absolute
  paths, plus `file://` URLs folded in. Distinct from `LOCAL:` (which is root-relative).
- **Auto-detect + normalize on input**, and **consolidate** the scattered classify/strip logic
  into one place.
- **Abspath UX = copy-path only.** The server does **not** stream arbitrary disk/UNC files (no
  new route, no new attack surface). The row shows the path with a copy button.
- **Bare token → assume `LOCAL:`** (e.g. `spec.pdf`, `Engineering\spec.pdf` become
  `LOCAL:...`), matching the field's doc-control intent.

Final classifier enum: `url` · `localfile` · `localdir` · **`abspath`** · `unknown`.

## Changes

### 1. `arxlib/urlutil/urlutil.go` — single source of truth (new pure funcs)
- `IsAbsPath(val) bool` — true for UNC (`\\`), drive-letter (`^[A-Za-z]:[\\/]`), or `file://`.
- `StripLocalPrefix(val) string` — returns the value after `LOCAL:` (case-insensitive),
  else returns val unchanged. Replaces the hand-sliced `val[len("LOCAL:"):]` sites.
- `NormalizeLink(raw) string` — the input normalizer (pure, testable):
  - trim; empty → `""`
  - `IsHTTPURL` or `IsAbsPath` → return verbatim
  - starts with `LOCAL:` (any case) → re-emit with uppercase `LOCAL:` + original remainder
    (preserve trailing `\`/`/` so dir-ness survives)
  - otherwise (bare relative token) → `"LOCAL:" + raw`
- Keep existing `Is*`/`*URL` funcs; leave stored `LOCAL:` invariant intact.

### 2. Register `isAbsPath` for templates
- `arx_go/handlers.go` (~L315) and `arx_go/render_tr.go` (~L75) FuncMaps — add
  `"isAbsPath": urlutil.IsAbsPath`.

### 3. Normalize on the manual-input write paths
- `arx_go/attachments.go` `resolveAttachmentFileInput` (L170) — in the **manual** branch
  (`src == ""`), pass `fv(r,"FILFileName")` through `urlutil.NormalizeLink` before returning.
  The browse branch already emits canonical `LOCAL:name`, so it is untouched.
- Apply the same `NormalizeLink` to the company/supplier manual `file_path` write in
  `arx_go/suppliers.go` (attachment create/update) so both attachment surfaces behave the same.

### 4. Render `abspath` as a copy-path control (was dead text)
- `arx_go/templates/pm/part_attachments.html` (~L57-75): add `{{else if isAbsPath $f}}`
  before the final `{{else}}`. Show `fileBaseName $f` (or the full path) with a small
  **Copy path** button using `navigator.clipboard.writeText` (inline, Bootstrap
  `btn btn-sm btn-secondary`, `title="{{$f}}"`). No `<a href>` (browsers block `file://`
  from http).
- Mirror the same `abspath` branch in `supplier_attachments.html` and `supplier_detail.html`
  and (read-only display) `part_detail.html` where the link list is rendered.

### 5. Consolidate the duplicated strip/resolve logic (user asked for this)
Route these hand-rolled `LOCAL:` handlers through the new helpers — surgical, no behavior change:
- `arx_go/parts.go:1264`, `arx_go/api.go:360`, `arx_go/suppliers.go:575` — replace
  `oldFileName[len("LOCAL:"):]` with `urlutil.StripLocalPrefix(...)`.
- `arx_go/pos.go` `localFilePath` (~L2443) — replace its ad-hoc
  `strings.HasPrefix(strings.ToUpper(...),"LOCAL:")` + slice with `urlutil.IsLocalFile` +
  `urlutil.StripLocalPrefix`.

### 6. Form help text
- Update the `FILFileName` placeholder in the add/edit forms
  (`arx_go/templates/pm/part_attachments.html:187,131`) to reflect auto-detect, e.g.
  `https://…, a network/drive path (\\server\… or C:\…), or a doc-control file/folder`.

## Out of scope
- No `link_type` DB column, no migration, no schema-doc changes.
- No server route to open/stream absolute paths (copy-path only).
- Test-record result images (`app.js`) — different flow (bare filename, no `LOCAL:`); untouched.

## Tests
- `arxlib/urlutil/urlutil_test.go` — table tests for `NormalizeLink` (url passthrough, lowercase
  `local:`→`LOCAL:`, trailing-slash dir preserved, bare token→`LOCAL:`, UNC/drive/`file://`
  passthrough), `IsAbsPath`, `StripLocalPrefix`. These are the meaningful regression guard.

## Verification
- From `arx_go/`: `go build ./...`, `go vet ./...`, `go test ./...` (incl. new urlutil tests) pass.
- Manual (user): on a part's Attachments tab —
  1. Add `\\server\share\spec.pdf` → row shows a **Copy path** control (not dead text); button copies the full path.
  2. Add `local:Engineering\drawing.pdf` (lowercase) → stored/rendered as `LOCAL:` doc-control link.
  3. Add bare `spec.pdf` → stored as `LOCAL:spec.pdf`.
  4. Existing `http(s)://` and `LOCAL:` file/dir rows render exactly as before (no regression).
- Repeat (1)/(4) on a supplier's Attachments tab.
