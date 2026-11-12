# Plan: #453 — Attach-step result isn't clickable in the main results table

## Open questions
None. Verified both templates are parsed with the same `recordsTemplateFuncs()` FuncMap
(`arx_go/templates.go`), and `.` is the full `models.ResultRow` at the point the main-cell
markup runs in both templates.

## Verified facts (code as currently read)

- `arx_go/templates.go` `parseTemplates`: both `records:record_print.html`-equivalent
  (actually keyed `recordsprint:record_print.html`) and `records:records_show.html` are parsed
  with the same `records := recordsTemplateFuncs()` FuncMap. `coreTemplateFuncs()` is unrelated
  to these two templates — no registration needed there.
- `arx_go/render_records.go` `recordsTemplateFuncs()` currently registers `"imageResult": imageResult`
  but not `isImageRow`/`isFileRow`.
- `arx_go/templates/records/records_show.html` (inside `{{range .Rows}}` or similar, at the row
  loop establishing `.` = a `models.ResultRow`): `{{$val := .EffectiveValue}}` then the main
  cell gate is `{{if imageResult $val}} ... {{else if isLocalFile $val}} ... {{else}}
  {{applyFormat $val .Step.Format}} {{end}}`. `.` is confirmed to be the row (same block calls
  `.EffectiveParameter`, `.Step.ID`, `.Step.Format`).
- `arx_go/templates/records/record_print.html`: same shape, single-line cell:
  `{{if imageResult $val}}{{if isPDF $val}}[see attachments]{{else}}[see gallery]{{end}}{{else if isLocalFile $val}}[file: {{fileBaseName $val}}]{{else}}{{applyFormat $val .Step.Format}}{{end}}`.
  `.` here is also the row (`{{range .Rows}}` → `{{if gt .Level 0}}` branch uses `.EffectiveParameter`,
  `.Step.ID`/`.Level` on the same `.`).
- `arx_go/render_records.go` `attachRowKind(row models.ResultRow) string` returns `"image"`,
  `"file"`, or `""`. `isImageRow`/`isFileRow` are thin wrappers over it. Classification logic:
  `isAttach := row.Step != nil && row.Step.PFType == "attach"`; if `!isAttach && !imageResult(val)`
  → `""` (not an attachment); otherwise PDF → `"file"`, else → `"image"`. This already satisfies
  acceptance criterion 2 (legacy non-attach row matching `imageResultRe` still classifies, via the
  `imageResult(val)` half of the `||`) — no Go-side change needed, only the template-side gate.

## Code changes

1. **`arx_go/render_records.go`**, `recordsTemplateFuncs()`: add two entries next to the existing
   `"imageResult": imageResult` line:
   ```go
   "isImageRow": isImageRow,
   "isFileRow":  isFileRow,
   ```
   (`imageResult` stays registered — `record_print.html`'s inner branch still needs `isPDF $val`
   and other templates/paths may still reference `imageResult` elsewhere; out of scope to remove.)

2. **`arx_go/templates/records/records_show.html`**, main results table cell: change only the
   outer gating condition, keep every inner branch byte-for-byte:
   - `{{if imageResult $val}}` → `{{if isImageRow .}}` for the image/thumbnail inner branch
     (currently the `{{if isPDF $val}}...{{else}}...{{end}}` block) — wait: that inner block
     itself re-checks `isPDF $val` to decide PDF-link vs. image-thumbnail markup, so the outer
     condition must cover BOTH kinds (image or file), not just `isImageRow`. Use:
     `{{if or (isImageRow .) (isFileRow .)}}` in place of `{{if imageResult $val}}`.
     The existing inner `{{if isPDF $val}}` / `{{else}}` split still works unchanged: a `"file"`
     row's value is a PDF so `isPDF $val` is true there; an `"image"` row's value is never a PDF
     so it falls to the thumbnail branch. No other line in this block changes.
   - Leave `{{else if isLocalFile $val}}` and the final `{{else}} {{applyFormat $val .Step.Format}}
     {{end}}` exactly as-is.

3. **`arx_go/templates/records/record_print.html`**, main cell, same substitution:
   `{{if imageResult $val}}` → `{{if or (isImageRow .) (isFileRow .)}}`. Inner `{{if isPDF $val}}
   [see attachments]{{else}}[see gallery]{{end}}` stays unchanged — same reasoning as above (a
   `"file"` kind row is always a PDF here, so the existing `isPDF $val` split still routes
   correctly without needing to call `isFileRow`/`isImageRow` again inside).

4. No change to `imageResult`, `imageResultRe`, `attachRowKind`, `isImageRow`, `isFileRow`, or
   `buildResultImageName`/`sanitizeFileNamePart` themselves — purely wiring the existing
   row-aware classifiers into the two templates' main-cell gate.

5. Double check after editing: grep both templates for any other `imageResult $val` occurrence
   (e.g. a mobile/compact view, a JS-rendered partial, or a second main-table copy) in case the
   loop appears more than once per file — only the two locations identified above are known to
   exist as of this read, but confirm no third copy was missed before calling the change done.

## Test plan

### 1. Coverage audit
- `arx_go/helpers_tr_test.go`: `TestIsImageRow_AttachStepAnyValueIsImage`,
  `TestIsFileRow_PDFAttachStepIsFileNotImage` — cover `isImageRow`/`isFileRow`/`attachRowKind`
  Go logic directly, already passing, unaffected by this change (classification logic doesn't
  change). No coverage of the acceptance-criterion-2 "legacy row matches `imageResultRe`"
  path through `attachRowKind` specifically — only through `TestImageResult` (same file) which
  tests the bare `imageResult(val)` function, not `attachRowKind`/`isImageRow`.
- `arx_go/render_records_test.go`: none found testing `attachRowKind`/`isImageRow`/`isFileRow`
  (the two tests above live in `helpers_tr_test.go`, not here) — confirm naming before writing
  new tests, to place them consistently (recommend adding to `helpers_tr_test.go`, matching where
  the existing two already live, since that file groups `models.ResultRow`-level unit tests).
- `arx_go/templates_parse_test.go`: parses `records:*.html` and `recordsprint:record_print.html`
  for syntax validity (`TestXxxParse`-style) but does not execute them against sample row data or
  assert on rendered cell markup — adding `isImageRow`/`isFileRow` to the FuncMap is covered by
  this parse test only insofar as the new funcs must exist and be callable; it will catch a typo
  in the funcmap registration but not a wrong gating condition.
- No existing test executes `records_show.html` or `record_print.html` end-to-end with a
  `models.ResultRow` set and asserts on the main-cell HTML output.

### 2. Characterization tests (pin current behavior, not the bug)
Add to `arx_go/helpers_tr_test.go` (Go-level, no template execution needed — matches the existing
two tests' convention and is simpler than rendering/parsing HTML):
- A non-attach-step row (`Step.PFType != "attach"`, or `Step == nil`) whose value matches
  `imageResultRe` (e.g. `"SN-123_rID-4_tID-5"`) still returns `"image"` from `attachRowKind`
  (and `true` from `isImageRow`) — pins acceptance criterion 2's Go-side precondition. This is
  NOT a bug lock-in; it documents behavior that must keep working.

### 3. Red tests (fail today, pass after the fix)
These must exercise the actual templates, since the bug is template-side (the Go classifiers
already return the right answer today — `isImageRow`/`isFileRow` already exist and work; the
templates just don't call them yet). Recommend a small template-execution test in
`arx_go/render_records_test.go` (new test, following the `template.New("").Funcs(recordsTemplateFuncs()).ParseFS(...)`
pattern already used in `templates_parse_test.go`, then `tmpl.ExecuteTemplate(&buf, ..., data)`
and asserting on substrings of `buf.String()`):
- `TestRecordsShowMainCell_AttachStepNonMatchingSerial`: build a `models.ResultRow` with
  `Step.PFType = "attach"`, `Result.Result` (or whatever field backs `EffectiveValue`) set to a
  filename built from a serial containing a non-`[A-Za-z0-9-]` character (e.g.
  `"SN my_serial.1_RID-7_TID-9.png"` or simpler: any attach-step value that does NOT match
  `imageResultRe`, since the bug reproduces for ALL attach-step filenames whose serial breaks the
  regex, not just one specific character — pick one representative case, e.g. a space). Render
  just the row-loop portion (or the whole template if simpler) and assert the output contains the
  thumbnail/`img-hover-wrap` markup (or PDF-link markup, for a PDF case) — **fails today** because
  `imageResult $val` is false (doesn't match the regex) so the template falls through to the
  `{{else}} {{applyFormat ...}}` branch, rendering plain text instead.
- `TestRecordPrintMainCell_AttachStepNonMatchingSerial`: same construction, rendered through
  `recordsprint:record_print.html`, asserting the cell contains `[see attachments]` (PDF case) or
  `[see gallery]` (image case) instead of the raw filename — fails today for the same reason.
- Before writing: inspect `arx_go/records.go` / `models.ResultRow` to confirm the exact field(s)
  that make `EffectiveValue` return the attach filename in a test-constructed row (likely
  `Result.Result`, per the existing `helpers_tr_test.go` tests) and whatever minimal `Step`/`Form`
  fields `imageURL`/`isPDF`/`fileBaseName` need to not panic when executed — check whether these
  templates need more context fields (e.g. `.Form.PartNumber`, `$.DebugMode`) supplied in the test
  harness's root data, matching whatever `TestYieldTemplateRenders` (templates_parse_test.go) uses
  as its minimal-data convention for records templates.

### 4. Manual-only
- Open a real record (`/records/{id}`) whose serial contains a space/underscore/dot, with an
  attach-step image and a separate attach-step PDF, and visually confirm: main table cell shows a
  clickable thumbnail with hover preview for the image row, and a clickable PDF link/icon for the
  PDF row — identical to what the Screenshots/Attachments sections below already show for the
  same rows.
- Open the print view (`/records/{id}/print` or equivalent) for the same record and confirm the
  main cell shows `[see gallery]` / `[see attachments]` instead of raw filename text.
- This step exists because template logic tests (asserting on HTML substrings) won't catch a
  genuine markup/CSS rendering mistake (e.g. a broken `img-hover-preview` CSS selector) — only a
  real browser render does.

### Seed/fixture data
No new DB seed or fixture data needed — this is pure template/classification logic over
`models.ResultRow` values constructible directly in Go tests; confirmed no integration-test
(`arx_go/integration_test.go`) coverage is required for this fix.
