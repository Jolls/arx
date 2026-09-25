# #197 Templates: parse once at startup (parse-once only)

Issue: https://github.com/Jolls/arx/issues/197. Area: refactor. Typed page-data structs are OUT of scope.
Branch: `feature/197-templates-parse-once` (create from main before first commit).

## Findings (current code)
- Per-request parsing sites (5), all `template.New("").Funcs(...).ParseFS(h.tmplFS, ...)`:
  - `arx_go/handlers.go` `render` (~L619): funcs `coreTemplateFuncs()`, files `shared/layout.html`, `shared/partials.html`, `<page>`; executes `"layout"`.
  - `arx_go/handlers.go` `renderPrint` (~L576): `coreTemplateFuncs()`, single file `<page>`; executes `path.Base(page)`.
  - `arx_go/render_records.go` `renderRecords` (~L31): `recordsTemplateFuncs()`, files `shared/layout.html`, `records/<page>` (NO partials.html); executes `"layout"`.
  - `arx_go/render_records.go` `renderPrintRecords` (~L46): `recordsTemplateFuncs()`, single file `records/<page>`; executes `<page>`.
  - `arx_go/auth.go` `renderLogin` (~L545): NO Funcs, single file `shared/login.html`; executes `login.html`.
- No other template.New/ParseFS/Parse of templates exists in non-test code. `ExecuteTemplate` only in those 5 functions.
- `coreTemplateFuncs()` and `recordsTemplateFuncs()` are stateless (no closures over Handler/request); safe to build once.
- `render`/`renderRecords` mutate only `data`, never the template; a parsed `*template.Template` is safe for concurrent `Execute`.
- All callers pass string-literal page names (verified by grep); no dynamic page names.
- Print/standalone callers: `parts/part_bom_paste_preview.html` (renderPrint, parts.go:1304), `pos/po_print.html` (renderPrint, pos.go:1086), `record_print.html` (renderPrintRecords, records.go:1502). Note `part_bom_paste_preview.html` does not end in `_print.html`, so the suffix cannot classify it.
- `templates/records/layout.html` does NOT exist (existing test skips that name; leave it).
- Embedded FS (`embed.go`, `//go:embed templates`): `go run .` and the exe both read the compiled-in FS, so templates already only change on rebuild. Parse-once causes no dev-mode behaviour change.
- `New(...)` is called with nil `tmplFS` in `auth_test.go:40`, `middleware_test.go:23,46`; and `&Handler{}` literals in `handlers_test.go:61`, `trfiles_test.go:26`. Tests that call render paths use `templatesFS` (`files_test.go:21`, `categories_test.go:17`, `settings_save_test.go:26`, integration tests).
- Existing `arx_go/templates_parse_test.go` already parse-tests every page via `fs.Glob` (`TestCoreTemplatesParse`, `TestRecordsTemplatesParse`, `TestCorePrintTemplatesParse`, `TestSharedStandaloneTemplatesParse`) using duplicated inline ParseFS calls.

## Design
New file `arx_go/templates.go` (package main):
- Type `templateSet` = `struct{ core, records, standalone map[string]*template.Template }`? NO: use one flat map keyed by the exact page string each render function already receives, prefixed by kind, so lookups are trivial:
  - key `"layout:"+page` for `render` (page like `parts/foo.html`)
  - key `"records:"+page` for `renderRecords` (page like `yield.html`)
  - key `"print:"+page` for `renderPrint` (page like `pos/po_print.html`)
  - key `"recordsprint:"+page` for `renderPrintRecords` (page like `record_print.html`)
  - key `"login"` for `renderLogin`
- `func parseTemplates(fsys fs.FS) (map[string]*template.Template, error)`:
  - core funcs `coreTemplateFuncs()`, records funcs `recordsTemplateFuncs()` each called once.
  - Walk `templates/{parts,suppliers,pos,contacts,settings,reports}/*.html` and `templates/shared/{error,not_found,local_dir}.html` with `fs.Glob`; for each, parse `shared/layout.html`, `shared/partials.html`, page -> key `layout:<path relative to templates/>`.
  - Explicit list `corePrintPages = []string{"parts/part_bom_paste_preview.html", "pos/po_print.html"}`: each ALSO parsed standalone (single file) -> key `print:<page>`. (These pages are also parsed with layout by the glob above, matching current test behaviour; harmless.)
  - `templates/records/*.html`: parse `shared/layout.html` + page with records funcs -> key `records:<base>`; additionally `record_print.html` standalone with records funcs -> key `recordsprint:record_print.html`. (Records layout+page parse must skip nothing; `record_print.html` is also layout-parsed, mirroring current test.)
  - `shared/login.html` no funcs -> key `login`.
  - Any parse error returns `fmt.Errorf("parse %s: %w", key, err)`.
- Handler gets field `tmpls map[string]*template.Template`.
- `func (h *Handler) loadTemplates() error { m, err := parseTemplates(h.tmplFS); ...; h.tmpls = m }`.

## File changes
1. `arx_go/templates.go` (new): as above.
2. `arx_go/handlers.go`:
   - add `tmpls map[string]*template.Template` to `Handler` struct (next to `tmplFS`, L46).
   - `render`: replace ParseFS block with `tmpl, ok := h.tmpls["layout:"+page]`; if !ok -> `serverError(w, "template not found", fmt.Errorf("%s", page))` and return. Keep ExecuteTemplate("layout").
   - `renderPrint`: same with key `"print:"+page`; keep `path.Base(page)` execute.
   - Fix doc comments ("parses ..." -> "executes the pre-parsed ...").
3. `arx_go/render_records.go`: `renderRecords` -> key `"records:"+page`; `renderPrintRecords` -> key `"recordsprint:"+page`. Same not-found handling. Remove any imports left unused (`html/template` stays: used by `recordsTemplateFuncs`).
4. `arx_go/auth.go` `renderLogin`: use `h.tmpls["login"]`; remove `html/template` import only if now unused (check file).
5. `arx_go/main.go`: after `h = New(...)` (L48) add `if err := h.loadTemplates(); err != nil { log.Fatalf("arx: template parse error: %v", err) }` (match the file's existing fatal-error style).
6. Tests that reach render paths must call `loadTemplates`: `files_test.go` helper (L21), `categories_test.go` (L17), `settings_save_test.go` (L26), `integration_test.go` (L80, L109), `settings_save_integration_test.go` (L59) — add `if err := h.loadTemplates(); err != nil { t.Fatal(err) }` after construction (adjust to each helper's shape; only where the handler under test calls render/renderPrint/renderRecords/renderLogin). Tests constructing `New(..., nil, ...)` are untouched.
7. `arx_go/templates_parse_test.go`: add `TestParseTemplates`:
   - `parseTemplates(templatesFS)` returns no error.
   - For every `fs.Glob` page under each `coreTabDirs` dir, every `templates/records/*.html` (skip `layout.html` if present), and `shared/{error,not_found,local_dir}.html`, assert the matching `layout:`/`records:` key exists.
   - Assert keys exist for `corePrintPages`, `recordsprint:record_print.html`, `login`.
   - Assert every `render*(...)` call-site page literal resolves: a small table of the literal pages passed to renderPrint/renderPrintRecords (the 3 above) checked against map.
   Existing four parse tests stay unchanged (out of scope to dedupe).
8. `CHANGELOG.md`: new top entry `## [0.7.71] - 2026-09-24` (bump patch; current top is 0.7.70), under `### Changed`:
   `- Templates are now parsed once at startup instead of on every request; a broken template fails at startup ([#197](https://github.com/Jolls/arx/issues/197))`
   Do NOT touch `RELEASE_NOTES.md` (internal refactor, no user-facing change).

## Verify
- `go build ./... && go vet ./... && go test ./...` from repo root (per CLAUDE.md; do not run the app).
- Manual (user): run `go run .` in `arx_go/`, visit a parts page, a PO print view, a record print view, /login, a 404 URL; all render as before.
- No integration-tagged DB run needed beyond compiling with `go vet -tags integration ./arx_go/...` (edited integration test files).

## Resolved decisions
1. Separate `h.loadTemplates()` called from main.go after `New` (fatal on error); `New` signature unchanged.
2. Explicit `corePrintPages` list (`parts/part_bom_paste_preview.html`, `pos/po_print.html`).
3. Design section: ignore the "NO: use one flat map" drafting remnant; the flat prefixed-key map is the design.
