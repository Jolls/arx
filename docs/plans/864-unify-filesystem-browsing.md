# Plan: Unify local-filesystem browsing (#864)

**Sequencing decision (resolved, not a judgment call):** #861 will already be in the working tree (Cache-Control, `mime.FormatMediaType` filename encoding) before this plan is applied. `arx_go/files.go`'s `ServeLocalFile`/`ServeSupplierFile` already show this fixed pattern today. `suppliers.go`'s `serveSupplierFile` and `pos.go`'s `POFile` will match it too by the time this plan runs. Therefore issue #864's "step 3: land #861's fix at the unified site" is moot: extraction and fix are combined into a single `refactor:` commit that pulls from the already-correct post-#861 code. Verify the actual post-#861 `serveSupplierFile`/`POFile` bodies at implementation time and extract from those.

**Test coverage confirmed adequate.** Existing tests (`arx_go/files_test.go`, `arx_go/suppliers_test.go`, `arx_go/pos_test.go`) cover every one of the 8 functions: `ServeLocalDir` (7 tests), `ServeSupplierDir` (7 tests), `renderSupplierFolder` (6 tests, called directly bypassing DB), `renderPOFolder` (6 tests), `ServeLocalFile` (6 tests), `ServeSupplierFile`/files.go (6 tests), `serveSupplierFile`/suppliers.go (6 tests), `POFile` (6 tests). Two tests pin *intentional* cross-site drift as current behavior: `TestRenderSupplierFolder_NotFound` (200 + error page, not files.go's 404) and `TestRenderSupplierFolder_PathTraversalRejected` (400, unlike `safePath`'s silent-strip-and-serve behavior used elsewhere). These two must stay green unchanged by step 2's extraction.

## Directory-listing extraction

New types/helper in `arx_go/files.go`:

```go
type dirListingEntity struct {
    Key   string // "PO", "Supplier", or "" for neither
    Value any    // *models.PurchaseOrder, *models.Supplier, or nil
}

type dirListingParams struct {
    Path          string           // absolute path to list; caller has already validated existence/is-dir/containment
    RelParts      []string         // path segments from the site's root to Path; nil at the root
    DirURLPrefix  string           // no trailing slash, e.g. "/local-dir", "/supplier/5/folder", "/po/PO-100/folder"
    FileURLPrefix string           // no trailing slash, e.g. "/local", "/supplier/5/file", "/po/PO-100/file"
    DirName       string           // caller-computed display name
    ParentURL     string           // caller-computed; "" at root
    Entity        dirListingEntity
    ActiveTab     string
    ActiveSubTab  string
    NavBackURL    string
    NavBackLabel  string
}

func (h *Handler) renderDirListing(w http.ResponseWriter, r *http.Request, p dirListingParams)
```

`renderDirListing` does: `os.ReadDir(p.Path)` (500 on error), the existing sort comparator, the entry loop building `DirEntry{Name, IsDir, URL, Ext, Size}` with `URL` computed uniformly as `relURL := name; if len(p.RelParts) > 0 { relURL = strings.Join(append(append([]string{}, p.RelParts...), name), "/") }` then `DirURLPrefix + "/" + relURL` (dirs) or `FileURLPrefix + "/" + relURL` (files) — verified by hand to reproduce today's exact URL strings at all four sites — then calls `h.render(w, r, "shared/local_dir.html", map[string]any{...})` with `TestMode: h.cfg.TestMode` always included and `p.Entity.Key`/`p.Entity.Value` set into the map only if `Key != ""` (matches the template constraint: `local_dir.html` only branches on `{{if .PO}}`/`{{if .Supplier}}`).

Also extract (files.go only — used by `ServeLocalDir`/`ServeSupplierDir`, which derive `RelParts` from `filepath.Rel` rather than receiving `subParts`):

```go
func relSegments(root, path string) []string
```
— identical logic to the existing duplicated block (`filepath.Abs` both, `filepath.Rel`, split on `filepath.Separator`, drop empties).

**Containment check placement — NOT unified in step 2.** Do not replace `renderSupplierFolder`'s hand-rolled abs-prefix check with `safePath`: `safePath` silently strips `..`/`.` segments and serves whatever remains under root (as `ServeLocalDir`/`ServeSupplierDir` already do — see `TestServeLocalDir_DotDotSegmentsStayUnderRoot`, which expects 200), whereas `TestRenderSupplierFolder_PathTraversalRejected` pins a 400 for the same shape of input. Swapping the check would flip that test's expectation. Keep `renderSupplierFolder`'s existing hand-rolled check verbatim, before calling `renderDirListing`.

`renderPOFolder` has zero test pinning its (currently absent) containment behavior, so add `safePath(base, strings.Join(subParts, "/"))` there (using the existing `safePath` from `files.go`) before calling `renderDirListing` — this adds no new test failures and directly answers the issue's complaint that "`renderPOFolder` asserts nothing." Do the same for `POFile` (file-serving side, see below).

Net result: `safePath` becomes the containment mechanism for `ServeLocalDir`, `ServeSupplierDir`, `renderPOFolder`, and `POFile`. `renderSupplierFolder` keeps its own hand-rolled check (its differing 400-on-traversal semantics is pinned behavior, not something this refactor may change) — see Open Question below.

### Before/after per directory-listing call site

- **`ServeLocalDir`** (`files.go:247`): keep root lookup, `safePath`, stat/not-found handling as-is. Replace the `filepath.Rel` block with `relParts := relSegments(root, path)`. Keep the existing `parentURL`/`dirName` computation blocks verbatim (preserves the `"/local-dir/"`-with-trailing-slash convention for the depth-1 case, needed because `main.go:179` registers only `r.Get("/local-dir/*", ...)`, no bare `/local-dir` route). Replace the sort/loop/render tail with one call to `h.renderDirListing(w, r, dirListingParams{Path: path, RelParts: relParts, DirURLPrefix: "/local-dir", FileURLPrefix: "/local", DirName: dirName, ParentURL: parentURL, ActiveTab: ""})`.
- **`ServeSupplierDir`** (`files.go:141`): identical transform, `DirURLPrefix: "/supplier-local-dir"`, `FileURLPrefix: "/supplier-local"`.
- **`renderSupplierFolder`** (`suppliers.go:706`): keep root/code lookup and the existing hand-rolled abs-prefix containment check (lines ~726–731) verbatim. Keep the existing `dirName`/`parentURL` computation verbatim (uses `fmt.Sprintf("/supplier/%d/folder", s.ID)`/`.../file`, no trailing slash, matching `main.go:288` which registers a bare `/supplier/{id}/folder` route). Replace the sort/loop/render tail with `h.renderDirListing(w, r, dirListingParams{Path: path, RelParts: subParts, DirURLPrefix: fmt.Sprintf("/supplier/%d/folder", s.ID), FileURLPrefix: fmt.Sprintf("/supplier/%d/file", s.ID), DirName: dirName, ParentURL: parentURL, Entity: dirListingEntity{Key: "Supplier", Value: &s}, ActiveTab: "suppliers", ActiveSubTab: "folder", NavBackURL: backURL, NavBackLabel: backLabel})`.
- **`renderPOFolder`** (`pos.go:1066`): after resolving `base := filepath.Join(root, baseName)`, add `path, ok := safePath(base, strings.Join(subParts, "/")); if !ok { http.Error(w, "Invalid path", http.StatusBadRequest); return }` in place of the current unchecked `filepath.Join`. Keep stat/not-found and `dirName`/`parentURL` computation verbatim (`fmt.Sprintf("/po/%s/folder", po.Number)`, no trailing slash — `main.go:324` registers a bare `/po/{id}/folder` route). Replace the tail with `h.renderDirListing(w, r, dirListingParams{Path: path, RelParts: subParts, DirURLPrefix: fmt.Sprintf("/po/%s/folder", po.Number), FileURLPrefix: fmt.Sprintf("/po/%s/file", po.Number), DirName: dirName, ParentURL: parentURL, Entity: dirListingEntity{Key: "PO", Value: &po}, ActiveTab: "pos", ActiveSubTab: "folder", NavBackURL: backURL, NavBackLabel: backLabel})`.

## File-serving extraction

```go
type fileServingParams struct {
    Root   string
    Splat  string
    Inline func(path string) bool // true → Content-Disposition: inline
}

func (h *Handler) serveLocalizedFile(w http.ResponseWriter, r *http.Request, p fileServingParams)
```

Body: `safePath(p.Root, p.Splat)` → 400 on failure; `os.Stat` → 404 if missing/is-dir, 500 on other error; `if p.Inline(path) { Content-Disposition: inline } else { Content-Disposition: mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}) }`; `Cache-Control: no-cache`; `http.ServeFile(w, r, path)`. This is a straight lift of the already-fixed `ServeLocalFile` body with the inline predicate parameterized.

**Resolved: the `Inline` predicate is identical everywhere** — #861 made image-inline uniform across all four file-serving sites (`ext == ".pdf" || urlutil.IsImage(path)`), so no per-site variation remains to preserve. `fileServingParams` can therefore drop the `Inline func(path string) bool` field entirely and hardcode the predicate inside `serveLocalizedFile` — simpler than plumbing a callback for behavior that no longer varies. Update the struct/signature:

```go
type fileServingParams struct {
    Root  string
    Splat string
}

func (h *Handler) serveLocalizedFile(w http.ResponseWriter, r *http.Request, p fileServingParams) {
    path, ok := safePath(p.Root, p.Splat)
    if !ok {
        http.Error(w, "Invalid path", http.StatusBadRequest)
        return
    }
    info, err := os.Stat(path)
    if os.IsNotExist(err) || (err == nil && info.IsDir()) {
        http.NotFound(w, r)
        return
    }
    if err != nil {
        http.Error(w, "Error accessing file", http.StatusInternalServerError)
        return
    }
    ext := strings.ToLower(filepath.Ext(path))
    if ext == ".pdf" || urlutil.IsImage(path) {
        w.Header().Set("Content-Disposition", "inline")
    } else {
        w.Header().Set("Content-Disposition",
            mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
    }
    w.Header().Set("Cache-Control", "no-cache")
    http.ServeFile(w, r, path)
}
```

**Before/after:**
- `ServeLocalFile` (`files.go:59`): root lookup unchanged; `h.serveLocalizedFile(w, r, fileServingParams{Root: root, Splat: splat})`.
- `ServeSupplierFile` (`files.go:100`): root lookup/fallback unchanged; `h.serveLocalizedFile(w, r, fileServingParams{Root: root, Splat: splat})`.
- `serveSupplierFile` (`suppliers.go:847`): keep root/code lookup; `base := filepath.Join(root, s.SUSupplierCode)`; `h.serveLocalizedFile(w, r, fileServingParams{Root: base, Splat: splat})`.
- `POFile` (`pos.go:1179`): keep `root`/`baseName` lookup; replace the manual per-segment `parts` loop with `splat := strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/po/%s/file/", num))`; `h.serveLocalizedFile(w, r, fileServingParams{Root: filepath.Join(root, baseName), Splat: splat})`. This switches POFile from ad-hoc sanitization to `safePath`, which is safe: no test pins POFile's internal sanitization mechanism, and `TestPOFile_DotDotInURLRejectedByServeFile` passes at the `net/http` layer regardless.

## Template constraint

`shared/local_dir.html` (`arx_go/templates/shared/local_dir.html`) branches only on `{{if .PO}}` / `{{else if .Supplier}}` / neither, and otherwise only reads `DirName`, `FullPath`, `ParentURL`, `Entries` (`Name`/`IsDir`/`URL`/`Ext`/`Size`), `NumDirs`, `NumFiles`. `dirListingEntity{Key, Value}` preserves this exactly — `renderDirListing` sets `data["PO"] = &po`, `data["Supplier"] = &s`, or nothing, matching today's three call patterns. No template changes needed.

## Verification steps

1. `go build ./...` (via build.bat, from arx_go/).
2. `go test ./arx_go/...` — all 8 functions' existing test suites must stay green unchanged (this is the behavior-preservation check called for by the issue).
3. Confirm the two intentionally-drifted tests (`TestRenderSupplierFolder_NotFound`, `TestRenderSupplierFolder_PathTraversalRejected`) still pass unmodified.

## Resolved decision: unify containment semantics as a 4th commit

User decision: unify path-traversal handling in this issue, as an explicit `fix:` commit on top of the `refactor:` commit (not left as drift, not filed as a follow-up). `safePath`'s silent-strip-and-serve semantic wins, since 3 of 4 sites already use it — `renderSupplierFolder` changes to match, not the other way around.

### 10. `arx_go/suppliers.go` — `renderSupplierFolder`: replace hand-rolled containment check with `safePath`

Read the current hand-rolled abs-prefix check at ~line 727 before editing (exact variable names may differ slightly from the excerpt below — match what's actually there). The check currently looks like:

```go
	path := filepath.Join(base, filepath.Join(subParts...))
	absBase, _ := filepath.Abs(base)
	absPath, _ := filepath.Abs(path)
	if absPath != absBase && !strings.HasPrefix(absPath, absBase+string(filepath.Separator)) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
```

Replace with:
```go
	path, ok := safePath(base, strings.Join(subParts, "/"))
	if !ok {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
```

(`safePath` is defined in files.go, same package — no new import needed.)

This is a **behavior change**: `safePath` strips `..`/`.` segments and resolves whatever remains under `base`, rather than rejecting the request outright. A traversal attempt like `../../etc` now resolves to `base/etc` (silently stripped) instead of a 400.

### 11. Test update: `arx_go/suppliers_test.go` — `TestRenderSupplierFolder_PathTraversalRejected` (lines 76–87)

Rename and update to match the new silent-strip semantics. Since `root/etc` is never created in this test, the stripped path resolves to a nonexistent directory, which `renderSupplierFolder` handles via its existing not-found path (200, renders `shared/error.html` — same as `TestRenderSupplierFolder_NotFound`), not a 404.

old_string:
```go
func TestRenderSupplierFolder_PathTraversalRejected(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	s := models.Supplier{ID: 1, SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/folder/../../etc", nil)
	rec := httptest.NewRecorder()
	h.renderSupplierFolder(rec, req, s, []string{"..", "..", "etc"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
```
new_string:
```go
// renderSupplierFolder now uses safePath (#864), matching ServeLocalDir/
// ServeSupplierDir/renderPOFolder: traversal segments are silently
// stripped and the request resolves under root, rather than rejected.
func TestRenderSupplierFolder_DotDotSegmentsStayUnderRoot(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	if err := os.Mkdir(filepath.Join(root, "etc"), 0755); err != nil {
		t.Fatal(err)
	}
	s := models.Supplier{ID: 1, SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/folder/../../etc", nil)
	rec := httptest.NewRecorder()
	h.renderSupplierFolder(rec, req, s, []string{"..", "..", "etc"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (resolves to root/etc), body=%s", rec.Code, rec.Body.String())
	}
}
```

Note this mirrors `TestServeLocalDir_DotDotSegmentsStayUnderRoot` (files_test.go:393) in both setup (creates `root/etc` so the resolved path exists and returns a real listing, not the not-found error page) and assertion shape — keep them consistent since they now test the same underlying `safePath` mechanism.

### 12. Also add `safePath` containment to `POFile` (file-serving side)

Per section "File-serving extraction" above, `POFile`'s new body via `serveLocalizedFile` already gets `safePath` for free (item 2b/#861's plan touches this too — verify no duplicate edit). No separate action needed here beyond what's already specified in the file-serving extraction section.

### Commit structure for this issue (both now resolved, no longer open)

1. `refactor:` — extract `renderDirListing`/`relSegments`/`serveLocalizedFile`, behavior-preserving except for the intentionally-inherited #861 fixes already in the tree. All existing tests green **except** the one updated in step 11 below, which lands in the same commit since it's driven by the mechanical extraction of `renderPOFolder`'s containment gap being closed — decide at implementation time whether to fold #10/#11 into the refactor commit or keep them separate; either is acceptable since `renderSupplierFolder`'s behavior change is small and self-contained.
2. `fix:` — items #10/#11 above (containment unification) if kept as a separate commit per the issue's original methodology.

### Critical files
- arx_go/files.go
- arx_go/suppliers.go
- arx_go/pos.go
- arx_go/files_test.go
- arx_go/suppliers_test.go
- arx_go/pos_test.go
- arx_go/templates/shared/local_dir.html
