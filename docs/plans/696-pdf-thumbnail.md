# #696 — PDF first-page thumbnail

## Context
Issue #696 wants a downsized preview image generated from a PDF attachment's first
page, so a PDF part can show a picture the way image attachments already do. Today an
attachment only counts as a "photo" if its own filename is a known image extension
(`urlutil.IsImage`, filtered at `parts.go:338-341`); a PDF never renders a preview. No
image-resize or PDF library exists in the repo — stdlib `image/png` is used only for the
systray icon.

Decisions locked with the user:
- **PDF backend:** `github.com/klippa-app/go-pdfium` **WebAssembly (wazero) backend** —
  **no cgo**, one cross-platform binary (PDFium embedded via `go:embed`), permissive
  licenses (go-pdfium MIT / PDFium BSD-3 / wazero Apache-2). Chosen over cgo `go-fitz`
  because MuPDF is AGPL-3.0 and cgo would force mingw-w64 onto every build. Trade-off
  accepted: ~2× slower render + larger `Arx.exe` (embedded `.wasm` blob) — negligible for
  an on-demand button.
- **Storage:** generate real image files and insert **new `part_attachment` rows** (reuse
  the existing write/display machinery — no schema/model change).
- **Trigger:** **on-demand button** per PDF attachment (no auto-hook on import, no bulk
  backfill). **One thumbnail per part**, and it's **replaceable**: the button reads
  "Generate thumbnail" when none exists, "Replace thumbnail" when one does, and re-running
  **edits the existing rows in place** (same ids) rather than creating duplicates.
- **Sizes / categories:** **two** images per generation, each with its **own** category so
  they're unambiguously PDF-derived (distinct from user-pasted `Photo` rows) — lets
  regeneration find and replace exactly these rows. Render page 1 once at ~150 DPI, downsample
  to both:
  - **`PDF Preview`** (~800px) — shows in the part-detail Photos/image-preview card.
  - **`Thumbnail`** (~400px) — drives the /parts part-number hover tooltip; **kept out of the
    Photos card**.
- **Display:**
  - `PDF Preview` surfaces in the Photos card automatically — that grid filters on
    `IsImage`/filename, not category (`parts.go:338-341`); the small `Thumbnail` is excluded
    there by category.
  - `Thumbnail` shows as a hover tooltip over the part number on `/parts`.

## Build / cross-platform note
No toolchain change: the wazero backend is pure Go, so `build.bat`, `go run .`, and
`go test ./...` stay as-is (no C compiler). PDFium ships as an embedded `.wasm` — expect
`Arx.exe` to grow by roughly ten-plus MB. wazero has native compiler support for amd64/arm64
(both target platforms), so no interpreter-fallback slowdown.

## Changes

### 1. PDF→image pipeline — new file `arx_go/pdfthumb.go`
- **Pool init (once):** the wazero PDFium pool is expensive to spin up, so create it lazily
  as a package-level singleton via `sync.Once` (`webassembly.Init(webassembly.Config{...})`,
  small pool). Instances are not concurrency-safe — take one from the pool per call,
  `defer instance.Close()`.
- `renderPDFFirstPage(pdfBytes []byte) (image.Image, error)` — get an instance,
  `OpenDocument({File:&pdfBytes})`, guard page-count ≥ 1, `RenderPageInDPI({DPI:150, Page:index 0})`
  → `*image.RGBA`, close document. Return error on any failure (surfaced to the button).
- `resizeLongEdge(img image.Image, maxPx int) image.Image` — scale preserving aspect ratio
  with `golang.org/x/image/draw` (`draw.CatmullRom`); no upscaling when already smaller.
- PNG-encode via stdlib `image/png`.
- New deps in `arx_go/go.mod`: `github.com/klippa-app/go-pdfium` (+ transitive wazero) and
  `golang.org/x/image/draw`.

### 2. Generation endpoint — `arx_go/api.go`
`POST /api/part/{id}/attachments/{attID}/generate-thumbnail`, registered next to the other
`paste-attachment` routes.
- Load the source attachment row (`file_name`, `part_revision`) scoped to `part_id` (mirror
  the SELECT in `APIPartPasteAttachmentReplace`, `api.go:320-331`).
- Reject unless the file is a `LOCAL:` file (`urlutil.IsLocalFile`) **and** a PDF
  (`urlutil.IsPDF`); reject if `DocControlRoot == ""`.
- Resolve the on-disk path safely: strip `LOCAL:`, split via `urlutil.SafePathSegments`,
  `filepath.Join(DocControlRoot, segs...)` (handles a stored subdir, blocks traversal).
  Read the PDF bytes; render page 1 once.
- **Upsert both categories** (`PDF Preview` ~800px, `Thumbnail` ~400px), shared consts
  `previewCategory`/`thumbnailCategory` in `api.go`. For each: resize, PNG-encode,
  `name := buildAttachmentFileName(p.PartNumber, rev, p.Title, category, ".png")`,
  `finalName, _ := writeIntoDocControlUnique(DocControlRoot, name, ".png", data)`, then look
  up the part's existing active row for that category:
  - **exists → edit in place:** capture old `file_name`, `UPDATE ... SET file_name="LOCAL:"+finalName
    WHERE id=<existing>` (same row id, preserves any `primary_attachment_id`), then
    `deleteAttachmentFileIfUnshared` on the old file.
  - **absent → INSERT** a new row (reuse the INSERT shape at `api.go:294-297`).
  Both categories are unique to this feature, so "the existing row" is unambiguous (never a
  pasted photo).
- Return `{ok:true}`.
- **Reuses:** `fetchPartBasic`, `buildAttachmentFileName`, `writeIntoDocControlUnique`,
  `execContext`, `writeJSON/writeJSONError`.

### 3. Button — part_attachments handler (`parts.go`) + `templates/parts/part_attachments.html`
- In the handler that renders `part_attachments.html` (`parts.go`, ends at `:1522`), compute a
  `HasThumbnail bool` from the already-loaded attachment list (any active row with
  `category == thumbnailCategory`) and pass it in the template data.
- In the template, on every local-PDF row (existing `isLocalFile` + `isPDF` helpers,
  registered at `handlers.go:556`) show the action, labelled **"Replace thumbnail"** when
  `.HasThumbnail` else **"Generate thumbnail"** (both hit the same endpoint). Small fetch
  handler modeled on the page's existing paste-attachment JS (CSRF via `?csrf_token=` query,
  validated by `FormValue`); reload the list on success. Bootstrap `btn btn-sm btn-outline-secondary`.

### 4. Parts-table hover tooltip — `arx_go/parts.go` + `static/shared/app.js`
The parts list is JSON-driven: `PartsRows` (`parts.go`) returns rows, `app.js` `ROW_BUILDERS`
builds the table.
- Add a `Thumb string` field. Dialect-safe (no `TOP`/`LIMIT`): fetch in **one extra query**
  `SELECT part_id, file_name FROM <attachments> WHERE is_active=<true> AND category=@p1` (bound
  to `thumbnailCategory`), build `map[int]string` (last row wins), set `p.Thumb = localFileURL(...)`.
- In the `/api/parts/rows` builder, when `r.thumb` is set, wrap the part-number link in the
  existing `.img-hover-wrap` / `.img-hover-preview` pattern (CSS at `app.css:336-352`) so
  hovering the part number shows the thumbnail; otherwise render the plain link.

### 5. Category options `PDF Preview` + `Thumbnail` — reference + seed + migration
`category` is free-text, but add both generated categories to the managed options for consistency:
- `SQL/app_config.sql:10` and `SQL/postgres/app_config.sql` — append `,PDF Preview,Thumbnail`
  to `attachment_categories`.
- `SQL/seed_test_data.sql:107` — same append (keeps ArxDev seed in sync).
- New idempotent migration `SQL/migrations/migrate_add_thumbnail_category.sql`
  (`USE ArxDev;` + human-edits-for-prod comment) appending both values to the live `app_config`
  row only where absent. Author only — never run (ArxProd rule).

No new table, no `cfg.*Table()` helper, no `schema.md` change, no `models.Attachment` change.

## Out of scope (deferred / dropped)
Auto-generate on import; bulk backfill.

## Verification
- `cd arx_go; go vet ./...; go test ./...` — must pass (no cgo needed now).
- `arx_go\build.bat` — confirms the embedded-wasm build produces a working `Arx.exe`; sanity-
  check the size bump is acceptable.
- Unit test `resizeLongEdge` in `pdfthumb_test.go` (aspect ratio preserved, no upscale) —
  pure Go, no PDF fixture needed.
- A minimal render smoke test using a tiny checked-in single-page PDF fixture (optional) to
  prove `renderPDFFirstPage` returns a non-empty image — pure Go via wazero, safe in
  `go test ./...`.
- Manual (user, app-run is user-side): part with a local PDF attachment → Attachments tab →
  **Generate thumbnail** → confirm two rows (`PDF Preview`, `Thumbnail`); `PDF Preview` shows
  in the part Photos card and `Thumbnail` does **not**; hovering the part number on `/parts`
  shows the thumbnail tooltip. Button now reads "Replace thumbnail"; click again → both rows
  update in place (same ids, no duplicates). Non-PDF / non-local row → button absent.
