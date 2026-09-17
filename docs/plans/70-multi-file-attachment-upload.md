# #70 — Multi-file attachment upload (batch import via one Add Attachment submission)

Blocked-by #65 (merged) — this builds on the shared multipart upload mechanism it introduced.

## Resolved decision

When Cancel is clicked at a mid-batch collision, the whole remaining batch aborts: files before the
collision stay imported, the colliding file and everything after it are not imported. This matches
today's single-file Cancel semantics most closely (user confirmed via AskUserQuestion, 2026-09-17).

In `templates/parts/part_attachments.html` step 5d below, use option (1) — `batch_remaining` is
empty on the Cancel form — and delete option (2)'s line.

## Verified current state (read before trusting line numbers — they drift)

- `arx_go/attachments.go`:
  - `attachmentUploads(r, field) []*multipart.FileHeader` (line ~168) already returns a slice;
    every caller today uses only `ups[0]`.
  - `resolveAttachmentFileInput(ctx, r, partID, rev, category, comment, replaceName) attachmentFileInput`
    (line ~184) pulls the upload itself via `attachmentUploads(r, "upload_file")`, takes `ups[0]`,
    builds the destination name via `buildAttachmentFileName(partNumber, rev, description,
    category, ext)` — **`ext` (`filepath.Ext(hdr.Filename)`) is the only thing taken from the
    original filename; the generated name never otherwise depends on it.** On collision it returns
    `attachmentFileInput{Collision: map[string]string{"Name", "Category", "Rev", "OrderID",
    "Comment", "VendorScope"}}` with the current form's field values baked in.
  - `attachmentFileInput{FileName, Collision, ErrMsg}` (line ~156).
- `arx_go/parts.go`:
  - `PartAttachmentCreate` (line ~1646) and `PartAttachmentUpdate` (line ~1688) each call
    `resolveAttachmentFileInput` once and insert/update exactly one row.
  - `renderPartAttachments(w, r, id string, extra map[string]any)` (line ~1547) merges `extra` into
    the template data via `maps.Copy` — this is how `Error`/`ImportCollision` reach the template.
- `arx_go/suppliers.go` — `SupplierAttachmentCreate`/`SupplierAttachmentUpdate` (lines ~578, ~631)
  call `attachmentUploads(r, "upload_file")` (the shared #65 helper) but **not**
  `resolveAttachmentFileInput`. On a name collision, `saveSupplierUpload` →
  `copyReaderIntoDocControlUnique` silently auto-renames with `" (2)"`, `" (3)"`, ... — there is no
  collision *decision* UI for supplier attachments at all, unlike parts.
  **Decision: this plan is scoped to part attachments only.** The issue's central problem (staging
  file bytes across the redirect a mid-batch collision causes) does not exist for suppliers, since
  suppliers never pause for a user decision — they can't collide in a way that blocks the batch.
  Making the supplier upload input `multiple` too would be a trivial, unrelated change (loop over
  `ups`, call `saveSupplierUpload` per file) with no shared code, so it's left out per "keep the
  change surgical" — file a follow-up issue if wanted.
- `arx_go/templates/parts/part_attachments.html`:
  - Collision alert box (lines ~20-41): a `<form>` posting `link_existing=1` + hidden
    `Name`/`Category`/`Rev`/`OrderID`/`Comment`/`VendorScope`/`AttID`(update only), and a plain
    `<a>` Cancel link back to the list. No rename option.
  - Add form's file input (line ~197): `<input type="file" name="upload_file" id="upload_file" ...>`
    — not `multiple` today.
  - Edit form's file input (line ~144): `id="edit_upload_file"` — replaces the *one* row's file;
    **stays single-file, untouched by this plan.**
  - `updateBrowsePreview(mode)` JS (line ~247) drives the "Will be copied to Doc Control as `<name>`"
    live preview by calling `GET /api/part/{id}/attachment-name?rev&category&ext`.
- `arx_go/main.go` (line ~122): router middleware already wraps every request body in
  `http.MaxBytesReader(w, r.Body, maxUploadBytes)` (`100 << 20`, `files.go:17`) — this already caps
  the *combined* size of a multi-file POST; no new size-limit code needed.

## Design: staging to temp files (issue's option (a))

A batch POST with N files must import them one at a time, in order. A mid-batch collision
re-renders the page — a brand-new HTTP request — so file K+1..N's bytes (living in the *original*
request's multipart body) do not exist anymore once that response is sent. Per the issue, staging
is required. Exact mechanism:

1. On the batch POST, before importing anything, copy every uploaded file's bytes into its own file
   in a fresh temp directory (`os.MkdirTemp("", "arx-attach-batch-")`), named `"<3-digit
   index><ext>"` in submission order (e.g. `000.pdf`, `001.jpg`). **Only the extension is needed
   downstream** — `buildAttachmentFileName` never uses the original filename — so the staged name
   need not preserve it.
2. Import staged files one at a time, in order (`000`, `001`, ...), via the same
   `resolveAttachmentFileInput` used for single-file upload (see signature change below), reading
   from the staged temp file instead of the original multipart upload.
3. On collision, re-render the attachments page with the temp dir path and the list of staged
   filenames still pending, encoded into hidden form fields, so the "Link to existing file" /
   Cancel POST resumes the batch from exactly where it stopped.
4. The temp dir is removed (`os.RemoveAll`) when the batch fully completes, is fully aborted, or
   any file in it errors out (not a collision — an actual I/O/DB error).
5. **Known limitation (accepted, not fixed by this plan):** if the user abandons a mid-batch
   collision page without submitting either button (closes the tab, navigates away), the temp dir
   is never cleaned up by the app. This mirrors the fact that nothing in this codebase currently
   sweeps orphaned temp state, and OS temp directories are periodically cleared independently. Not
   worth a scheduled-cleanup feature for this issue.

## 1. `arx_go/attachments.go` — generalize the upload source, add batch machinery

### 1a. Change `resolveAttachmentFileInput`'s upload parameter

Replace the `*multipart.FileHeader`-pulling-from-`r` behavior with an explicit parameter, so the
same function serves a live multipart upload *or* a staged-from-disk batch file. Add near
`attachmentFileInput` (after its definition, before `resolveAttachmentFileInput`):

```go
// attachmentUploadSource abstracts a single pending file's bytes for
// resolveAttachmentFileInput, so the same resolution logic serves both a live
// multipart upload (single-file Create/Update) and a file staged to disk from
// an earlier multi-file batch POST that's resuming after a collision decision
// (#70) — its bytes no longer live in any *http.Request by the time of resume.
type attachmentUploadSource struct {
	ext  string
	open func() (io.ReadCloser, error)
}

// multipartUploadSource wraps a directly-submitted upload.
func multipartUploadSource(fh *multipart.FileHeader) attachmentUploadSource {
	return attachmentUploadSource{
		ext:  filepath.Ext(fh.Filename),
		open: func() (io.ReadCloser, error) { return fh.Open() },
	}
}

// stagedUploadSource wraps a file previously staged to disk by
// startAttachmentBatch, resumed after a collision decision (#70).
func stagedUploadSource(path string) attachmentUploadSource {
	return attachmentUploadSource{
		ext:  filepath.Ext(path),
		open: func() (io.ReadCloser, error) { return os.Open(path) },
	}
}
```

Change the signature and body (`attachmentUploads` itself is untouched — it's still used directly
by `PartAttachmentUpdate` and both supplier handlers):

```go
// BEFORE
func (h *Handler) resolveAttachmentFileInput(ctx context.Context, r *http.Request, partID, rev, category, comment, replaceName string) attachmentFileInput {
	if fv(r, "link_existing") == "1" {
		...
	}

	ups := attachmentUploads(r, "upload_file")
	if len(ups) == 0 {
		return attachmentFileInput{FileName: urlutil.NormalizeLink(fv(r, "FILFileName"))}
	}
	if h.cfg.DocControlRoot == "" {
		return attachmentFileInput{ErrMsg: "DOC_CONTROL_ROOT is not configured; cannot import files."}
	}
	p, err := h.fetchPartBasic(ctx, partID)
	if err != nil {
		return attachmentFileInput{ErrMsg: "Error loading part: " + err.Error()}
	}
	hdr := ups[0]
	name := buildAttachmentFileName(p.PartNumber, rev, p.Description, category, filepath.Ext(hdr.Filename))
	f, err := hdr.Open()
	if err != nil {
		return attachmentFileInput{ErrMsg: "Error reading upload: " + err.Error()}
	}
	defer f.Close()

	if replaceName != "" && strings.EqualFold(name, replaceName) {
		...
```

```go
// AFTER
func (h *Handler) resolveAttachmentFileInput(ctx context.Context, r *http.Request, partID, rev, category, comment, replaceName string, upload *attachmentUploadSource) attachmentFileInput {
	if fv(r, "link_existing") == "1" {
		...  // unchanged
	}

	if upload == nil {
		return attachmentFileInput{FileName: urlutil.NormalizeLink(fv(r, "FILFileName"))}
	}
	if h.cfg.DocControlRoot == "" {
		return attachmentFileInput{ErrMsg: "DOC_CONTROL_ROOT is not configured; cannot import files."}
	}
	p, err := h.fetchPartBasic(ctx, partID)
	if err != nil {
		return attachmentFileInput{ErrMsg: "Error loading part: " + err.Error()}
	}
	name := buildAttachmentFileName(p.PartNumber, rev, p.Description, category, upload.ext)
	f, err := upload.open()
	if err != nil {
		return attachmentFileInput{ErrMsg: "Error reading upload: " + err.Error()}
	}
	defer f.Close()

	if replaceName != "" && strings.EqualFold(name, replaceName) {
		...  // unchanged
```

Everything from `if replaceName != ""` to the end of the function is unchanged (still refers to
`f`, `name`, `category`, `rev`, `r`, `comment` exactly as before — `f`'s static type changes from
`multipart.File` to `io.ReadCloser`, which `copyReaderIntoDocControl`/`replaceLocalFileFrom` already
accept as `io.Reader`).

### 1b. Add batch staging/resume/import functions

Add after `resolveAttachmentFileInput` (needs new imports `"regexp"` in this file's import block;
`io`, `os`, `path/filepath`, `strconv`, `strings`, `mime/multipart` are already imported):

```go
// batchDirPrefix names every temp directory created by a multi-file
// attachment batch (#70), so a client-supplied batch_dir value can be
// verified to be one of ours before anything touches the filesystem with it.
const batchDirPrefix = "arx-attach-batch-"

// stagedFileNamePattern matches the "NNN.ext" names startAttachmentBatch
// gives staged files. Used to validate a client-supplied batch_remaining
// value before joining it onto a filesystem path.
var stagedFileNamePattern = regexp.MustCompile(`^[0-9]{3}[A-Za-z0-9.]*$`)

// isBatchTempDir reports whether dir is exactly one of ours: a direct child
// of the OS temp directory, named with batchDirPrefix, that still exists.
func isBatchTempDir(dir string) bool {
	if !strings.HasPrefix(filepath.Base(dir), batchDirPrefix) {
		return false
	}
	if filepath.Dir(dir) != filepath.Clean(os.TempDir()) {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// startAttachmentBatch handles a multi-file Add Attachment submission (#70).
// Every uploaded file is staged to its own temp file, named "<3-digit
// index><ext>" in submission order, before any import runs — a mid-batch
// collision re-renders the page (a fresh request), and the original
// multipart upload's bytes do not survive that. Only the extension is staged
// per name because buildAttachmentFileName never uses the original filename.
func (h *Handler) startAttachmentBatch(w http.ResponseWriter, r *http.Request, id, rev, category, comment string, oID, supplierPartID, mfgPartID any, ups []*multipart.FileHeader) {
	dir, err := os.MkdirTemp("", batchDirPrefix)
	if err != nil {
		h.renderError(w, r, "Error starting import: "+err.Error())
		return
	}
	staged := make([]string, 0, len(ups))
	for i, fh := range ups {
		name := fmt.Sprintf("%03d%s", i, filepath.Ext(fh.Filename))
		src, err := fh.Open()
		if err != nil {
			os.RemoveAll(dir)
			h.renderError(w, r, "Error reading upload: "+err.Error())
			return
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			src.Close()
			os.RemoveAll(dir)
			h.renderError(w, r, "Error staging upload: "+err.Error())
			return
		}
		_, copyErr := io.Copy(dst, src)
		src.Close()
		dst.Close()
		if copyErr != nil {
			os.RemoveAll(dir)
			h.renderError(w, r, "Error staging upload: "+copyErr.Error())
			return
		}
		staged = append(staged, name)
	}
	h.importAttachmentBatch(w, r, id, rev, category, comment, oID, supplierPartID, mfgPartID, dir, staged, len(staged))
}

// resumeAttachmentBatch continues a batch after a collision decision posted
// back from the collision page (#70): "Link to existing file" (link_existing=1,
// batch_remaining includes the colliding file so it's retried and resolved via
// the link_existing branch above) or "Cancel" (batch_remaining is either empty,
// to abort the rest of the batch, or excludes just the colliding file, to skip
// it and continue — see the template's [[OPEN QUESTION]] comment). dir and
// remaining are client-supplied (hidden form fields) and are validated before
// any filesystem access.
func (h *Handler) resumeAttachmentBatch(w http.ResponseWriter, r *http.Request, id, rev, category, comment string, oID, supplierPartID, mfgPartID any, dir string) {
	if !isBatchTempDir(dir) {
		h.renderError(w, r, "This import batch has expired or is invalid. Please re-select your files.")
		return
	}
	total, _ := strconv.Atoi(fv(r, "batch_total"))
	var remaining []string
	if v := fv(r, "batch_remaining"); v != "" {
		remaining = strings.Split(v, ",")
	}
	for _, name := range remaining {
		if !stagedFileNamePattern.MatchString(name) {
			os.RemoveAll(dir)
			h.renderError(w, r, "This import batch has expired or is invalid. Please re-select your files.")
			return
		}
	}
	h.importAttachmentBatch(w, r, id, rev, category, comment, oID, supplierPartID, mfgPartID, dir, remaining, total)
}

// importAttachmentBatch imports staged[0], then recurses on staged[1:] — one
// file at a time, in submission order (#70). A collision re-renders the
// attachments page with staged[1:] (and, separately, all of staged, for the
// "Link to existing" retry) encoded into ImportCollision so the next POST can
// resume via resumeAttachmentBatch. dir is removed once staged is exhausted
// or an error (not a collision) aborts the batch.
func (h *Handler) importAttachmentBatch(w http.ResponseWriter, r *http.Request, id, rev, category, comment string, oID, supplierPartID, mfgPartID any, dir string, staged []string, total int) {
	if len(staged) == 0 {
		os.RemoveAll(dir)
		http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
		return
	}
	upload := stagedUploadSource(filepath.Join(dir, staged[0]))
	in := h.resolveAttachmentFileInput(r.Context(), r, id, rev, category, comment, "", &upload)
	if in.ErrMsg != "" {
		os.RemoveAll(dir)
		h.renderPartAttachments(w, r, id, map[string]any{"Error": in.ErrMsg})
		return
	}
	if in.Collision != nil {
		in.Collision["BatchDir"] = dir
		in.Collision["BatchTotal"] = strconv.Itoa(total)
		in.Collision["BatchIndex"] = strconv.Itoa(total - len(staged) + 1)
		// For "Link to existing file": resume must retry staged[0] itself
		// (this time taking the link_existing branch), so the full list
		// (current file included) is what that form's batch_remaining posts.
		in.Collision["BatchRemaining"] = strings.Join(staged, ",")
		h.renderPartAttachments(w, r, id, map[string]any{"ImportCollision": in.Collision})
		return
	}
	if err := h.insertAttachmentRow(r.Context(), id, in.FileName, rev, category, oID, comment, supplierPartID, mfgPartID); err != nil {
		os.RemoveAll(dir)
		h.renderError(w, r, "Error adding attachment: "+err.Error())
		return
	}
	h.importAttachmentBatch(w, r, id, rev, category, comment, oID, supplierPartID, mfgPartID, dir, staged[1:], total)
}
```

## 2. `arx_go/parts.go` — dispatch + shared insert helper

### 2a. Extract the INSERT into a helper (used by both the single-file path and the batch)

Add near `PartAttachmentCreate`:

```go
func (h *Handler) insertAttachmentRow(ctx context.Context, partID, fileName, rev, category string, oID any, comment string, supplierPartID, mfgPartID any) error {
	_, err := h.execContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_id, file_name, part_revision, category, sort_order, comment, supplier_part_id, mfg_part_id) VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8)`,
		h.cfg.AttachmentsTable(),
	), partID, fileName, rev, category, oID, comment, supplierPartID, mfgPartID)
	return err
}
```

### 2b. `PartAttachmentCreate` — dispatch to batch when >1 file, or resume when `batch_dir` is set

```go
// BEFORE
	in := h.resolveAttachmentFileInput(r.Context(), r, id, rev, category, comment, "")
	if in.ErrMsg != "" {
		h.renderPartAttachments(w, r, id, map[string]any{"Error": in.ErrMsg})
		return
	}
	if in.Collision != nil {
		h.renderPartAttachments(w, r, id, map[string]any{"ImportCollision": in.Collision})
		return
	}

	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`INSERT INTO %s (part_id, file_name, part_revision, category, sort_order, comment, supplier_part_id, mfg_part_id) VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8)`,
		h.cfg.AttachmentsTable(),
	), id, in.FileName, rev, category, oID, comment, supplierPartID, mfgPartID); err != nil {
		h.renderError(w, r, "Error adding attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}
```

```go
// AFTER — inserted right after the resolveVendorScope error check, replacing
// everything from "in := h.resolveAttachmentFileInput(...)" through the end
// of the function
	// Batch resume: a collision decision ("Link to existing file" or Cancel)
	// posted back from a mid-batch collision page (#70). batch_dir identifies
	// the temp staging directory startAttachmentBatch created for the
	// original multi-file POST.
	if dir := fv(r, "batch_dir"); dir != "" {
		h.resumeAttachmentBatch(w, r, id, rev, category, comment, oID, supplierPartID, mfgPartID, dir)
		return
	}

	ups := attachmentUploads(r, "upload_file")
	if len(ups) > 1 {
		h.startAttachmentBatch(w, r, id, rev, category, comment, oID, supplierPartID, mfgPartID, ups)
		return
	}

	var upload *attachmentUploadSource
	if len(ups) == 1 {
		u := multipartUploadSource(ups[0])
		upload = &u
	}
	in := h.resolveAttachmentFileInput(r.Context(), r, id, rev, category, comment, "", upload)
	if in.ErrMsg != "" {
		h.renderPartAttachments(w, r, id, map[string]any{"Error": in.ErrMsg})
		return
	}
	if in.Collision != nil {
		h.renderPartAttachments(w, r, id, map[string]any{"ImportCollision": in.Collision})
		return
	}

	if err := h.insertAttachmentRow(r.Context(), id, in.FileName, rev, category, oID, comment, supplierPartID, mfgPartID); err != nil {
		h.renderError(w, r, "Error adding attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}
```

(The `batch_dir` check must run before the `len(ups)` check: the resume POST from a "skip" Cancel
carries no `upload_file` at all, and must not fall into the plain zero-file/manual-link path.)

### 2c. `PartAttachmentUpdate` — wrap its single upload in the new type, unchanged otherwise

```go
// BEFORE
	in := h.resolveAttachmentFileInput(r.Context(), r, id, rev, category, comment, replaceName)
```

```go
// AFTER
	var upload *attachmentUploadSource
	if ups := attachmentUploads(r, "upload_file"); len(ups) > 0 {
		u := multipartUploadSource(ups[0])
		upload = &u
	}
	in := h.resolveAttachmentFileInput(r.Context(), r, id, rev, category, comment, replaceName, upload)
```

No route changes: batch resume reuses the existing `POST /part/{id}/attachments` (Create) route —
disambiguated by the `batch_dir` hidden field, not a new URL.

## 3. `arx_go/attachments_test.go` / `arx_go/parts_test.go` — signature-change fallout

Any existing test calling `resolveAttachmentFileInput` directly needs its call site updated to pass
`upload *attachmentUploadSource` (nil, or `&multipartUploadSource(fh)`/`&stagedUploadSource(path)`
as appropriate) as the new final argument. Search both files for `resolveAttachmentFileInput(` and
update each call; no behavioral change to those existing tests is intended.

## 4. Regression tests to add (per CLAUDE.md rule 5 — confirm with user before writing)

- `TestPartAttachmentCreate_BatchImportsAllFiles`: POST 3 files with no collisions, assert 3 rows
  inserted with the shared category/rev/comment/vendor_scope, and the temp dir is gone afterward.
- `TestPartAttachmentCreate_BatchCollisionThenLinkExisting`: POST 3 files where file 2 collides;
  assert file 1 is inserted, the response shows the collision for file 2 with `BatchIndex=2`,
  `BatchTotal=3`; submit the "Link to existing" form; assert file 2 becomes a `LOCAL:` link row and
  file 3 is then imported normally afterward (temp dir gone).
- `TestPartAttachmentCreate_BatchCollisionThenCancel`: same setup; submit whichever Cancel behavior
  was chosen for the open question; assert the resulting row count and that the temp dir is gone.
- `TestResumeAttachmentBatch_RejectsForgedDir`/`...RejectsForgedRemaining`: POST a `batch_dir`
  outside the OS temp dir (or with the wrong prefix) / a `batch_remaining` value that doesn't match
  `stagedFileNamePattern`; assert it's rejected without touching the filesystem.

## 5. `arx_go/templates/parts/part_attachments.html`

### 5a. Add form file input — accept multiple files

```html
<!-- BEFORE (line ~197) -->
<input type="file" name="upload_file" id="upload_file" class="form-control form-control-sm" style="max-width:320px;"
       onchange="updateBrowsePreview()"
       {{if not .DocControlConfigured}}disabled title="DOC_CONTROL_ROOT is not configured"{{end}}>
```

```html
<!-- AFTER -->
<input type="file" name="upload_file" id="upload_file" class="form-control form-control-sm" style="max-width:320px;" multiple
       onchange="updateBrowsePreview()"
       {{if not .DocControlConfigured}}disabled title="DOC_CONTROL_ROOT is not configured"{{end}}>
```

Edit form's `edit_upload_file` (line ~144) is **not** changed — it stays single-file.

### 5b. Add form — a place to list multiple selected filenames (Bootstrap `form-text`, matching the
existing single-file preview's styling)

```html
<!-- BEFORE (lines ~204-207) -->
<div id="browse-note" class="form-text" style="display:none;">
    Will be copied to Doc Control as <code id="browse-preview"></code>
</div>
<div id="paste-error" class="form-text text-danger" style="display:none;"></div>
```

```html
<!-- AFTER -->
<div id="browse-note" class="form-text" style="display:none;">
    Will be copied to Doc Control as <code id="browse-preview"></code>
</div>
<div id="browse-multi-list" class="form-text" style="display:none;"></div>
<div id="paste-error" class="form-text text-danger" style="display:none;"></div>
```

(No equivalent added to the Edit form — its `upload_file` is never `multiple`, so
`document.getElementById('edit_browse-multi-list')` will simply be `null`, which the JS below
already guards against.)

### 5c. `updateBrowsePreview` — branch on file count

```js
// BEFORE
function updateBrowsePreview(mode) {
    mode = mode || 'add';
    var input = document.getElementById(attId(mode, 'upload_file'));
    var base = (input && input.files[0]) ? input.files[0].name : '';
    var el = document.getElementById(attId(mode, 'browse-preview'));
    var note = document.getElementById(attId(mode, 'browse-note'));
    var clearLink = document.getElementById(attId(mode, 'browse-clear'));
    var f = document.getElementById(attId(mode, 'FILFileName'));
    if (!base) {
        if (el) { el.textContent = ''; }
        if (note) { note.style.display = 'none'; }
        if (clearLink) { clearLink.style.display = 'none'; }
        if (f) { f.value = ''; f.readOnly = false; }
        return;
    }
    if (note) { note.style.display = ''; }
    if (clearLink) { clearLink.style.display = ''; }
    if (f) { f.value = base; f.readOnly = true; }
    if (!el) { return; }
    var dot = base.lastIndexOf('.');
    var ext = dot > -1 ? base.slice(dot) : '';
    var rev = document.getElementById(attId(mode, 'FILPNRev')).value;
    var cat = document.getElementById(attId(mode, 'category')).value;
    clearTimeout(attPreviewDebounce[mode]);
    var seq = (attPreviewRequestSeq[mode] || 0) + 1;
    attPreviewRequestSeq[mode] = seq;
    attPreviewDebounce[mode] = setTimeout(function () {
        var qs = new URLSearchParams({rev: rev, category: cat, ext: ext});
        fetch('/api/part/' + ATT_PART_ID + '/attachment-name?' + qs)
            .then(function (r) { return r.json(); })
            .then(function (data) {
                if (attPreviewRequestSeq[mode] === seq) { el.textContent = data.name || ''; }
            });
    }, 250);
}
```

```js
// AFTER
function updateBrowsePreview(mode) {
    mode = mode || 'add';
    var input = document.getElementById(attId(mode, 'upload_file'));
    var files = (input && input.files) ? input.files : null;
    var el = document.getElementById(attId(mode, 'browse-preview'));
    var note = document.getElementById(attId(mode, 'browse-note'));
    var multiList = document.getElementById(attId(mode, 'browse-multi-list'));
    var clearLink = document.getElementById(attId(mode, 'browse-clear'));
    var f = document.getElementById(attId(mode, 'FILFileName'));
    if (!files || files.length === 0) {
        if (el) { el.textContent = ''; }
        if (note) { note.style.display = 'none'; }
        if (multiList) { multiList.style.display = 'none'; multiList.textContent = ''; }
        if (clearLink) { clearLink.style.display = 'none'; }
        if (f) { f.value = ''; f.readOnly = false; }
        return;
    }
    if (clearLink) { clearLink.style.display = ''; }
    if (files.length > 1) {
        // Each selected file becomes its own attachment row on submit — the
        // single-file generated-name preview doesn't apply (it depends on
        // each file's own extension), so just list what's selected instead.
        if (note) { note.style.display = 'none'; }
        if (f) { f.value = files.length + ' files selected'; f.readOnly = true; }
        if (multiList) {
            multiList.style.display = '';
            var names = [];
            for (var i = 0; i < files.length; i++) { names.push(files[i].name); }
            multiList.textContent = files.length + ' files selected: ' + names.join(', ');
        }
        return;
    }
    if (multiList) { multiList.style.display = 'none'; multiList.textContent = ''; }
    var base = files[0].name;
    if (note) { note.style.display = ''; }
    if (f) { f.value = base; f.readOnly = true; }
    if (!el) { return; }
    var dot = base.lastIndexOf('.');
    var ext = dot > -1 ? base.slice(dot) : '';
    var rev = document.getElementById(attId(mode, 'FILPNRev')).value;
    var cat = document.getElementById(attId(mode, 'category')).value;
    clearTimeout(attPreviewDebounce[mode]);
    var seq = (attPreviewRequestSeq[mode] || 0) + 1;
    attPreviewRequestSeq[mode] = seq;
    attPreviewDebounce[mode] = setTimeout(function () {
        var qs = new URLSearchParams({rev: rev, category: cat, ext: ext});
        fetch('/api/part/' + ATT_PART_ID + '/attachment-name?' + qs)
            .then(function (r) { return r.json(); })
            .then(function (data) {
                if (attPreviewRequestSeq[mode] === seq) { el.textContent = data.name || ''; }
            });
    }, 250);
}
```

(`files.length` can never exceed 1 for `mode === 'edit'` since `edit_upload_file` isn't `multiple`,
so the new branch is effectively add-form-only; no `mode` guard needed.)

### 5d. Collision alert — batch progress + hidden batch fields

```html
<!-- BEFORE (lines ~20-41) -->
{{if .ImportCollision}}
<div class="alert alert-warning">
    <p class="mb-2">A file named <strong>{{.ImportCollision.Name}}</strong> already exists in
        Doc Control. The file was <em>not</em> copied. The uploaded file has been discarded —
        cancelling below will require re-selecting it.</p>
    <form method="post"
          action="{{if .ImportCollision.AttID}}/part/{{.Part.ID}}/attachments/{{.ImportCollision.AttID}}{{else}}/part/{{.Part.ID}}/attachments{{end}}"
          class="d-inline">
        <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        <input type="hidden" name="link_name" value="{{.ImportCollision.Name}}">
        <input type="hidden" name="category" value="{{.ImportCollision.Category}}">
        <input type="hidden" name="FILPNRev" value="{{.ImportCollision.Rev}}">
        <input type="hidden" name="order_id" value="{{.ImportCollision.OrderID}}">
        <input type="hidden" name="comment" value="{{.ImportCollision.Comment}}">
        <input type="hidden" name="vendor_scope" value="{{.ImportCollision.VendorScope}}">
        <input type="hidden" name="link_existing" value="1">
        <button type="submit" class="btn btn-sm btn-warning">Link to existing file</button>
    </form>
    <a href="/part/{{.Part.ID}}/attachments{{if .ImportCollision.AttID}}?edit={{.ImportCollision.AttID}}#edit-form{{end}}"
       class="btn btn-sm btn-secondary">Cancel</a>
</div>
{{end}}
```

```html
<!-- AFTER -->
{{if .ImportCollision}}
<div class="alert alert-warning">
    {{if .ImportCollision.BatchTotal}}
    <p class="mb-1 fw-semibold">Importing file {{.ImportCollision.BatchIndex}} of {{.ImportCollision.BatchTotal}}</p>
    {{end}}
    <p class="mb-2">A file named <strong>{{.ImportCollision.Name}}</strong> already exists in
        Doc Control. The file was <em>not</em> copied.
        {{if .ImportCollision.BatchDir}}The remaining files in this batch have not been imported yet.
        {{else}}The uploaded file has been discarded — cancelling below will require re-selecting it.{{end}}</p>
    <form method="post"
          action="{{if .ImportCollision.AttID}}/part/{{.Part.ID}}/attachments/{{.ImportCollision.AttID}}{{else}}/part/{{.Part.ID}}/attachments{{end}}"
          class="d-inline">
        <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        <input type="hidden" name="link_name" value="{{.ImportCollision.Name}}">
        <input type="hidden" name="category" value="{{.ImportCollision.Category}}">
        <input type="hidden" name="FILPNRev" value="{{.ImportCollision.Rev}}">
        <input type="hidden" name="order_id" value="{{.ImportCollision.OrderID}}">
        <input type="hidden" name="comment" value="{{.ImportCollision.Comment}}">
        <input type="hidden" name="vendor_scope" value="{{.ImportCollision.VendorScope}}">
        <input type="hidden" name="link_existing" value="1">
        {{if .ImportCollision.BatchDir}}
        <input type="hidden" name="batch_dir" value="{{.ImportCollision.BatchDir}}">
        <input type="hidden" name="batch_remaining" value="{{.ImportCollision.BatchRemaining}}">
        <input type="hidden" name="batch_total" value="{{.ImportCollision.BatchTotal}}">
        {{end}}
        <button type="submit" class="btn btn-sm btn-warning">Link to existing file</button>
    </form>
    {{if .ImportCollision.BatchDir}}
    <form method="post" action="/part/{{.Part.ID}}/attachments" class="d-inline">
        <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        <input type="hidden" name="category" value="{{.ImportCollision.Category}}">
        <input type="hidden" name="FILPNRev" value="{{.ImportCollision.Rev}}">
        <input type="hidden" name="order_id" value="{{.ImportCollision.OrderID}}">
        <input type="hidden" name="comment" value="{{.ImportCollision.Comment}}">
        <input type="hidden" name="vendor_scope" value="{{.ImportCollision.VendorScope}}">
        <input type="hidden" name="batch_dir" value="{{.ImportCollision.BatchDir}}">
        <input type="hidden" name="batch_total" value="{{.ImportCollision.BatchTotal}}">
        {{/* Resolved: Cancel aborts the rest of the batch (files before the collision stay imported). */}}
        <input type="hidden" name="batch_remaining" value="">
        <button type="submit" class="btn btn-sm btn-secondary">Cancel</button>
    </form>
    {{else}}
    <a href="/part/{{.Part.ID}}/attachments{{if .ImportCollision.AttID}}?edit={{.ImportCollision.AttID}}#edit-form{{end}}"
       class="btn btn-sm btn-secondary">Cancel</a>
    {{end}}
</div>
{{end}}
```

Non-batch (single-file) collisions are unaffected: `.ImportCollision.BatchDir`/`BatchTotal` are
empty for them, so both new `{{if}}` blocks fall through to exactly today's markup and the plain
`<a>` Cancel link.

## Out of scope (per issue)

- Building the shared upload mechanism (#65, done).
- Per-file metadata entry within one batch submission — category/rev/comment/vendor scope apply to
  every file in the batch identically, same as `sort_order` (the issue doesn't call out
  `sort_order` specifically, but it's one of the "same form submission" fields with no reason to
  treat differently — every row in a batch gets the same `order_id` value entered on the form).
- Supplier attachments (see decision above).
- Any temp-directory sweep/TTL cleanup for abandoned batches (see Known limitation above).
