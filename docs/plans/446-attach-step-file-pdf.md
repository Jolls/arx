# Plan: #446 — file-browse + PDF support for `pf_type = "attach"` steps

## Summary of the design decision (endpoint shape)

Keep the existing JSON+base64 clipboard-paste endpoint
(`POST /api/record/{id}/step/{tid}/paste-image`) exactly as it is today, still
capped at 8MB, and **add a second endpoint** for file-browse:

```
POST /api/record/{id}/step/{tid}/upload-result-file
Content-Type: multipart/form-data; field name "file"
```

Rationale: this mirrors the codebase's own existing split for part/company
attachments — a small base64 JSON endpoint for clipboard paste
(`decodeJSONBody`, 8MB cap, used only by paste endpoints) vs. a multipart
upload path (`attachmentUploads`/`multipartUploadSource` in
`arx_go/attachments.go`) for anything coming from a file picker. A multipart
upload is already parsed by the global `RequireCsrfOnPost` middleware
(`arx_go/handlers.go:938`, calls `r.ParseMultipartForm(32<<20)`) and is
already bounded by the global `maxUploadBytes` `MaxBytesReader` wrapper in
`arx_go/main.go` (~100MB) applied to every POST body before that — so the new
endpoint needs **no new size-cap code**, and a multi-page PDF is not
3-base64-encoded-bytes-per-source-byte like the JSON path would make it.

Response shape for the new endpoint matches the existing one exactly —
`{"ok": true, "filename": "<name>"}` — so the client reuses
`applyPastedResultImage` for both paths.

## MIME/extension allow-list — do not touch the shared `pasteImageExts` map

`pasteImageExts` (`arx_go/api.go:380`) is reused by `decodePastedImage` for
**three** existing callers: `APIPartPasteAttachment`,
`APIPartPasteAttachmentReplace` (clipboard-paste of a part "Photo"), and
indirectly its values are also the allow-list `SettingsCompanyLogoSave`
(`arx_go/settings.go:436`) checks a sniffed MIME against. None of those three
should start accepting PDFs. So:

- Leave `pasteImageExts` and `decodePastedImage` untouched.
- Add a new map in `arx_go/api.go`, next to `pasteImageExts`:
  ```go
  // attachResultExts extends pasteImageExts with application/pdf for
  // test-record attach-step results only (#446) — kept separate so the
  // part-Photo clipboard-paste endpoints and the company-logo upload (which
  // both reuse pasteImageExts) do not start accepting PDFs.
  var attachResultExts = map[string]string{
      "image/png":      ".png",
      "image/jpeg":     ".jpg",
      "image/webp":     ".webp",
      "image/gif":      ".gif",
      "application/pdf": ".pdf",
  }
  ```
- Refactor `decodePastedImage` into a generic helper + thin wrapper so both
  maps can be validated with the same logic:
  ```go
  // decodeDataURL parses a "data:<mime>;base64,<data>" URL into a file
  // extension (looked up in exts) and the raw decoded bytes.
  func decodeDataURL(dataURL string, exts map[string]string) (ext string, data []byte, err error) {
      commaIdx := strings.Index(dataURL, ",")
      if commaIdx < 0 || !strings.HasPrefix(dataURL, "data:") {
          return "", nil, fmt.Errorf("image_data must be a data: URL")
      }
      header := dataURL[len("data:"):commaIdx]
      mime := strings.TrimSuffix(header, ";base64")
      ext, ok := exts[mime]
      if !ok {
          return "", nil, fmt.Errorf("Unsupported image type: %s", mime)
      }
      data, err = base64.StdEncoding.DecodeString(dataURL[commaIdx+1:])
      if err != nil {
          return "", nil, fmt.Errorf("Invalid base64 image data: %w", err)
      }
      return ext, data, nil
  }

  // decodePastedImage decodes a data: URL restricted to the four pasteable
  // image types. Used by the part-Photo / company-logo clipboard-paste paths.
  func decodePastedImage(dataURL string) (ext string, data []byte, err error) {
      return decodeDataURL(dataURL, pasteImageExts)
  }
  ```
- In `APIRecordPasteResultImage`, change the single call site from
  `decodePastedImage(body.ImageData)` to
  `decodeDataURL(body.ImageData, attachResultExts)`. No other behavior in that
  handler changes.

This keeps `TestDecodePastedImage` (`arx_go/api_test.go`) passing unmodified
(it calls `decodePastedImage`, which still only accepts the four image types),
and gives attach-step clipboard paste the extra PDF type without touching any
other caller.

## 1. `arx_go/api.go` — new multipart upload handler

Add `APIRecordUploadResultFile`, placed next to
`APIRecordPasteResultImage` (~line 787). Factor out the shared
guard+directory logic first so the two handlers do not duplicate it:

```go
// loadAttachStepTarget validates a test-record step is a writable attach
// target (record exists, not locked, IMAGE_ROOT configured) and returns the
// per-part image directory to write into, creating it if needed. Shared by
// APIRecordPasteResultImage and APIRecordUploadResultFile.
func (h *Handler) loadAttachStepTarget(ctx context.Context, recordID int) (dir, serial string, err error) {
    if h.cfg().ImageRoot == "" {
        return "", "", errImageRootNotConfigured // sentinel; handlers map this to the existing 400 message
    }
    rec, err := records.New(handlerDB{h}).GetHeader(ctx, recordID)
    if err != nil {
        return "", "", err // sql.ErrNoRows or other — caller distinguishes
    }
    if rec.Locked {
        return "", "", errRecordLocked
    }
    dir = filepath.Join(h.cfg().ImageRoot, sanitizeFileNamePart(rec.PartNumber))
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return "", "", err
    }
    return dir, rec.Serial, nil
}
```

Do not over-engineer this helper — if, once written, the error-sentinel
plumbing (`errImageRootNotConfigured`/`errRecordLocked`) ends up uglier than
just leaving the two handlers' guard blocks duplicated (they are ~15 lines
each, already written and tested in `APIRecordPasteResultImage`), it is fine
to skip the refactor and copy the guard block verbatim into the new handler
instead. Either is acceptable; prefer whichever reads cleaner once both
handlers exist side by side. The important constraint is: **the existing
`APIRecordPasteResultImage` behavior/response bodies/status codes must not
change** (its two integration tests assert on them verbatim).

New handler:

```go
// APIRecordUploadResultFile saves a file browsed from disk to the same
// location/naming scheme as APIRecordPasteResultImage, for a test-record
// result step (pf_type = "attach"), accepting the same four image types plus
// PDF (#446). POST /api/record/{id}/step/{tid}/upload-result-file,
// multipart/form-data, field "file".
func (h *Handler) APIRecordUploadResultFile(w http.ResponseWriter, r *http.Request) {
    recordID, err := strconv.Atoi(r.PathValue("id"))
    if err != nil {
        writeJSONError(w, http.StatusBadRequest, "Invalid record id")
        return
    }
    testID, err := strconv.Atoi(r.PathValue("tid"))
    if err != nil {
        writeJSONError(w, http.StatusBadRequest, "Invalid test id")
        return
    }
    if h.cfg().ImageRoot == "" {
        writeJSONError(w, http.StatusBadRequest, "IMAGE_ROOT is not configured; cannot save uploaded files.")
        return
    }

    rec, err := records.New(handlerDB{h}).GetHeader(r.Context(), recordID)
    serial, partNumber, locked := rec.Serial, rec.PartNumber, rec.Locked
    if err == sql.ErrNoRows {
        writeJSONError(w, http.StatusNotFound, "Record not found")
        return
    }
    if err != nil {
        writeJSONError(w, http.StatusInternalServerError, "Error loading record: "+err.Error())
        return
    }
    if locked {
        writeJSONError(w, http.StatusConflict, "Record is locked")
        return
    }

    // RequireCsrfOnPost has already called r.ParseMultipartForm (see
    // attachmentUploads's doc comment in attachments.go) — do not call it
    // again here.
    ups := attachmentUploads(r, "file")
    if len(ups) == 0 {
        writeJSONError(w, http.StatusBadRequest, "No file was uploaded.")
        return
    }
    f, err := ups[0].Open()
    if err != nil {
        writeJSONError(w, http.StatusBadRequest, "Error reading upload: "+err.Error())
        return
    }
    data, err := io.ReadAll(f)
    f.Close()
    if err != nil {
        writeJSONError(w, http.StatusBadRequest, "Error reading upload: "+err.Error())
        return
    }

    // Validated by sniffing content (http.DetectContentType), the same
    // approach SettingsCompanyLogoSave uses — not by trusting the browsed
    // file's name/extension, so a renamed/disguised file is still rejected.
    mime := http.DetectContentType(data)
    ext, ok := attachResultExts[mime]
    if !ok {
        writeJSONError(w, http.StatusBadRequest, "Unsupported file type: "+mime)
        return
    }

    dir := filepath.Join(h.cfg().ImageRoot, sanitizeFileNamePart(partNumber))
    if err := os.MkdirAll(dir, 0o755); err != nil {
        writeJSONError(w, http.StatusInternalServerError, "Error creating image folder: "+err.Error())
        return
    }

    name := buildResultImageName(serial, recordID, testID, ext)
    existed, err := writeIntoDocControl(dir, name, data)
    if err != nil {
        writeJSONError(w, http.StatusInternalServerError, "Error saving file: "+err.Error())
        return
    }
    if existed {
        writeJSONError(w, http.StatusConflict, "A file with this name was just created; please try again.")
        return
    }

    writeJSON(w, map[string]any{"ok": true, "filename": name})
}
```

`io` and `net/http` are already imported in `api.go`; confirm
`http.DetectContentType` doesn't need a new import (it's `net/http`, already
imported).

Note `http.DetectContentType` sniffs `%PDF-` as `application/pdf` and the
four image formats by their standard magic bytes — same mechanism
`SettingsCompanyLogoSave` already relies on, so this is a proven pattern in
this codebase, not a new one.

## 2. `arx_go/main.go` — route registration

Add, next to the existing paste-image route (~line 459):

```go
b.handle(http.MethodPost, "/api/record/{id}/step/{tid}/upload-result-file", withAuth, h.APIRecordUploadResultFile)
```

This is a POST route, so it is not subject to `route_budget_test.go` (that
budget table only covers GET routes) — no update needed there.

## 3. `arx_go/trfiles.go` — `ServeImage` Content-Type fix

`ServeImage` (`arx_go/trfiles.go:11`) currently hardcodes
`w.Header().Set("Content-Type", "image/png")` for every file under
`IMAGE_ROOT`, regardless of actual extension. A PDF served through
`/images/<part>/<file>.pdf` with that header will not open correctly in most
browsers (shows as a failed/blank "image"). Fix:

```go
	if strings.EqualFold(filepath.Ext(fullPath), ".pdf") {
		w.Header().Set("Content-Type", "application/pdf")
	} else {
		w.Header().Set("Content-Type", "image/png")
	}
	w.Header().Set("Content-Disposition", "inline")
	http.ServeFile(w, r, fullPath)
```

This needs `path/filepath` imported in `trfiles.go` (not currently imported —
check and add). Do not touch the `.PNG`-auto-append fallback logic above it;
it only triggers when the exact requested path doesn't exist, which will not
happen for a freshly-written `.pdf` file.

## 4. `arx_go/render_records.go` — rendering logic

### `isImageRow` / new `isFileRow`

Current `isImageRow` (line 181) treats every non-empty `pf_type="attach"`
result as an image. Split it so a PDF result is excluded from the image
gallery and instead picked up by a new file-list:

```go
// isImageRow returns true for a Level-0 result row that holds a pasted
// image (not a PDF): either a pf_type="attach" step with a recorded
// non-PDF value, or a value matching the legacy/new image filename shape
// (for rows whose form_row predates the #587 migration to pf_type="attach").
func isImageRow(row models.ResultRow) bool {
	val := row.EffectiveValue()
	if val == "" {
		return false
	}
	if urlutil.IsPDF(val) {
		return false
	}
	if row.Step != nil && row.Step.PFType == "attach" {
		return true
	}
	return imageResult(val)
}

// isFileRow returns true for a Level-0 attach-step result that holds a
// non-image attachment (currently only PDF) rather than a pasted image
// (#446) — rendered as a plain link/file-list entry instead of a thumbnail.
func isFileRow(row models.ResultRow) bool {
	val := row.EffectiveValue()
	if val == "" {
		return false
	}
	if !urlutil.IsPDF(val) {
		return false
	}
	if row.Step != nil && row.Step.PFType == "attach" {
		return true
	}
	return imageResult(val)
}
```

(`urlutil` is already imported in `render_records.go`.)

### Inline result-cell rendering (not the gallery)

`records_show.html`'s main table cell (lines ~214–238) and
`record_print.html`'s main table cell (line 97) both branch on
`imageResult $val` directly (not through `isImageRow`), independent of the
gallery. Both need the same PDF carve-out, keyed off `isPDF $val` (already a
registered template func, `urlutil.IsPDF`, takes a bare filename — `$val` is
already the bare filename here, same as the gallery code uses it).

`records_show.html` (replace the existing `{{if imageResult $val}}` block,
lines 215–222):

```html
{{if imageResult $val}}
  {{if isPDF $val}}
          {{$fileURL := imageURL $.Form.PartNumber $val}}
          <a href="{{$fileURL}}" target="_blank">
            <i class="bi bi-file-pdf text-danger"></i> {{$val}}
          </a>
  {{else}}
          {{$imgURL := imageURL $.Form.PartNumber $val}}
          <span class="img-hover-wrap" data-src="{{$imgURL}}">
            <a href="{{$imgURL}}" target="_blank">
              <i class="bi bi-image"></i> View
            </a>
            <span class="img-hover-preview"><img alt="preview"></span>
          </span>
  {{end}}
{{else if isLocalFile $val}}
  ... (unchanged)
```

`record_print.html` line 97, replace:

```html
<td>{{if imageResult $val}}[see gallery]{{else if isLocalFile $val}}[file: {{fileBaseName $val}}]{{else}}{{applyFormat $val .Step.Format}}{{end}}</td>
```

with:

```html
<td>{{if imageResult $val}}{{if isPDF $val}}[see attachments]{{else}}[see gallery]{{end}}{{else if isLocalFile $val}}[file: {{fileBaseName $val}}]{{else}}{{applyFormat $val .Step.Format}}{{end}}</td>
```

### Gallery / new "Attachments" section

`arx_go/records.go` builds `imageRows` twice (`RecordDetail` ~line 1093,
`RecordPrint` ~line 1176) by filtering `resultRows` through `isImageRow`. Add
a parallel `fileRows` filtered through `isFileRow` in both places, and add it
to both `renderRecords`/`renderPrintRecords` data maps as `"FileRows"`:

```go
var fileRows []models.ResultRow
for _, row := range resultRows {
    if row.Level == 0 && isFileRow(row) {
        fileRows = append(fileRows, row)
    }
}
```
...
```go
h.renderRecords(w, r, "records_show.html", map[string]any{
    ...
    "ImageRows": imageRows,
    "FileRows":  fileRows,
    ...
})
```
(same for the `RecordPrint` map, and the `renderPrintRecords` call.)

`records_show.html` — add, right after the existing `{{if .ImageRows}}`
Screenshots block (after line 300):

```html
{{if .FileRows}}
<h5 class="mt-4 mb-3"><i class="bi bi-paperclip"></i> Attachments</h5>
<ul class="list-group mb-4">
  {{range .FileRows}}
  {{$fileURL := imageURL $.Form.PartNumber .EffectiveValue}}
  <li class="list-group-item d-flex justify-content-between align-items-center">
    <a href="{{$fileURL}}" target="_blank"><i class="bi bi-file-pdf text-danger"></i> {{.EffectiveValue}}</a>
    <span class="text-muted small">{{.EffectiveParameter}}</span>
  </li>
  {{end}}
</ul>
{{end}}
```

`record_print.html` — add, right after the existing `{{if .ImageRows}}`
Screenshots block (after line 119):

```html
{{if .FileRows}}
<div style="margin-top:16px;">
  <div style="font-weight:700; font-size:10pt; border-bottom:2px solid #000; padding-bottom:4px; margin-bottom:10px;">Attachments</div>
  <ul style="list-style:none; padding:0; margin:0;">
    {{range .FileRows}}
    {{$fileURL := imageURL $.Form.PartNumber .EffectiveValue}}
    <li style="padding:3px 0; font-size:9pt;">{{.EffectiveParameter}}: {{.EffectiveValue}}</li>
    {{end}}
  </ul>
</div>
{{end}}
```

(Print view keeps it plain text/no live links, matching the existing
print-page convention of static values rather than clickable UI — the PDF
isn't reachable from a printed page anyway. `$fileURL` is computed but
unused in this minimal print rendering; drop the `{{$fileURL := ...}}` line
from the print-page version since nothing uses it there — only
`records_show.html`'s version needs the link.)

## 5. `arx_go/templates/records/record_edit.html` — attach-step edit cell

Replace the attach-step cell block (lines ~229–247) to add a file-browse
input alongside the existing "Grab from clipboard" button, and to split the
preview into an image-preview variant and a PDF-link variant that JS toggles:

```html
{{if eq .Step.PFType "attach"}}
<input type="hidden" name="result_{{$sid}}" value="{{$cur}}"
       data-pf-type="{{.Step.PFType}}" data-pf-target="pf-{{$sid}}">
<div class="d-flex align-items-center gap-2 flex-wrap" data-attach-cell
     data-record-id="{{$.Record.ID}}" data-test-id="{{$sid}}"
     data-result-input="result_{{$sid}}" data-part-number="{{sanitizedPartNumber $.Form.PartNumber}}">
  <input type="file" class="form-control form-control-sm" style="max-width:220px;" data-attach-file-input
         accept="image/png,image/jpeg,image/webp,image/gif,application/pdf"
         {{if not $.ImageRootConfigured}}disabled title="IMAGE_ROOT is not configured"{{end}}>
  <button type="button" class="btn btn-sm btn-secondary" data-attach-btn
          {{if not $.ImageRootConfigured}}disabled title="IMAGE_ROOT is not configured"{{end}}>
    <i class="bi bi-clipboard-plus"></i> Grab from clipboard
  </button>
  <span class="small text-danger d-none" data-attach-error></span>
  <span class="img-hover-wrap{{if or (not $cur) (isPDF $cur)}} d-none{{end}}" data-attach-preview
        data-src="{{if $cur}}{{imageURL $.Form.PartNumber $cur}}{{end}}">
    <a href="{{if $cur}}{{imageURL $.Form.PartNumber $cur}}{{end}}" target="_blank" data-attach-link>
      <i class="bi bi-image"></i> View
    </a>
    <span class="img-hover-preview"><img alt="preview"></span>
  </span>
  <span class="{{if or (not $cur) (not (isPDF $cur))}}d-none{{end}}" data-attach-pdf-link>
    <a href="{{if $cur}}{{imageURL $.Form.PartNumber $cur}}{{end}}" target="_blank" data-attach-pdf-href>
      <i class="bi bi-file-pdf text-danger"></i> View PDF
    </a>
  </span>
</div>
{{else if $opts}}
...
```

`isPDF` must be callable on `$cur` (bare filename, same pattern already used
elsewhere in this file's funcs) — already registered in
`recordsTemplateFuncs`.

## 6. `arx_go/static/records/paste_result_image.js` — JS changes

1. **`applyPastedResultImage`** — currently always shows the image-preview
   span. Change it to branch on extension and toggle both spans:

```js
function applyPastedResultImage(cell, filename) {
    var inputName = cell.dataset.resultInput;
    var input = document.querySelector('#edit-form [name="' + inputName + '"]');
    if (input) {
        input.value = filename;
        input.dispatchEvent(new Event('input'));
        input.dispatchEvent(new Event('change'));
    }

    var fileURL = '/images/' + cell.dataset.partNumber + '/' + filename;
    var isPdf = /\.pdf$/i.test(filename);

    var preview = cell.querySelector('[data-attach-preview]');
    var pdfLink = cell.querySelector('[data-attach-pdf-link]');
    if (preview) {
        if (isPdf) {
            preview.classList.add('d-none');
        } else {
            preview.dataset.src = fileURL;
            preview.classList.remove('d-none');
            var link = preview.querySelector('[data-attach-link]');
            if (link) { link.setAttribute('href', fileURL); }
            var img = preview.querySelector('.img-hover-preview img');
            if (img) { img.removeAttribute('src'); }
            wireImgHoverPreview(preview);
        }
    }
    if (pdfLink) {
        if (isPdf) {
            pdfLink.querySelector('[data-attach-pdf-href]').setAttribute('href', fileURL);
            pdfLink.classList.remove('d-none');
        } else {
            pdfLink.classList.add('d-none');
        }
    }
}
```

2. **New `uploadBrowsedResultFile`** (multipart POST, mirrors
   `uploadPastedResultImage`'s success/error handling but can't reuse
   `postImageDataURL`, which is base64/JSON-specific):

```js
function uploadBrowsedResultFile(cell, file) {
    var errEl = cell.querySelector('[data-attach-error]');
    if (errEl) { errEl.classList.add('d-none'); errEl.textContent = ''; }
    var recordId = cell.dataset.recordId;
    var testId = cell.dataset.testId;
    var url = '/api/record/' + recordId + '/step/' + testId + '/upload-result-file?csrf_token=' + encodeURIComponent(PASTE_RESULT_CSRF_TOKEN);
    var fd = new FormData();
    fd.append('file', file);
    fetch(url, { method: 'POST', body: fd })
        .then(function (r) {
            return r.json()
                .catch(function () { return {}; })
                .then(function (data) { return { ok: r.ok, data: data }; });
        })
        .then(function (result) {
            if (!result.ok) { throw new Error(result.data.error || 'Upload failed'); }
            applyPastedResultImage(cell, result.data.filename);
        })
        .catch(function (err) {
            if (errEl) { errEl.textContent = err.message; errEl.classList.remove('d-none'); }
        });
}
```

3. **Wire the new file input**, alongside the existing `DOMContentLoaded`
   block that wires `[data-attach-btn]`:

```js
document.addEventListener('DOMContentLoaded', function () {
    document.querySelectorAll('[data-attach-file-input]').forEach(function (input) {
        input.addEventListener('change', function () {
            var cell = input.closest('[data-attach-cell]');
            var file = input.files && input.files[0];
            if (cell && file) { uploadBrowsedResultFile(cell, file); }
            input.value = ''; // allow re-selecting the same file after an error
        });
    });
});
```
   (This can be a second `document.addEventListener('DOMContentLoaded', ...)`
   call, or folded into the existing one at the bottom of the file — match
   whichever reads better once both exist; functionally identical.)

4. **Global Ctrl+V listener** — extend the type filter so a PDF *file* pasted
   via the synchronous paste event (e.g. a file copied in a file manager,
   then Ctrl+V'd into the browser — `event.clipboardData.items` reports its
   real MIME type for this case, unlike `navigator.clipboard.read()`, which
   the "Grab from clipboard" button uses and which browsers sanction only for
   image types) is also accepted:

```js
document.addEventListener('paste', function (e) {
    var active = document.activeElement;
    var cell = active && active.closest ? active.closest('[data-attach-cell]') : null;
    if (!cell) return;
    var items = (e.clipboardData || {}).items || [];
    for (var i = 0; i < items.length; i++) {
        if (items[i].type.indexOf('image/') === 0 || items[i].type === 'application/pdf') {
            uploadPastedResultImage(cell, items[i].getAsFile());
            e.preventDefault();
            return;
        }
    }
});
```

   Do **not** change `readImageFromClipboard` in `static/clipboard_paste.js`
   — it's shared with the part-Photo clipboard-paste button
   (`static/parts/paste_attachment.js`), which should stay image-only. The
   "Grab from clipboard" *button* on an attach step therefore stays
   image-only; only the raw Ctrl+V path gains PDF support, plus the new
   file-browse input, which is the primary way users will attach a real PDF
   certificate. This is a deliberate, documented limitation — see "Manual
   testing" below; it is not a loose end.

No changes are needed to `static/clipboard_paste.js` itself.

## Out of scope / explicitly not done

- No schema/migration changes (per the issue).
- No change to `SaveResults`/the attach pass-fail logic in
  `internal/models`/`arx_go/models` — presence/absence of a value already
  drives PASS/MISSING regardless of file type; untouched.
- No change to `pasteImageExts`, `decodePastedImage`'s existing two part-level
  callers, or `SettingsCompanyLogoSave`.
- `readImageFromClipboard`/`static/clipboard_paste.js` unchanged (shared with
  part-Photo paste, stays image-only).

## Test plan

### Coverage audit (existing tests that already touch this code)

- `arx_go/api_test.go: TestDecodePastedImage` — covers `decodePastedImage`'s
  four-type allow-list and error paths. After the refactor into
  `decodeDataURL`/`decodePastedImage`, this test exercises the thin wrapper
  and must keep passing unmodified (same four accepted types, same error
  strings).
- `arx_go/integration_test.go: TestIntegration_PasteResultImageGuards` —
  covers `APIRecordPasteResultImage`'s not-found (404) and locked (409)
  guards, via the JSON endpoint. Guard logic is untouched; must keep passing
  unmodified.
- `arx_go/integration_test.go: TestIntegration_PasteResultImageWrite` —
  covers the real write path (correct filename pattern, correct bytes on
  disk) for a PNG via the JSON endpoint. Untouched code path; must keep
  passing unmodified.
- `arx_go/integration_test.go: TestIntegration_AttachStepPassFail` — covers
  `SaveResults`' PASS/MISSING evaluation for an attach step's result value
  (string presence only, no file I/O). Untouched; must keep passing
  unmodified.
- `arx_go/trfiles_test.go: TestServeImage_DirectoryNotListed`,
  `TestServeImage_SymlinkEscape` — cover `ServeImage`'s path-safety guards.
  The Content-Type fix (section 3) must not change any of these status-code
  assertions; both must keep passing unmodified.
- `arx_go/helpers_tr_test.go` (`imageResult` test table) — covers the
  filename-pattern regex itself, which is not changed by this plan (only how
  templates/`isImageRow` branch on its result, given a PDF extension, changes).
  Must keep passing unmodified.
- `arx_go/models/trmodels_test.go` (attach-type PASS/MISSING cases) — covers
  `CalcPF` for `pf_type="attach"`, unaffected by this change (value presence,
  not file type). Must keep passing unmodified.

### Characterization tests (new — pin down current behavior nothing covers today, must pass unmodified on current code)

- `TestServeImage_ContentType_DefaultsToImagePNG` (new, in
  `arx_go/trfiles_test.go`): write a plain file (any extension, e.g.
  `foo.jpg`) under a temp `ImageRoot`, GET it via `ServeImage`, assert
  `Content-Type: image/png` (today's blanket behavior for every extension).
  This pins down the *pre-fix* behavior so the PDF-specific branch added in
  section 3 is proven to be additive, not a silent regression for the
  existing image types.
- `TestIsImageRow_AttachStepAnyValueIsImage` (new, in
  `arx_go/render_records_test.go` — create this file if it doesn't exist;
  check first) — construct a `models.ResultRow{Step: &models.TestStep{PFType:
  "attach"}, ...}` with a non-empty, non-PDF value (e.g. `"shot.png"`) and
  assert `isImageRow(row)` is `true`. Pins down today's behavior (every
  non-empty attach value is treated as an image) before `isFileRow` is
  introduced and `isImageRow` gets its PDF carve-out.

### Red tests (acceptance criteria — fail today, must pass after the change)

1. `TestAttachStep_FileBrowseInputPresent` — **manual-only**, see below (this
   AC item is a page-rendering/UI-control-presence check; see note).
   Actually automatable minimally as `TestRecordEdit_AttachCellHasFileInput`
   (new, in a `records_test.go`/`render_tr_test.go`-style file — check for an
   existing template-rendering test harness for `record_edit.html` first and
   add to it, or create one if none exists): render `record_edit.html` for a
   form with one `pf_type="attach"` step and assert the output HTML contains
   `data-attach-file-input`. Fails today (no such attribute exists on the
   current template) and passes once section 5's template change lands.
2. `TestIntegration_UploadResultFile_PDF` (new, in `arx_go/integration_test.go`,
   alongside `TestIntegration_PasteResultImageWrite`): seed an unlocked
   record the same way `TestIntegration_PasteResultImageWrite` does, POST a
   small valid PDF byte sequence (e.g. `%PDF-1.4\n...` minimal bytes — a
   literal test fixture is fine, doesn't need to be a real renderable PDF,
   just sniffable as `application/pdf`) as multipart form data to
   `/api/record/{id}/step/{tid}/upload-result-file`, assert `200`,
   `ok:true`, a `.pdf`-suffixed filename matching the same
   `SN..._rID..._tID..._<timestamp>.pdf` shape `TestIntegration_
   PasteResultImageWrite` checks for PNG, and that the file on disk has the
   posted bytes. Fails today (route doesn't exist → 404) and passes once
   sections 1–2 land.
3. `TestIntegration_UploadResultFile_PNGViaBrowse` (new, same file): same
   flow as #2 but posting a PNG, confirming the four existing image types
   still work through the new browse endpoint (not just the old paste
   endpoint). Fails today (route doesn't exist) and passes once sections 1–2
   land.
4. `TestIntegration_UploadResultFile_RejectsUnsupportedType` (new, same
   file): POST plain-text bytes (sniffs as `text/plain`) to the new
   endpoint, assert `400` with an "Unsupported file type" error message.
   Fails today (route doesn't exist → 404, not the intended 400) and passes
   once section 1 lands.
5. `TestDecodeDataURL_AttachResultExtsAcceptsPDF` (new, in
   `arx_go/api_test.go`, alongside `TestDecodePastedImage`): call
   `decodeDataURL(dataURL, attachResultExts)` with a `data:application/pdf;
   base64,...` payload, assert no error and `ext == ".pdf"`; and call it
   again with `decodePastedImage` (i.e. the original `pasteImageExts` map) on
   the same payload and assert it still errors
   `"Unsupported image type: application/pdf"` — proving the part-Photo/
   company-logo paths are untouched. Fails today (`attachResultExts`/
   `decodeDataURL` don't exist) and passes once the refactor in the "MIME/
   extension allow-list" section lands.
6. `TestIsFileRow_PDFAttachStepIsFileNotImage` (new, in
   `arx_go/render_records_test.go`): same construction as the
   characterization test above but with a `.pdf`-suffixed value; assert
   `isImageRow(row) == false` and `isFileRow(row) == true`. Fails today
   (`isFileRow` doesn't exist, and `isImageRow` returns `true` for every
   attach value) and passes once section 4 lands.
7. `TestServeImage_PDFContentType` (new, in `arx_go/trfiles_test.go`): write
   a `.pdf`-suffixed file under a temp `ImageRoot`, GET it via `ServeImage`,
   assert `Content-Type: application/pdf`. Fails today (hardcoded
   `image/png`) and passes once section 3 lands.

### Manual-only (cannot reasonably be automated)

- Clicking the new file-browse `<input type="file">` on an attach step,
  picking a PDF from the OS file dialog, confirming it uploads and the cell
  shows the "View PDF" link (not a broken thumbnail) without a page reload.
- Same, for each of the four existing image types via the new browse input
  (not just clipboard paste), confirming the thumbnail/hover-preview still
  renders correctly.
- Pasting (Ctrl+V) a PDF file copied from a file manager (e.g. Windows
  Explorer) into a focused attach cell, confirming the browser actually
  surfaces it as an `application/pdf` clipboard item on the tester's
  machine/browser — this is inherently environment-dependent (not all
  OS/browser combinations expose a copied file's real MIME type via
  `clipboardData.items`), so if it doesn't work in practice, file-browse is
  the documented fallback, not a bug to chase further.
- Visual check of the record-view (`/records/{id}`) and print
  (`/records/{id}/print`) pages after saving a PDF attach-step result:
  confirm the inline result cell shows a plain PDF link (not an `<img>`), and
  the new "Attachments" section lists it correctly alongside (not mixed
  into) the "Screenshots" image gallery.
- Confirm the "Grab from clipboard" button is unaffected (still image-only,
  still disabled when `IMAGE_ROOT` isn't configured) — regression check on
  existing behavior.

### Integration test note

`TestIntegration_UploadResultFile_PDF`,
`TestIntegration_UploadResultFile_PNGViaBrowse`, and
`TestIntegration_UploadResultFile_RejectsUnsupportedType` are
integration-covered (`arx_go/integration_test.go`, `-tags integration`,
live-DB). The manager session implementing this plan should run them (and
the full existing integration suite, to catch any regression) via the
throwaway local container per `CLAUDE.md`'s Integration tests section — this
plan does not run them itself.

## Open questions

None — the one design decision the issue called out (JSON-cap-raise vs.
second multipart endpoint) is resolved above (second endpoint, no cap
change), and the PDF-via-clipboard-paste ambiguity is resolved by splitting
"Grab from clipboard" (stays image-only, unchanged) from the raw Ctrl+V
listener (gains PDF support) based on which browser API each one uses.
