# Issue #614 — Deduplicate the PM/TR shared layout shell

## Context

`arx_go` was formed by merging two former apps — Parts Master (`pm`) and Test Records
(`tr`) — into one binary. That merge is fully complete (one binary, port 4568, Go
package, config, DB pool, session cookie, and shared `static/app.css`; the header + tab
nav were explicitly unified in 0.5.16). The `templates/pm/` vs `templates/tr/` split is
the last visible seam.

Issue #614 asks whether that split is still necessary. **Answer: the split as *page
organization* is harmless and worth keeping, but the split has produced one real latent
bug — two near-duplicate `layout.html` shells.** `templates/pm/layout.html` and
`templates/tr/layout.html` are ~98% byte-identical: same header, same nav bar (the TR
layout even renders the Parts Master nav and PM icons), same banners, same scripts. They
differ only in `<title>`, favicon path, and one extra `tr/app.js`. Any nav/header change
today must be made in both files or they silently drift.

**Scope (chosen):** dedup the shared shell only. Extract a single unified layout both
render helpers parse; keep the `pm/`/`tr/` page-template folders and the two domain
func maps untouched. This is surgical and low-risk — the layout uses only builtin
template funcs (`eq`, `or`, `range`, `if`), so it parses cleanly under either func map.
The 3 conflicting date funcs and the 26 domain-specific funcs are **out of scope**.

## Changes

### 1. New file: `arx_go/templates/shared/layout.html`
One unified layout (based on the current `pm/layout.html`), with the three per-side
differences replaced by injected data fields:
- `<title>{{.Title}}</title>`
- favicon in both spots (`<link rel="icon">` and the `.app-header-icon` img) → `{{.Favicon}}`
- extra per-side scripts after the shared `pm/app.js`:
  ```
  {{range .ExtraScripts}}<script src="{{.}}?v={{$.AppVersion}}"></script>{{end}}
  ```
  (`$.AppVersion` because `.` rebinds inside `range`)

Everything else (header, nav, whats-new banner, schema-mismatch alert, bootstrap +
`table-sort.js` + `pm/app.js`) is copied verbatim from the current shared markup. Pick
one encoding for the whats-new banner text (literal `'`/`—`); it renders identically.
`//go:embed templates` (`arx_go/embed.go:5`) picks up the new `templates/shared/` subdir
automatically — no embed change needed.

### 2. `arx_go/handlers.go` — `render()` (~L190-203)
- Change the ParseFS layout path from `templates/pm/layout.html` to
  `templates/shared/layout.html` (keep `templates/pm/partials.html` and the page).
- In the `map[string]any` injection block (alongside the existing `AppVersion` etc.), add:
  `m["Title"] = "Arx Parts Master"`, `m["Favicon"] = "/static/pm/favicon.png"`.
  (No `ExtraScripts` for PM — the `range` over a nil slice emits nothing.)

### 3. `arx_go/render_tr.go` — `renderTR()` (~L26-29)
- Change the ParseFS layout path from `templates/tr/layout.html` to
  `templates/shared/layout.html` (keep the page; TR still has no partials).
- In its injection block add:
  `m["Title"] = "Arx: Test Records"`, `m["Favicon"] = "/static/tr/favicon.png"`,
  `m["ExtraScripts"] = []string{"/static/tr/app.js"}`.

`renderPrint` / `renderPrintTR` are standalone (no layout) — **no change**.

### 4. Delete `arx_go/templates/pm/layout.html` and `arx_go/templates/tr/layout.html`

### 5. `arx_go/templates_parse_test.go`
- `TestPMTemplatesParse` (L20-26): parse `templates/shared/layout.html` +
  `templates/pm/partials.html` + page; the `base == "layout.html"` skip for the pm glob
  is now dead (no pm/layout.html) but harmless — leave the `partials.html` skip.
- `TestTRTemplatesParse` (L42, L52): parse `templates/shared/layout.html` + page; the
  `base == "layout.html"` tr skip is likewise now moot. Update both string literals.

## Why safe / notes
- The injection is guarded by `data.(map[string]any)`, exactly like the existing
  `AppVersion` injection — every layout-rendered page already passes a map (or the version
  banner would already be blank), so the new fields land on all of them.
- Per-side `Title`/`Favicon` are preserved rather than collapsed to a single value, to
  keep this a pure refactor with no visible change. A follow-up could collapse to one
  app-wide favicon/title if desired — noted, not done here.
- CHANGELOG: add one line under a new version entry
  (`- Deduplicate the shared PM/TR page layout into one template ([#614](https://github.com/Jolls/arx-legacy/issues/614))`).
  Not user-facing → no RELEASE_NOTES entry.

## Verification
1. `cd arx_go && go build ./... && go vet ./... && go test ./...` — the parse tests
   (`TestPMTemplatesParse`, `TestTRTemplatesParse`, print variants) re-parse **every**
   page template against the new shared layout, so a broken reference fails the build.
2. Manual (user runs `go run .` from `arx_go/`):
   - Load `/` (Parts) — header, tab bar, title "Arx Parts Master", PM favicon all render.
   - Load `/records` (Test Records) — same shell, title "Arx: Test Records", TR favicon,
     and TR-specific behavior still works (confirm `tr/app.js` loaded, e.g. via a
     records-page interaction that depends on it).
   - Spot-check one print view (e.g. a PO print or `record_print`) still renders — confirms
     the standalone print path was untouched.
