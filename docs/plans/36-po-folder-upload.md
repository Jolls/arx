# Issue #36 — Add option to upload file to PO Folder

Issue title only: "Add option to upload file to PO Folder". No body/comments.

## Current state (verified in code)

- `arx_go/files.go`:
  - `safePath(root, splat)` (line 26) — sanitizes a `/`-joined splat by taking
    `filepath.Base` of each segment (drops `.`/`..`/empty), joins under `root`,
    and confirms the result is still inside `root` via `Abs` prefix check.
    Returns `("", false)` on escape.
  - `dirListingParams` (line 127) / `renderDirListing` (line 160) — reads
    `p.Path`, builds `DirEntry` rows, renders `templates/shared/local_dir.html`.
    Explicit comment: "shared by all four directory-listing sites (#864)".
    The four call sites are `ServeLocalDir` (files.go:318), `ServeSupplierDir`
    (files.go:277), `renderPOFolder` (pos.go:1101), and supplier's per-folder
    handler (suppliers.go:754, `renderSupplierFolder`-equivalent).
- `arx_go/pos.go`:
  - `findPOBaseFolder(root, poNumber)` (line 1053) — first dir in root prefixed
    by the PO number.
  - `renderPOFolder(w, r, po, subParts)` (line 1066) — resolves
    `base := root/baseName`, then `safePath(base, join(subParts))`, stats it,
    calls `h.renderDirListing` with `PO: &po`, `FileURLPrefix: /po/{num}/file`,
    `DirURLPrefix: /po/{num}/folder`. No PO-specific rendering fork — fully
    shared template/renderer.
  - `POFolder` (1113, GET `/po/{id}/folder`) and `POFolderSub` (1123, GET
    `/po/{id}/folder/*`) both call `renderPOFolder`.
  - `createPOFolder(r, poNumber, supplierIDStr)` (1980) — `os.MkdirAll`s
    `root/folderName` (folderName = number [+ " " + supplier code] [+
    "-testmode"]).
  - `POImportPartFile` (2567, POST `/po/{id}/import-part-file`) — an existing
    but *different* feature: copies a `LOCAL:`-referenced catalog attachment
    (looked up by `att_id`+`part_id` from the DB) into the PO's folder via
    `copyFile(srcPath, dst)`, where `dst := filepath.Join(root, base,
    filepath.Base(srcPath))`. Triggered from a modal on `po_detail.html:365-384`
    ("Import part file" button, not a generic file-picker upload). Not path-
    validated with `safePath` because `srcPath` comes from a DB-known
    attachment, not user-supplied text — not a template for the traversal
    check we need, but *is* the template for the CSRF/form-POST pattern.
- `arx_go/templates/shared/local_dir.html` — pure read-only listing (table of
  entries + "Copy Path" button + "Up one level" link). No form, no
  upload/multipart/enctype anywhere in the codebase (confirmed by grep).
- CSRF: `arx_go/handlers.go` `csrfToken`/`verifyCsrf`/`RequireCsrfOnPost`
  (lines 583-618) — session-stored token, `r.FormValue("csrf_token")` compared
  via constant-time compare; `RequireCsrfOnPost` middleware rejects any POST
  with a bad/missing token. `r.FormValue` calls `ParseMultipartForm` under the
  hood for `multipart/form-data` POSTs, so the existing CSRF middleware works
  unchanged for a file-upload form — no special-casing needed.
  Route `/po/{id}/import-part-file` (main.go:322) sits in the same
  CSRF-protected route group as the rest of `/po/*` — a new upload route added
  next to it inherits the same protection.

## Plan

1. **New handler `POFolderUpload` in `arx_go/pos.go`** (near `POImportPartFile`,
   since both write into a PO folder):
   - `POST /po/{id}/folder-upload` — accepts an optional subpath for uploads
     into subfolders (mirror `POFolderSub`'s splat handling: trailing route
     `/po/{id}/folder-upload/*` reusing the same `subParts` sanitization used
     in `POFolderSub`, OR keep it simple and only support uploading into the
     PO's root folder for v1 — see Open Question below on scope; recommend
     starting root-only since the issue doesn't mention subfolders).
   - Resolve `base := findPOBaseFolder(h.cfg.POFolderRoot, num)`; 404/error if
     empty ("Open the PO folder first to create it" — same message as
     `POImportPartFile`).
   - `r.ParseMultipartForm(<limit>)` then `file, header, err := r.FormFile("upload")`.
   - Compute destination filename from `header.Filename`, validate with
     `safePath(base, header.Filename)` — **required, non-negotiable**: this is
     the only place a user-controlled string (the uploaded filename, which a
     malicious client can set to anything including `../../x`) reaches a
     filesystem join, so it MUST go through `safePath` (or
     `filepath.Base(header.Filename)`, which `safePath` already does
     internally) rather than a raw `filepath.Join`. Reject with 400 on
     `!ok`.
   - Copy the multipart file reader to the destination with `os.Create` +
     `io.Copy` (mirror `copyFile` at pos.go:2548, or reuse it if signatures
     line up — `copyFile` takes two path strings, not a reader, so this needs
     its own small copy loop, not a reuse of `copyFile` verbatim).
   - Redirect back to the folder view (`/po/{num}/folder`) on success,
     matching `POImportPartFile`'s `http.Redirect(..., "/po/"+num, ...)` style
     (redirect to `/po/{num}/folder` here since that's the page the form lives
     on, not `/po/{num}`).

2. **Route registration in `arx_go/main.go`** — add
   `r.Post("/po/{id}/folder-upload", h.POFolderUpload)` next to line 322-326
   (alongside `import-part-file` and the folder GET routes). Same route group,
   so `RequireCsrfOnPost`/`RequireAuth` middleware apply automatically — no
   separate wiring needed.

3. **Template change** — add an upload form to
   `templates/pos/po_detail.html`'s "Import part file" area is NOT the right
   spot (that's the folder-browser page, not detail page). The right spot is
   `templates/shared/local_dir.html`, gated so it only renders when `.PO` is
   set (mirrors the existing `{{if .PO}}`/`{{if .Supplier}}` breadcrumb
   pattern at the top of that file) — **unless** the shared-vs-PO-only
   decision below goes the other way, in which case this becomes a
   PO-specific include/branch inside the same file instead of an unconditional
   addition. Bootstrap-styled per CLAUDE.md: a `form` with
   `enctype="multipart/form-data"`, `method="POST"`, `action="/po/{{.PO.Number}}/folder-upload"`,
   hidden `csrf_token` input (`{{.CSRFToken}}` — note `renderDirListing`'s
   `data` map at files.go:200-212 does **not** currently set `CSRFToken`; this
   must be added there, e.g. `"CSRFToken": h.csrfToken(w, r)`, or the shared
   template won't have a token to embed regardless of which handler renders
   it), an `<input type="file" name="upload" class="form-control">`, and a
   `<button type="submit" class="btn btn-primary">` — no custom CSS, following
   the `btn btn-primary`/`form-control` conventions already used elsewhere
   (e.g. `po_detail.html`'s import-file modal).

4. **`renderDirListing`/`dirListingParams` change** — add `CSRFToken` to the
   `data` map (files.go ~line 211, next to `"TestMode": h.cfg.TestMode`) via
   `h.csrfToken(w, r)`, so the new template block has a token regardless of
   which of the four call sites rendered the page. This is needed even under
   the "PO-only" scope option, since `renderPOFolder` calls the shared
   `renderDirListing`.

## Open questions (need your decision, not deciding silently)

- **Scope: PO-only vs. shared component.** The rendering path
  (`renderDirListing`/`local_dir.html`) is shared by 4 call sites (PO folders,
  supplier folders, and the two generic `/local-dir` and `/supplier-local-dir`
  browsers). Issue #36 says only "PO Folder."
  - **Option A (recommended): PO-only.** New handler/route only for PO
    folders; in `local_dir.html`, gate the upload form behind `{{if .PO}}` so
    suppliers/generic browsers are untouched. Minimal, matches the issue
    literally.
  - **Option B: Shared.** Add upload to `local_dir.html` unconditionally (or
    gated behind a new `dirListingParams.AllowUpload bool` that all 4 sites
    could opt into), so suppliers/generic folder views get upload too if a
    later issue asks. More reuse, but scope creep beyond what #36 asked for.
- **Subfolder uploads.** Should the upload form also work on nested PO
  subfolders (`/po/{id}/folder/sub/...`), or root-folder-only for v1?
  - **Option A (recommended): root-folder-only.** Simpler route/handler
    (`POST /po/{id}/folder-upload`, no splat), matches the issue's plain
    wording. A user who needs a file in a subfolder uploads to root and moves
    it via Explorer (the folder is already a real disk path they can open, per
    `POOpenFolder`).
  - **Option B: support subfolders.** Splat route
    `/po/{id}/folder-upload/*`, mirroring `POFolderSub`'s subParts handling,
    upload form's `action` built dynamically per current subpath.
- **File type/size restrictions.** Issue specifies none.
  - **Recommended:** no type restriction (matches how `LOCAL:` attachments
    behave elsewhere — any file type is copyable via `POImportPartFile`
    already). For size, recommend a generous but explicit cap passed to
    `r.ParseMultipartForm(maxBytes)` (e.g. 100 MB) purely to avoid an
    unbounded memory/disk write from a runaway upload, not because the issue
    asked for validation — flag if you'd rather have no cap at all or a
    different number.
- **Duplicate filename handling.** Uploading a file whose name already exists
  in the folder — overwrite, auto-rename (e.g. `name (1).ext`), or reject?
  - No repo precedent for this exact case (`POImportPartFile`'s `copyFile`
    calls `os.Create`, which silently overwrites, so today's *only* existing
    "put a file in a PO folder" path already overwrites silently).
  - **Recommended:** reject with a clear error ("A file named X already
    exists in this folder") rather than silently overwriting, since an
    accidental overwrite of a PO document (e.g. a signed order) is a bigger
    problem than a rejected upload — but flagging since it diverges from the
    existing `POImportPartFile` behavior and you may prefer consistency with
    that instead.

## Resolved decisions

- **Scope: Option B — shared across all folder browsers.** Add upload to `local_dir.html` unconditionally (not gated behind `{{if .PO}}`), so supplier folders and the generic `/local-dir`/`/supplier-local-dir` browsers get upload too. This means:
  - New handler(s) needed for **each** folder-browser consumer, not just PO: mirror `POFolderUpload` for suppliers (`SupplierFolderUpload` or similar, next to `ServeSupplierDir`/`renderSupplierFolder` in `suppliers.go`) and for the two generic browsers in `files.go` (`ServeLocalDir`/`ServeSupplierDir`'s upload counterparts). Route each under its own existing route group so `RequireAuth`/`RequireCsrfOnPost` apply the same way they do today.
  - The upload form in `local_dir.html` should build its `action` generically from whatever URL prefix the page already uses for other operations (check what `dirListingParams`/the `data` map already expose for the current directory's POST target — e.g. an analogous field to `FileURLPrefix`/`DirURLPrefix`; add an `UploadURLPrefix` string field to `dirListingParams` and its `data` map, set per call site: `/po/{num}/folder-upload`, `/supplier/{id}/folder-upload`, etc.) rather than special-casing `.PO`/`.Supplier` in the template.
  - Step 3/4 in the Plan section above (adding `CSRFToken` to the shared `data` map) still applies unchanged — needed regardless of scope.
- **Subfolder uploads: root-of-that-folder (i.e., wherever the user is currently browsing), not the splat-route "Option B" from the plan text above.** User clarification: "if the user is in PO 1234, it should upload the file into the PO 1234 folder... it will appear in the folder subtab" — read together with the shared-component decision, this means: upload always targets the folder currently being viewed (whichever directory `local_dir.html` is rendering for that call), the same directory `renderDirListing` already resolved via `safePath(base, subParts)` before rendering — not hardcoded to the folder's root when the user is browsing a subfolder, and not a separate splat route. Concretely: the existing resolved directory path already available inside `renderDirListing`/the handler (the same `p.Path` used to list entries) is the upload destination — so `UploadURLPrefix` per call site should include the current subpath the same way `DirURLPrefix` already does, and the upload handler re-derives the same resolved+validated directory (via the same `safePath` call the GET path already makes) rather than trusting a client-supplied path.
- **File type/size: confirmed.** No type restriction; 100 MB cap via `r.ParseMultipartForm(100 << 20)`.
- **Duplicate filename handling: confirmed.** Reject with a clear error ("A file named X already exists in this folder") rather than overwriting or auto-renaming.

## Manual verification plan

1. `go build ./...` / `go vet ./...` from `arx_go/` (per build.bat).
2. Start app in dev (`go run .` per user's own workflow — not run by the
   assistant), configure `PO_FOLDER_ROOT`, open an existing PO with a folder
   already created (or trigger folder creation via existing "Open Folder"
   flow), navigate to `/po/{id}/folder`.
3. Confirm the new upload form renders (file input + submit button,
   Bootstrap-styled, consistent with rest of page).
4. Upload a small file — confirm it appears in the listing after redirect,
   confirm the on-disk file lands in the correct PO folder path (cross-check
   against the "Copy Path" box already on the page).
5. Attempt to upload with a crafted filename via browser devtools (e.g.
   rename the file client-side to `../../evil.txt` before submit, or use
   curl/Postman directly against the endpoint with a multipart filename of
   `../evil.txt`) — confirm the server rejects it (400) rather than writing
   outside the PO folder. This is the one case worth testing via a raw HTTP
   client rather than the browser UI, since browsers themselves usually strip
   path separators from the `filename` field before it reaches the server —
   the server-side check is the real defense.
6. If Option A (PO-only) is chosen: confirm supplier folder view and
   `/local-dir` browser are visually unchanged (no stray upload form).
7. Test CSRF: submit the form with a stripped/incorrect `csrf_token` (e.g. via
   curl) and confirm 403, matching `RequireCsrfOnPost` behavior on other POST
   routes.

## Go unit tests — recommended (per CLAUDE.md rule 5)

This is a new handler introducing a fresh user-controlled-filename → filesystem
write path — exactly the "non-obvious edge case, silent-break logic" case rule
5 calls out for a regression test. Suggest (to write only if you agree):

- `TestPOFolderUpload_RejectsPathTraversalFilename` — POST a multipart form
  whose `FormFile` filename is `../evil.txt` (or `..\\evil.txt`), assert 400
  and that no file was written outside the PO folder (walk the temp root, or
  just assert the parent-of-parent directory doesn't contain the file).
- `TestPOFolderUpload_WritesFileIntoPOFolder` — happy path: valid PO/folder
  fixture (mirrors `pos_test.go`'s `TestRenderPOFolder_*` setup), POST a small
  multipart file, assert 302 redirect and the file exists at the expected
  path with the expected bytes.
- `TestPOFolderUpload_NoFolderYet` — PO exists but `findPOBaseFolder` returns
  empty (no folder created yet) — assert the same "open the PO folder first"
  error path as `POImportPartFile`.
- If Option A (duplicate-reject) is chosen for the duplicate-filename
  question: `TestPOFolderUpload_RejectsExistingFilename`.

These follow the existing `pos_test.go`/`files_test.go` conventions (table-free,
one behavior per test, `httptest.NewRecorder`/`httptest.NewRequest`,
`t.TempDir()` for `POFolderRoot`).
