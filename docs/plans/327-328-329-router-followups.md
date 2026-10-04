# #327, #328, #329 — router follow-ups from #319

Three small follow-ups found during #319 (chi → stdlib router migration) code review. All touch `arx_go/router.go` / `arx_go/main.go`; #329 also touches `arx_go/pos.go` and `arx_go/suppliers.go`. Order: #329 (pure deletion) → #327 (behavior decision + test) → #328 (signature change + call-site migration + test), since #328's generalized `handleWildcard` signature is the most invasive and should land last.

## #329 — delete unreachable bare-prefix check

`POFile` (arx_go/pos.go:1106-1125) and `serveSupplierFile` (arx_go/suppliers.go, ~720-741) each do:
```go
prefix := fmt.Sprintf("/po/%s/file/", num)
// A bare "/po/{id}/file" with no trailing slash still matches this route's
// "{rest...}" wildcard ... — reject it the same way chi did (#319).
if !strings.HasPrefix(r.URL.Path, prefix) {
    h.NotFound(w, r)
    return
}
splat := strings.TrimPrefix(r.URL.Path, prefix)
```
Both routes are registered via `handleWildcard`, which already registers an exact-literal sibling for the bare prefix mapped to `h.NotFound` (arx_go/main.go:356, :395). `net/http.ServeMux` prefers the more specific exact-literal pattern over the wildcard, so the bare path never reaches these handlers — the check is dead.

**Revised during implementation (medium-effort code review caught this):** the `if !strings.HasPrefix(...) { h.NotFound...; return }` block is unreachable via the router in production, but `TestPOFile_BarePrefixNotFound`/`TestServeSupplierFile_BarePrefixNotFound` call `POFile`/`serveSupplierFile` directly (bypassing the router) and document this exact check as the handler's own contract. Deleting it left those tests passing only by coincidence (the mis-joined path happens not to exist in the test's temp dir) instead of by the handler actually rejecting it — a latent file-serving risk for any future direct/non-router caller. Kept the check in both handlers; corrected the comment instead (it had wrongly implied the bare path still matches the router's wildcard, which is false post-#319 — restated as the handler's own defense-in-depth invariant, independent of router wiring).

**Test plan:**
- Coverage audit: `TestPOFile_BarePrefixNotFound`/`TestServeSupplierFile_BarePrefixNotFound` already assert this directly; no new test needed since the implementation (check retained, comment only) didn't change behavior.

## #327 — bare-prefix sibling auth behavior

**Resolved decision:** keep current behavior. An unauthenticated `GET /local` (no trailing slash) hits `withAuth` before the sibling's `h.NotFound`, so it 302s to `/login`, same as every other `withAuth`-gated route — consistent gating beats chi parity here. No code change in `handleWildcard`/`router.go`; this is a documentation + test change only.

**Change:** doc comment only. Add a note to `handleWildcard` in arx_go/router.go clarifying the sibling runs behind the same `mw` as the wildcard route, so an auth-gated prefix redirects-to-login for an unauthenticated bare request rather than 404ing (deliberate, per #327).

**Test plan:**
- Coverage audit found existing coverage, so no new test is needed: `TestBuildRouter_AllAppRoutesRequireAuth` (arx_go/middleware_test.go:430) already walks every registered route including each `handleWildcard` bare-prefix sibling (confirmed by running it with `-v`: `GET /local`, `GET /local-dir`, `GET /supplier-local`, `GET /images`, etc. all appear and all assert a redirect-to-login for an anonymous caller). This already is the "test either way" the issue asked for; it was added before #319/#330 and the issue's author (reviewing #319) missed it.
- No tests to add: documentation-only change.

## #328 — generalize handleWildcard to take a bare-prefix handler

**Resolved decision:** generalize `handleWildcard`'s signature to accept the bare-prefix handler as a parameter instead of hardcoding `h.NotFound`, so the four folder/upload routes (which have real bare-prefix handlers, not 404 placeholders) register through the same mechanism.

**Change in arx_go/router.go:**
```go
// handleWildcard registers prefix+"{rest...}" for method (wrapped in mw),
// plus an exact sibling for the bare prefix (no trailing slash) using
// bareHandler. net/http's ServeMux otherwise silently 307-redirects that
// bare request to prefix+"{rest...}" before any handler runs — any
// "x/{name...}" pattern implies the subtree pattern "x/" the same way a bare
// "x" implies it — so without the sibling, a bare "/local" would 307 to
// "/local/" instead of reaching bareHandler the way chi's old "/local/*" did
// when dialed without the trailing slash (#319). Most callers pass
// h.NotFound as bareHandler (no meaningful bare-prefix page exists); a few
// (the PO/supplier folder and folder-upload routes, #328) pass their real
// bare-prefix handler, since the bare path is a legitimate route (the
// folder root), not a dead end. prefix must end in "/". Both withAuth-gated
// paths 302-to-login for an unauthenticated request before bareHandler
// runs, same as any other withAuth route (#327).
func (b *routeBuilder) handleWildcard(method, prefix string, mw func(http.Handler) http.Handler, handler, bareHandler http.HandlerFunc) {
	b.handle(method, prefix+"{rest...}", mw, handler)
	b.handle(method, strings.TrimSuffix(prefix, "/"), mw, bareHandler)
}
```
Drop the now-unused `h *Handler` parameter (it was only used to reach `h.NotFound`).

**Call-site changes in arx_go/main.go:**
- The 10 existing call sites (`/static/`, `/local/`, `/local-dir/`, `/local-dir-upload/`, `/supplier-local/`, `/supplier-local-dir/`, `/supplier-local-dir-upload/`, `/images/`, `/supplier/{id}/file/`, `/po/{id}/file/`) drop the leading `h` argument and add `h.NotFound` as the last argument.
- Replace these 4 line-pairs:
  ```go
  b.handle(http.MethodGet, "/supplier/{id}/folder", withAuth, h.SupplierFolder)
  b.handle(http.MethodGet, "/supplier/{id}/folder/{rest...}", withAuth, h.SupplierFolderSub)
  ```
  with:
  ```go
  b.handleWildcard(http.MethodGet, "/supplier/{id}/folder/", withAuth, h.SupplierFolderSub, h.SupplierFolder)
  ```
  and:
  ```go
  b.handle(http.MethodPost, "/supplier/{id}/folder-upload", withAuth, h.SupplierFolderUpload)
  b.handle(http.MethodPost, "/supplier/{id}/folder-upload/{rest...}", withAuth, h.SupplierFolderUploadSub)
  ```
  with:
  ```go
  b.handleWildcard(http.MethodPost, "/supplier/{id}/folder-upload/", withAuth, h.SupplierFolderUploadSub, h.SupplierFolderUpload)
  ```
  Same pattern for `/po/{id}/folder` (→ `h.POFolderSub`/`h.POFolder`) and `/po/{id}/folder-upload` (→ `h.POFolderUploadSub`/`h.POFolderUpload`).

**Test plan:**
- Coverage audit: `TestBuildRouter_WildcardRoutesPreserveChiMatchingSemantics` covers the 404-bareHandler routes; nothing covers that the four folder/upload bare paths still reach their real handler after the refactor.
- Characterization test: before the refactor, confirm (by reading, not a new test — behavior is unchanged pre/post since this is a pure registration refactor) that `GET /supplier/{id}/folder` and `GET /po/{id}/folder` currently reach `SupplierFolder`/`POFolder`. Existing handler tests (if any) for these already exercise this; grep first.
- Red→green test: `TestBuildRouter_WildcardRoutesHaveBarePrefixSibling` — walk `buildRouter`'s route table (via the existing `registeredRoute` walk used elsewhere in tests) and assert every registered `{rest...}` pattern has a corresponding bare-prefix (no trailing slash) sibling pattern registered for the same method. This is the safety-net the issue asked for as the fallback option; worth adding alongside the refactor since it guards against the next person adding a `{rest...}` route by hand without the sibling. Fails today only if a `{rest...}` route is missing its sibling — confirm it passes after the refactor (all siblings present) and would fail if one were removed (spot-check by temporarily commenting one out locally, not committed).
- Manual-only: open an existing PO's and supplier's folder view (bare path, no sub-path) in the browser, confirm it still renders the folder root listing (not 404) — this exercises the live local-filesystem folder read `SupplierFolder`/`POFolder` perform, which isn't practical to unit test (depends on `SUPPLIER_FILES_ROOT`/`PO_FOLDER_ROOT` filesystem state).
