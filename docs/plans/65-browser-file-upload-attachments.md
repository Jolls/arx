# #65 — Replace native file picker with browser `<input type="file">` upload for attachments

Goal: part attachments and supplier attachments both import files through a standard multipart
form upload instead of `arxlib/folderpick`'s native dialog + `GET /api/browse-file` + server-side
copy from an absolute `source_path`. Move semantics are dropped (every import is a copy of the
uploaded bytes). Collision handling and `buildAttachmentFileName` carry over unchanged.

## Verified current behavior (read before trusting anything below)

- `arxlib/folderpick/folderpick.go` — `BrowseFolderContext`/`BrowseFolder` (used by Settings) and
  `BrowseFileContext`/`BrowseFile` (used only by the attachment Browse button). Both shell out to
  `powershell` + `System.Windows.Forms`. `folderpick_windows.go`/`folderpick_other.go` only supply
  `hideWindow(cmd)` — the build-tag split is *not* per-picker.
- `arx_go/api.go:212` `APIBrowseFolder`, `:218` `APIBrowseFile`; routes registered at
  `arx_go/main.go:148-149` behind `RequireAuthOnceConnected`.
- `arx_go/settings.go` UI (`templates/settings/settings.html:1086`) calls `/api/browse-folder` —
  **the folder picker stays**, so the `folderpick` package must NOT be deleted.
- `arx_go/attachments.go:176` `resolveAttachmentFileInput(ctx, r, partID, rev, category, comment, replaceName)`
  reads `FILFileName` (manual link) or `source_path` (import), reads `move_source`, `link_existing`,
  and returns `attachmentFileInput{FileName, MoveSrc, Collision, ErrMsg}`.
- `copyIntoDocControl(root, name, src string)` (`:82`) — `O_EXCL` create, returns `existed=true` on
  collision without overwriting. `replaceLocalFile(root, name, src)` (`:222`) — copy-to-`.tmp_replace`
  then swap. `writeIntoDocControl`/`writeIntoDocControlUnique`/`replaceDocControlData` are the
  byte-slice equivalents used by the clipboard-paste and DigiKey paths.
- **Confirmed: `buildAttachmentFileName` (`attachments.go:35`) takes only `partNumber, rev,
  description, category, ext` — it never sees the source path.** Only `filepath.Ext(src)` is taken
  from the source. Collision detection is purely `O_EXCL` on `<DocControlRoot>/<generated name>`.
  Both are destination-name-only and carry over untouched.
- `arx_go/parts.go:1647` `PartAttachmentCreate`, `:1698` `PartAttachmentUpdate` — call
  `resolveAttachmentFileInput`, render `ImportCollision`, and `os.Remove(in.MoveSrc)` for Move mode
  (`:1689`, `:1770` — the only two `os.` uses in `parts.go`).
- `arx_go/templates/parts/part_attachments.html` — collision "Link to existing file" form
  (lines 20-41, re-posts hidden `source_path`/`move_source`/`link_existing`), Edit form
  (`edit_source_path` line 122, Browse button line 145, Copy/Move switch lines 149-153), Add form
  (`source_path` line 186, Browse line 204, Copy/Move switch lines 208-212), and the JS
  `updateBrowsePreview`/`browseAttachment`/`clearBrowse` (lines 259-321).
- Supplier attachments: `arx_go/suppliers.go:550` `SupplierAttachmentCreate` and `:593`
  `SupplierAttachmentUpdate` take a plain `file_path` text value through `urlutil.NormalizeLink`.
  `templates/suppliers/supplier_attachments.html` Add form line 122, Edit form line 94. **No file
  picker exists today.** Supplier `LOCAL:` links are served by `ServeSupplierFile`
  (`files.go:114`) from `SupplierFilesRoot`, falling back to `DocControlRoot`.
- **Existing multipart precedent to follow:** `arx_go/files.go:275` `handleDirUpload` +
  `templates/shared/local_dir.html:26-34` (`enctype="multipart/form-data"`, `<input type="file"
  name="upload">`), and `main.go:112-127` — a global middleware wraps every request body in
  `http.MaxBytesReader(w, r.Body, maxUploadBytes)` (`files.go:17`, `100 << 20`). Its comment is
  load-bearing: `RequireCsrfOnPost` → `verifyCsrf` → `r.FormValue("csrf_token")` fully parses the
  multipart body *before any handler runs*, so a handler's own `ParseMultipartForm` is a no-op and
  the router middleware is the only place a size ceiling applies.

## Size limit decision

Reuse the existing app-wide `maxUploadBytes = 100 << 20` already applied by the router middleware.
Rationale: it is already enforced on this exact code path (it wraps every POST body), it is the
same ceiling the folder-upload feature uses, and adding a second per-route `MaxBytesReader` would
be a no-op anyway because the body is already consumed by the CSRF middleware's `FormValue` call.
No new constant. Known consequence (pre-existing, shared with folder upload): a body over the cap
fails inside `verifyCsrf`, producing `403 "Invalid form submission"` rather than a size-specific
message — see Open question 5.

## 1. `arxlib/folderpick/folderpick.go` — remove the file picker only

Delete `BrowseFileContext` (lines 39-59) and `BrowseFile` (lines 61-66). Keep
`BrowseFolderContext`/`BrowseFolder` — still used by Settings' root pickers. Keep the package and
both build-tagged `hideWindow` files unchanged. (This is the only clear-cut part of the
"delete folderpick?" question; the package itself cannot go while Settings uses the folder dialog.)

## 2. `arxlib/folderpick/folderpick_test.go`

Delete `TestBrowseFileContext_CanceledContext` (lines 29-45). Keep the two folder tests. Leave the
`os/exec` import check — verify it's still used after the deletion.

## 3. `arx_go/api.go` — remove `APIBrowseFile`

Delete `APIBrowseFile` (lines 216-220). Keep `APIBrowseFolder` and the `arx/arxlib/folderpick`
import (still used at line 213). `APIPartAttachmentName` (line 265) is unchanged — the live
filename preview keeps working, now fed by the file input's client-side name instead of
`source_path`.

## 4. `arx_go/main.go` — remove the route

Delete line 149 (`r.With(h.RequireAuthOnceConnected).Get("/api/browse-file", h.APIBrowseFile)`).
Adjust the comment above lines 146-148 so it reads as folder-picker-only (it currently says
"folder/file picker dialogs", plural).

## 5. `arx_go/attachments.go` — shared upload plumbing

### 5a. Reader-based write primitive (replaces `copyIntoDocControl`)

Replace `copyIntoDocControl(root, name, src string)` (lines 79-112) with:

```go
// copyReaderIntoDocControl streams src into <root>/<name> without overwriting.
// If the target already exists it returns (true, nil) and writes nothing.
// A failure leaves no orphan target behind.
func copyReaderIntoDocControl(root, name string, src io.Reader) (existed bool, err error)
```

Same body as today minus the `os.Open(src)` step (the caller supplies the reader). Keep the
existing `dst.Close()`-before-`os.Remove` comment — it's a Windows sharing-violation guard.

Then:
- `writeIntoDocControl` (line 117) can delegate: `return copyReaderIntoDocControl(root, name, bytes.NewReader(data))`.
  Optional but removes a duplicated `O_EXCL`/cleanup block; keep its doc comment. If you'd rather
  not touch the paste/DigiKey path at all, leave it alone — it is not required by this issue.
- `replaceLocalFile(root, name, src string)` (lines 218-241) becomes
  `replaceLocalFileFrom(root, name string, src io.Reader) error`, calling
  `copyReaderIntoDocControl` for the `.tmp_replace` sibling. Everything else identical.
- `replaceDocControlData` (line 248) is untouched (used by `api.go:594`).

### 5b. Multipart accessor, shaped for #70

```go
// attachmentUploads returns the files submitted under the given multipart form
// field, or nil when none were submitted. It returns a slice (not a single
// file) so #70 can accept multiple files per submission without reshaping the
// callers' contract; today every caller uses only the first entry.
func attachmentUploads(r *http.Request, field string) []*multipart.FileHeader
```

Implementation: `if r.MultipartForm == nil || r.MultipartForm.File == nil { return nil }; return r.MultipartForm.File[field]`.
Do **not** call `r.ParseMultipartForm` — the CSRF middleware already parsed it (see main.go comment);
on a non-multipart POST `r.MultipartForm` is nil and the function correctly returns nil.
Field name: `upload_file` (distinct from the folder-listing form's `upload`).

### 5c. `attachmentFileInput` and `resolveAttachmentFileInput`

- Drop the `MoveSrc` field from `attachmentFileInput` (line 163) and its doc comment's Move sentence.
- Rewrite `resolveAttachmentFileInput` (lines 176-216) with the same signature. New flow:
  1. `link_existing == "1"` → resolve from the carried destination name, **not** from an upload
     (the bytes are gone after a collision round-trip; browsers cannot repopulate a file input):
     take `fv(r, "link_name")`, reduce to `sanitizeFileNamePart(filepath.Base(...))`, reject empty
     or a value that doesn't `os.Stat` inside `h.cfg.DocControlRoot` with
     `ErrMsg: "That file no longer exists in Doc Control."`, else return
     `attachmentFileInput{FileName: "LOCAL:" + name}`. (Security: `link_name` is user-controllable
     form input — the `filepath.Base` + sanitize + existence check is what keeps it from escaping
     the root. `source_path` needed no such check because it was never joined to a root.)
  2. `ups := attachmentUploads(r, "upload_file")`; if empty →
     `return attachmentFileInput{FileName: urlutil.NormalizeLink(fv(r, "FILFileName"))}` (unchanged
     manual-link branch).
  3. `h.cfg.DocControlRoot == ""` → same `ErrMsg` string as today.
  4. `h.fetchPartBasic` → same `"Error loading part: "` prefix.
  5. `hdr := ups[0]`; `ext := filepath.Ext(hdr.Filename)`;
     `name := buildAttachmentFileName(p.PartNumber, rev, p.Description, category, ext)` — identical
     call, identical inputs except the ext source.
  6. `f, err := hdr.Open()` → `defer f.Close()`; error → `ErrMsg: "Error reading upload: " + err`.
  7. If `replaceName != "" && strings.EqualFold(name, replaceName)` → `replaceLocalFileFrom(root, name, f)`
     (same `"Error replacing file: "` message). Else `copyReaderIntoDocControl(root, name, f)`;
     error → `"Error copying file: "`; `existed` → return the collision map.
  8. Collision map: drop `"SourcePath"` and `"Move"` keys, keep `"Name"`, `"Category"`, `"Rev"`,
     `"OrderID"`, `"Comment"`, `"VendorScope"` (`"AttID"` is still added by the Update handler).
     `"Name"` is what the template re-posts as `link_name`.
  9. Return `attachmentFileInput{FileName: "LOCAL:" + name}` with no Move field.
- Remove the now-unused `move := fv(r, "move_source") == "1"` line.
- Imports: add `io`, `mime/multipart`, `bytes` (only if you fold `writeIntoDocControl` into the
  reader helper); `io`/`os`/`filepath`/`strings` are already there.

## 6. `arx_go/parts.go` — drop Move cleanup

- `PartAttachmentCreate` (line 1647): delete the `if in.MoveSrc != ""` block (lines 1686-1694) and
  its comment.
- `PartAttachmentUpdate` (line 1698): delete the `if in.MoveSrc != ""` block (lines 1766-1775) and
  its comment. Everything else (the `fileChanged` logic, the `deleteAttachmentFileIfUnshared` call
  at 1776-1783) is unchanged.
- Remove `"os"` from the import block (line 14) — lines 1689/1770 are its only two uses in the file.
- No signature changes: `fv()`/`r.FormValue` work unchanged on multipart forms.

## 7. `arx_go/templates/parts/part_attachments.html`

### Collision banner (lines 20-41)
- Replace `<input type="hidden" name="source_path" value="{{.ImportCollision.SourcePath}}">`
  (line 28) with `<input type="hidden" name="link_name" value="{{.ImportCollision.Name}}">`.
- Delete line 34 (`{{if .ImportCollision.Move}}...move_source...{{end}}`).
- Keep `link_existing=1` (line 35) and the rest as-is.
- The banner text "The file was *not* copied" (lines 22-23) still holds. Note for the user: unlike
  today, cancelling a collision discards the uploaded bytes — retrying with a different rev/category
  requires re-selecting the file. (See Open question 4 on wording.)

### Edit form (lines 120-176)
- Add `enctype="multipart/form-data"` to the `<form>` (line 120).
- Delete the hidden `edit_source_path` input (line 122).
- Replace the Browse button (lines 145-146) with
  `<input type="file" name="upload_file" id="edit_upload_file" class="form-control form-control-sm" style="max-width:320px;" onchange="updateBrowsePreview('edit')" {{if not $.DocControlConfigured}}disabled title="DOC_CONTROL_ROOT is not configured"{{end}}>`.
- Delete the Copy/Move switch (lines 149-153) — see Open question 2.
- Keep `edit_browse-file-name`, `edit_browse-note`, `edit_browse-preview`, `edit_browse-clear`, and
  the `edit_paste-*` clipboard elements. Change the note text (line 158) to a fixed
  "Will be copied to Doc Control as `…`" (drop the `edit_browse-verb` span).

### Add form (lines 184-237)
Mirror of the above: `enctype` on line 184, delete hidden `source_path` (line 186), replace the
Browse button (lines 204-205) with `<input type="file" name="upload_file" id="upload_file" …>`,
delete the Copy/Move switch (lines 208-212), drop the `browse-verb` span (line 217).

### JS (lines 259-321)
- `attId(mode, name)` (line 253) already maps `'upload_file'` → `upload_file`/`edit_upload_file`
  under its existing default rule — no change needed.
- `updateBrowsePreview(mode)`: delete the `move_source`/`move_mode_label`/`browse-verb` lines
  (261-265). Replace `var src = …source_path…value` (line 268) with
  `var input = document.getElementById(attId(mode,'upload_file')); var base = input && input.files[0] ? input.files[0].name : '';`
  and derive `ext` from `base` (the existing `lastIndexOf('.')` logic). When `base` is empty, clear
  the preview/note/clear-link and return. Otherwise also do what `browseAttachment` used to do:
  set `browse-file-name` text, show `browse-note`/`browse-clear`, set `FILFileName` to `base` +
  `readOnly = true` (this is what satisfies the Add form's `required` on `FILFileName`), then run
  the existing debounced `/api/part/{id}/attachment-name` fetch unchanged.
- Delete `browseAttachment` entirely (lines 287-310) and the `/api/browse-file` fetch with it.
- `clearBrowse` (lines 311-321): replace the `source_path` reset with
  `document.getElementById(attId(mode,'upload_file')).value = '';` and keep the rest.
- `DOMContentLoaded` listeners (lines 346-353) are unchanged.

## 8. `arx_go/suppliers.go` + `templates/suppliers/supplier_attachments.html` — new upload

Add the same mechanism to the supplier Add/Edit attachment forms:
`enctype="multipart/form-data"` on both forms (lines 90 and 118) and an
`<input type="file" name="upload_file" class="form-control">` row beside the existing
`file_path` text input (see Open question 3 on whether it replaces or supplements the text field).

Handler side, sharing section 5's primitives:

- `SupplierAttachmentCreate` (line 550): before the current `file_path == ""` early return, call
  `attachmentUploads(r, "upload_file")`. If a file was submitted, write it into the supplier root
  and set `filePath = "LOCAL:" + finalName` instead of using the typed `file_path`; otherwise fall
  through to today's `urlutil.NormalizeLink(file_path)` path unchanged.
- `SupplierAttachmentUpdate` (line 593): same, replacing `newFilePath` when a file is uploaded.
  The existing `fileChanged` + `deleteAttachmentFileIfUnshared` block (lines 633-639) is unchanged.
- Destination root and filename are **not decided here** — see Open questions 3a/3b. Whatever is
  chosen, factor it as one small helper (e.g. `func (h *Handler) saveSupplierUpload(hdr *multipart.FileHeader) (name string, err error)`)
  so #70 can loop it.
- Pre-existing inconsistency, flagged not fixed (out of scope, surgical-change rule):
  `SupplierAttachmentUpdate:635` deletes the old supplier file from `h.cfg.DocControlRoot`, while
  `ServeSupplierFile` reads from `SupplierFilesRoot` (falling back to `DocControlRoot`). If
  SUPPLIER_FILES_ROOT is set, that delete targets the wrong root. Mention to the user; it deserves
  its own issue.

## 9. `docs/conventions.md`

Update the "Imported attachment naming convention" section (lines ~25-46): the trigger is now a
file-upload field, not a **Browse** button; delete the two sentences describing the **Copy / Move**
toggle. Keep the naming rules, the `LOCAL:<name>` storage rule, and the collision / "Link to
existing file" paragraph (all still accurate). Add the supplier-attachment upload once 3a/3b are
settled.

## 10. `CHANGELOG.md`

One entry at the top for the PR's version bump, e.g.:
```
### Changed
- Attachment import now uses a standard browser file upload on both part and vendor Attachments tabs, replacing the native Windows file-picker dialog; the Copy/Move toggle is gone — imports are always a copy ([#65](https://github.com/Jolls/arx/issues/65))
### Removed
- `GET /api/browse-file` and `folderpick.BrowseFile`/`BrowseFileContext` ([#65](https://github.com/Jolls/arx/issues/65))
```
`arx_go/RELEASE_NOTES.md` only if this ships as part of a user-facing release.

## 11. Tests

### `arx_go/attachments_test.go` (unit, runs by default)
- `TestCopyIntoDocControl` (lines 50-91) must be rewritten against `copyReaderIntoDocControl`:
  fresh copy from a `strings.Reader`, collision leaves the target byte-identical, and a failing
  reader (`iotest.ErrReader`) leaves no orphan target (replacing today's missing-source case).
- `TestBuildAttachmentFileName` / `TestBuildResultImageName` / `TestWriteIntoDocControl*` unchanged
  — they already prove the naming convention is destination-only.
- **New**: a unit test for `attachmentUploads` — build a multipart request with
  `multipart.NewWriter`, call `r.ParseMultipartForm` (standing in for the CSRF middleware), assert
  one header back; assert `nil` for a urlencoded POST and for a multipart POST with no file part.
- **New**: a `replaceLocalFileFrom` test — existing target is swapped to the new bytes; a failing
  reader leaves the original target intact (this is the invariant the copy-then-swap exists for).

### `arx_go/integration_test.go` (build-tagged `integration`, live ArxDev — does not run in `go test ./...`)
`TestIntegration_ResolveAttachmentFileInput` (line 5081) **will not compile** after this change —
six of its seven subtests post `source_path` and two assert on `result.MoveSrc`. Required edits:
- Add a local `postMultipart(path string, fields url.Values, fileField, fileName string, body []byte) *http.Request`
  helper next to `postForm`, that calls `ParseMultipartForm` on the built request (mirroring what
  the CSRF middleware does in production).
- `manual_filfilename_no_import` — drop the `MoveSrc` assertion, otherwise unchanged.
- `doc_control_root_not_configured`, `fresh_import_copy`, `import_collision`,
  `replace_name_uses_replace_local_file`, `part_not_found` — convert to `postMultipart`.
- Delete `move_mode` (line 5139) outright.
- `link_existing_skips_copy` (line 5184) — rewrite: pre-create `wantName` in the temp doc root, post
  `url.Values{"link_existing": {"1"}, "link_name": {wantName}}` with no file, assert
  `FileName == "LOCAL:"+wantName` and that the file's bytes are untouched. Add a sibling subtest
  asserting a traversal-y `link_name` (`..\..\evil.txt`) is rejected with an `ErrMsg`.
- `import_collision` — assert `Collision["Name"] == wantName` and that `Collision["SourcePath"]`
  and `Collision["Move"]` are gone.
- The main smoke flow at line ~307-315 posts only `FILFileName`/`FILPNRev`/`category` — unaffected.
- `TestIntegration_DeleteAttachmentFileIfUnshared` and the `writeIntoDocControl` call sites at
  lines 5632/5677/5840/6076/6108 are unaffected.

### `arx_go/middleware_test.go`
`TestBuildRouter_AllAppRoutesRequireAuth` walks the router generically — removing
`GET /api/browse-file` just means one fewer visited route. No edit needed (it asserts
`visited > 0`, not an exact count — verify that when you run it).

### Not needed
No new integration test is required by this issue; the new logic is unit-testable in
`attachments_test.go`. Don't add live-DB tests beyond fixing the ones that break.

## Verification

- `cd arx_go; go build ./... ; go vet ./...` and `cd arxlib; go build ./... ; go vet ./...`
  (repo root is a workspace, not a module — `./...` from root fails).
- `cd arx_go; .\build.bat` (runs `go test ./...` first) via the PowerShell tool.
- `go vet -tags integration ./...` in `arx_go/` to prove `integration_test.go` still compiles, even
  if the live DB isn't available.
- Manual (user): on a part's Attachments tab — pick a file, confirm the live filename preview
  matches, submit, confirm the file lands in `DOC_CONTROL_ROOT` under the generated name and the
  row stores `LOCAL:<name>`; repeat to hit the collision banner and confirm "Link to existing file"
  creates a second row pointing at the same file without touching its bytes; on the Edit form,
  confirm replacing a file with the same generated name swaps it in place, and with a different
  name deletes the old file only if unshared. Then the same upload on a vendor's Attachments tab.
  Finally confirm Settings' folder-picker buttons still work (folderpick not broken).

## Resolved decisions

1. **Max upload size**: reuse the existing app-wide `maxUploadBytes = 100 MB` (`arx_go/files.go:17`).
   No new constant.

2. **Copy/Move toggle UI**: removed entirely from both Add and Edit forms, per the issue.

3. **Supplier attachment upload**:
   a. **Destination root**: `SupplierFilesRoot` only — no fallback to `DocControlRoot`. If
      `SupplierFilesRoot` is unconfigured, reject the upload with a clear error message (e.g.
      "Supplier Files Root is not configured"), matching the `DocControlRoot`-not-configured
      pattern used for part attachments. Do not disable the file input in the UI for this case —
      surface the error on submit, same as the part-attachment path does for `DocControlRoot`.
   b. **Filename convention**: sanitized original uploaded filename (no supplier-code prefix).
   c. **Collision policy**: auto-suffix (e.g. `datasheet (2).pdf`) via a unique-name helper —
      mirrors the clipboard-paste `writeIntoDocControlUnique` pattern, no new collision-banner UI
      needed for suppliers.
   d. **UI placement**: file input sits beside the existing `file_path` text field (parts-tab
      pattern) — uploading and pasting a URL/network path both remain available.

4. **Banner wording**: add a line to the collision banner noting the uploaded bytes were discarded
   and must be re-selected if the user cancels (new behavior vs. the old source_path flow, where
   cancelling didn't lose anything since the path was still on disk).

5. **Oversize-upload error**: leave as-is for #65 (pre-existing `403 "Invalid form submission"`
   behavior, shared with the folder-upload feature). File a separate follow-up issue for a
   size-aware check ahead of `RequireCsrfOnPost` — do not expand this issue's scope.

6. **`folderpick` package deletion**: confirmed NOT deleted — only `BrowseFileContext`/`BrowseFile`,
   `APIBrowseFile`, the `GET /api/browse-file` route, and `TestBrowseFileContext_CanceledContext`
   are removed. `BrowseFolder*` stays; the folder picker (Settings' IMAGE_ROOT/DOC_CONTROL_ROOT/etc.
   pickers) remains a native dialog — replacing it is out of scope for #65.
