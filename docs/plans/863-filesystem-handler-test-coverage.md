# 863 — Test coverage for local-filesystem browse/serve handlers

Pin current behavior (including drift between the three copies) with `httptest`
+ `t.TempDir()`, no DB. Do not fix any bugs found — that's a separate issue.

## Shared test setup

- `New(nil, nil, cfg, templatesFS, nil)` with `cfg.SessionSecret = "test-secret"`.
  `SessionSecret` is required because directory-listing handlers call
  `h.render`, which calls `h.csrfToken` → `session.Save`.
- File-serving handlers (`ServeLocalFile`, `ServeSupplierFile`, `SupplierFile`,
  `POFile`) never call `h.render`, so a bare `New(nil, nil, cfg, nil, nil)` is
  fine for those, but reusing one `testHandler()`-style helper with
  `templatesFS` set is simplest and matches `categories_test.go`.
- Use `t.TempDir()` for `DocControlRoot` / `SupplierFilesRoot` / `POFolderRoot`.
- Directory-listing handlers render full HTML via `h.render` — assert via
  response status/headers and body substring checks (entry names, "Up one
  level" presence, item-count text), not struct introspection.

## `arx_go/files_test.go` (new)

Covers `ServeLocalFile`, `ServeSupplierFile`, `ServeSupplierDir`, `ServeLocalDir`.

Per file-serving handler (`ServeLocalFile`, `ServeSupplierFile`):
- 503 when root unconfigured.
- 404 when path doesn't exist.
- 404 when path is a directory.
- `..` segments in the splat: `safePath` strips them outright rather than
  resolving/rejecting them (confirmed against `TestSafePath` in
  `helpers_test.go` — `ok` is always `true`, so safePath's own "Invalid
  path"/400 branch is effectively unreachable through this route). However
  `http.ServeFile` (called at the end of both handlers) has its own
  independent guard that rejects any request whose *raw* `r.URL.Path`
  contains a `..` element, regardless of where the sanitized `name` argument
  points — confirmed empirically (`go doc net/http ServeFile`). So a request
  for `../../etc/secret.txt` still comes back 400, but with body `invalid URL
  path` from the stdlib, not our own `safePath` rejection. Pin that mechanism,
  not the wrong one.
- `.pdf` → `Content-Disposition: inline`.
- non-pdf → `Content-Disposition: attachment` with filename.
- `ServeLocalFile` only: an image extension (`urlutil.IsImage`, e.g. `.png`)
  also → inline. `ServeSupplierFile` only checks `.pdf` — pin that drift with
  a case showing a `.png` there comes back `attachment`.
- `Cache-Control: no-cache` present on both.
- `ServeSupplierFile`: falls back to `DocControlRoot` when `SupplierFilesRoot`
  is empty — one case with only `DocControlRoot` set.

Per directory-listing handler (`ServeSupplierDir`, `ServeLocalDir`):
- 503 when root unconfigured.
- 404 when path doesn't exist.
- 404 when path is a file, not a directory.
- `..` segments: unlike the file-serving handlers, these don't call
  `http.ServeFile`, so the only guard is `safePath`'s own stripping — pin that
  a request for `../../etc` resolves to `root/etc` (200), not a 400.
- Sort order: seed a temp dir with mixed-case dirs and files, assert response
  body lists dirs before files, case-insensitive alphabetical within each
  group (check ordering of name substrings in the body).
- Parent-URL derivation: depth 0 (request at root) → no "Up one level" link;
  depth 1 and depth 2 → link present (can't easily assert the exact href
  without a body substring check on the known parent path segment).
- `ServeSupplierDir`: falls back to `DocControlRoot` when `SupplierFilesRoot`
  is empty.

## `arx_go/suppliers_test.go` (add to existing file)

Covers `renderSupplierFolder` (call directly with a manually-built
`models.Supplier{ID: ..., SUSupplierCode: ...}` and `subParts`, bypassing the
DB-backed `fetchSupplier`) and `SupplierFile`.

`renderSupplierFolder`:
- 503 when `SupplierFilesRoot`/`DocControlRoot` both unconfigured.
- 400 when `SUSupplierCode` is empty.
- 400 when `subParts` contains `".."` segments that would escape the supplier's
  folder (this handler re-derives the traversal check itself via
  `absBase`/`absPath` prefix comparison — exercise it directly since
  `SupplierFolderSub` normally pre-sanitizes `subParts` before this is called).
- Directory-not-found → renders `shared/error.html` (200, not 404 — note this
  differs from `files.go`'s `http.NotFound`; pin as-is).
- Sort order and parent-URL depth 0/1/2, same shape as `ServeSupplierDir`.

**Resolved decision (2026-08-05):** unlike `POFile`, `SupplierFile` calls
`h.fetchSupplier` (a DB read) before any of the testable logic, and that call
panics on a nil `*sql.DB` — it can't be exercised via `httptest` without a live
DB. Extract the post-fetch logic into a new unexported helper, mirroring the
existing `renderSupplierFolder` split:

```go
func (h *Handler) SupplierFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}
	h.serveSupplierFile(w, r, s)
}

func (h *Handler) serveSupplierFile(w http.ResponseWriter, r *http.Request, s models.Supplier) {
	// body unchanged from the current SupplierFile, starting at `root := h.cfg.SupplierFilesRoot`
}
```

Pure refactor, no behavior change — `id` is only used for the `fetchSupplier`
call, so `serveSupplierFile` doesn't need it (the existing body already gets
the splat via `r.URL.Path`, using `id` only to build the trim prefix — keep
threading `id` through as a parameter since the splat-trim needs the URL
param, i.e. `serveSupplierFile(w, r, s, id)`).

`serveSupplierFile` tests (called directly with a manually-built
`models.Supplier{SUSupplierCode: ...}`):
- 503 when both roots unconfigured.
- 400 when `SUSupplierCode` is empty.
- Uses `safePath` + `http.ServeFile`, so same `..`-in-URL → 400 `invalid URL
  path` pin as the `files.go` handlers (from `http.ServeFile`'s guard, not
  `safePath`'s).
- 404 when file doesn't exist / is a directory.
- `.pdf` → inline; non-pdf → `attachment; filename="..."` (note: raw string
  format here, not `mime.FormatMediaType` like `files.go` — pin as-is, no
  `Cache-Control` header either, another drift point to pin).

## `arx_go/pos_test.go` (add to existing file)

Covers `findPOBaseFolder`, `renderPOFolder` (call directly with a manually-built
`models.PurchaseOrder{Number: ...}` and `subParts`), and `POFile`.

`findPOBaseFolder`:
- Direct unit test: empty root, no matching prefix, one match, and (seed two
  dirs sharing a prefix) confirms it returns the first match in `os.ReadDir`
  order — pin whichever that is, don't assert a specific "correct" pick.

`renderPOFolder`:
- 503 when `POFolderRoot` unconfigured.
- 404 when no folder matches the PO number prefix.
- 404 when subpath doesn't exist.
- Sort order and parent-URL depth 0/1/2, same shape as above.
- No traversal-guard test here — unlike suppliers, `renderPOFolder` does not
  re-derive an absPath containment check, so this is a real gap, not test
  scope; leave a `// NOT COVERED:` comment noting it rather than asserting
  behavior that may not exist as a guard (confirm by reading the function: it
  only checks `os.Stat` after `filepath.Join`, no prefix check — a caller
  passing raw ".." segments could escape). Do not add a fix; just don't
  fabricate a passing traversal test that isn't actually testing a guard.

`POFile`:
- 503 when `POFolderRoot` unconfigured.
- 404 when no folder matches the PO number prefix.
- 404 when file doesn't exist / is a directory.
- `..`-in-URL → 400 `invalid URL path` from `http.ServeFile`'s guard, same
  mechanism as the `files.go` handlers (POFile does its own per-segment
  sanitizing like `safePath` does, but `http.ServeFile` still rejects on the
  raw `r.URL.Path` regardless).
- `.pdf` → inline; non-pdf → `attachment; filename="..."` (same raw-string
  drift as `SupplierFile`; no `Cache-Control` header).

## Success criteria

- `go test ./arx_go/...` and `go vet ./arx_go/...` pass.
- All 8 handlers/helpers listed in #863 have direct test coverage.
- Only test files changed, plus the one approved surgical refactor in
  `suppliers.go` (`SupplierFile` split into a thin DB-fetching wrapper +
  `serveSupplierFile` helper) needed to make `SupplierFile` testable without a
  DB.
