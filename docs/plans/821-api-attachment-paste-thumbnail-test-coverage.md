# #821 — Test coverage for api.go paste/thumbnail attachment handlers

Part of #801 (test coverage gap audit). Plan only — no code/branch yet.

## Files

1. **New: `arx_go/api_test.go`** (no build tag — plain unit tests, no DB).
   Covers the pure/in-process helpers: `decodePastedImage`, `isGeneratedCategory`,
   `lockPartForThumbnail`.
2. **Extend: `arx_go/integration_test.go`** (`//go:build integration`, existing file).
   Adds new `TestIntegration_*` funcs for the six DB/filesystem-backed handlers/helpers,
   plus a new write-path case for `APIRecordPasteResultImage` alongside the existing
   `TestIntegration_PasteResultImageGuards`. Follows that test's established shape:
   `liveHandler(t)` for the DB connection, `h.cfg.ImageRoot = t.TempDir()` /
   `h.cfg.DocControlRoot = t.TempDir()` to keep file writes out of the real doc-control
   tree, throwaway `ITEST-<epoch>` parts/forms/records created and torn down inline
   (no new rows added to `SQL/seed_test_data.sql`), chi route context via
   `withID`/`withIDAndAttID` helpers already in the file.

No new fixture rows in `SQL/seed_test_data.sql` — every part/form/record/attachment
needed is created inline per-test and cleaned up in a `defer`, matching every other
test in `integration_test.go`. One new in-test-only fixture: a small valid one-page
PDF, embedded as a `[]byte` literal constant in `integration_test.go` (a minimal
hand-built PDF, not a checked-in binary file), used by the thumbnail-generation tests.

## Decisions (not left open)

- **Concurrency test for `lockPartForThumbnail`**: covered at the pure-function level
  only (goroutine + channel proving a second `lockPartForThumbnail(sameID)` blocks
  until the first caller's unlock, and a different ID doesn't block). No end-to-end
  test drives two concurrent `POST .../generate-thumbnail` requests through real
  PDFium rendering — that would be timing-dependent (real render latency racing
  goroutine scheduling) for coverage the unit-level lock test already provides.
- **`APIRecordPasteResultImage`'s 409 "file already exists" branch**: not tested.
  It only trips on a same-second double-write collision (`buildResultImageName`'s
  uniqueness is 1-second-timestamp resolution) and there's no seam to inject a
  fixed clock, so a deterministic test would mean racing the wall-clock second
  boundary. Skipped as not worth the flakiness for a narrow, low-severity branch.
- **PDF fixture**: a minimal valid one-page PDF is embedded as a Go byte-slice
  constant directly in `integration_test.go`, not a checked-in binary testdata file
  — keeps the fixture inline with the test and avoids adding a binary to the repo.

## Test cases

### 1. `decodePastedImage` — `arx_go/api_test.go`, `TestDecodePastedImage`
Table-driven, one `t.Run` per case:
- Happy path per supported MIME: `image/png`→`.png`, `image/jpeg`→`.jpg`,
  `image/webp`→`.webp`, `image/gif`→`.gif` — assert returned `ext` and decoded
  `data` bytes match the input payload exactly.
- No `data:` prefix (e.g. `"image/png;base64,abc"`) → error, message
  `"image_data must be a data: URL"`.
- No comma at all (e.g. `"data:image/png;base64"`) → same error.
- Unsupported MIME (e.g. `"data:image/bmp;base64,abc"`) → error containing
  `"Unsupported image type: image/bmp"`.
- Invalid base64 payload after the comma (e.g. `"data:image/png;base64,!!!not-base64!!!"`)
  → error containing `"Invalid base64 image data"`.

### 2. `isGeneratedCategory` — `arx_go/api_test.go`, `TestIsGeneratedCategory`
Table-driven: `previewCategory`→true, `thumbnailCategory`→true, `"Photo"`→false,
`""`→false.

### 3. `lockPartForThumbnail` — `arx_go/api_test.go`, `TestLockPartForThumbnail`
- Same-partID serialization: lock `"1"`, start a goroutine that calls
  `lockPartForThumbnail("1")` and sends on a channel once acquired; assert (via
  `select` with a short timeout) the goroutine has *not* signalled while the first
  lock is held; unlock; assert the goroutine's signal now arrives promptly.
- Different partID doesn't block: while `"1"` is locked, `lockPartForThumbnail("2")`
  returns immediately (no goroutine/timeout needed — just call it and unlock it).

### 4. `APIPartAttachmentName` — `integration_test.go`, `TestIntegration_APIPartAttachmentName`
- Happy path: seed a throwaway part with known `part_number`/`title`; GET
  `/api/part/{id}/attachment-name?rev=B&category=Drawing&ext=.pdf`; assert the
  JSON `name` field equals `buildAttachmentFileName(partNumber, "B", title, "Drawing", ".pdf")`
  computed independently in the test (mirrors `TestBuildAttachmentFileName`'s exact-string
  style, not a call back into the same helper the handler uses).
- Part not found: request against a non-existent id → 404.

### 5. `APIPartPasteAttachment` — `integration_test.go`, `TestIntegration_APIPartPasteAttachment`
- Happy path: `h.cfg.DocControlRoot = t.TempDir()`; seed a throwaway part; POST valid
  tiny-PNG `image_data` + `rev`/`order_id`/`comment`; assert 200 `{"ok":true}`;
  assert a file matching `buildAttachmentFileName(..., "Photo", ".png")` exists under
  DocControlRoot with the decoded PNG bytes; assert a new `part_attachment` row exists
  with `part_id`, `category="Photo"`, `file_name` = `"LOCAL:"+that name`, `part_revision`,
  `comment`, and `sort_order` = the numeric `order_id`.
- `DOC_CONTROL_ROOT` unset (`h.cfg.DocControlRoot = ""`) → 400, "DOC_CONTROL_ROOT is not configured".
- Malformed `image_data` (not a data URL) → 400, decode error surfaces.
- Part not found → 404.
- Non-numeric `order_id` (e.g. `"abc"`) → still 200 `{"ok":true}`; assert the inserted
  row's `sort_order` is `NULL` (covers the `strconv.Atoi` failure leaving `oID` nil).

### 6. `APIPartPasteAttachmentReplace` — `integration_test.go`, `TestIntegration_APIPartPasteAttachmentReplace`
- Happy path: seed a part + an existing `part_attachment` row whose `file_name` points
  at a real file already written into `DocControlRoot` (via `writeIntoDocControl`
  directly, no HTTP); POST a new tiny-PNG payload to replace it; assert 200
  `{"ok":true}` (no `warning` key); assert the row's `file_name`/`category`/`comment`/
  `part_revision` were updated; assert the *old* file no longer exists on disk
  (deleted as unshared) and the *new* file does.
- Shared old file: seed **two** active `part_attachment` rows with the same
  `file_name`; replace one of them; assert 200 response includes a `"warning"` key
  (soft-fail path) and the old file **still exists** on disk (still referenced by the
  other row).
- Invalid `attID` (non-numeric route param) → 400 "Invalid attachment id".
- `DOC_CONTROL_ROOT` unset → 400.
- Attachment not found (wrong `part_id`/`attID` pair) → 404.
- Malformed `image_data` → 400.

### 7. `APIPartGenerateThumbnail` — `integration_test.go`, `TestIntegration_APIPartGenerateThumbnail`
- Happy path: `DocControlRoot = t.TempDir()`; write the embedded minimal one-page PDF
  fixture into DocControlRoot; seed a part + a `part_attachment` row with
  `file_name = "LOCAL:" + thatPDFName` and some non-generated `category`; POST
  `/api/part/{id}/attachments/{attID}/generate-thumbnail`; assert 200 `{"ok":true}`;
  assert two new active `part_attachment` rows now exist for the part with
  `category` = `"PDF Preview"` and `"Thumbnail"`; assert both files exist on disk
  under DocControlRoot, decode as valid PNGs, and their long edges are ≤800 / ≤250 px
  respectively (mirrors `TestResizeLongEdge`'s bounds check, applied to the real
  written files).
- Re-run in place (#839): call the endpoint a second time on the same attachment;
  assert the *same two* `part_attachment` row ids are reused (no new rows created)
  and the on-disk files were overwritten (same filenames as after the first run).
- Invalid `attID` → 400.
- `DOC_CONTROL_ROOT` unset → 400.
- Attachment not found → 404.
- Source attachment is not a local PDF (e.g. `file_name` is a plain URL, and
  separately a local `.txt` file) → 400 "Thumbnails can only be generated from a
  local PDF attachment." (two sub-cases).
- Source attachment's DB row points at a local PDF path that doesn't actually exist
  on disk → 500 "Error reading PDF".

### 8. `upsertGeneratedAttachment` — `integration_test.go`, `TestIntegration_UpsertGeneratedAttachment`
Called directly (no HTTP), isolating the find-or-create semantics from the PDF
render pipeline:
- Insert path: no existing row for `(partID, category)` → seeds nothing extra; call
  `h.upsertGeneratedAttachment(ctx, partIDStr, rev, thumbnailCategory, "LOCAL:new.png")`;
  assert a new active `part_attachment` row was created with that file/category/rev.
- Update path: pre-seed one active row for `(partID, thumbnailCategory)` pointing at
  a real file in a temp DocControlRoot; call `upsertGeneratedAttachment` with a new
  `newFile`; assert the **same row id** now has the new `file_name`/`part_revision`,
  and the old file was deleted from disk (unshared).
- Update path, shared old file: pre-seed two rows pointing at the same old file
  (one being the target category row, one an unrelated row referencing the same
  filename); call `upsertGeneratedAttachment`; assert the row was updated and no
  error is returned, and the old file **still exists** on disk (shared-file guard).

### 9. `APIRecordPasteResultImage` write path — extend `TestIntegration_PasteResultImageGuards`
(or a new sibling test, `TestIntegration_PasteResultImageWrite`, reusing its
`postPasteImage` closure/seed pattern) to cover the actual write path the existing
test explicitly skips:
- Happy path: unlocked record (reuse the guards test's part/form, seed a new
  unlocked record); POST valid tiny-PNG `image_data`; assert 200 with JSON
  `filename` matching `buildResultImageName(serial, recordID, testID, ext)`'s pattern
  (mirrors `TestBuildResultImageName`'s regex style); assert the file was actually
  written to `filepath.Join(h.cfg.ImageRoot, sanitizeFileNamePart(partNumber), filename)`
  with the correct decoded PNG bytes.
- Malformed `image_data` on an unlocked record → 400 (proves the decode-error path
  is reachable through this endpoint, not just the not-found/locked guards).

## Confirmed pattern

All DB-touching tests use the existing `//go:build integration` tag, `liveHandler(t)`
against `ARX_TEST_DSN` (must contain `arxdev`), throwaway `ITEST-<UnixNano>`-prefixed
parts/forms/records inserted and torn down via `defer`, and chi route params injected
via `withID`/`withIDAndAttID`. File-writing tests additionally point
`h.cfg.DocControlRoot` / `h.cfg.ImageRoot` at `t.TempDir()` (same technique already
used by `TestIntegration_PasteResultImageGuards` for `ImageRoot`), so no test ever
writes into the real doc-control tree and no filesystem cleanup beyond `t.TempDir()`'s
automatic removal is needed.
