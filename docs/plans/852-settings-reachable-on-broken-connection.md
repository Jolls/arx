# #852 — Reach Settings when DB connection is misconfigured/unreachable

## Root cause

`h.database() != nil` only means a `*sql.DB` handle exists, not that it works.
`CheckSchemaVersion` (arxlib/config/config.go) is the only place that actually
probes the connection (queries `app_config.schema_version`), and it collapses
two different failures into one `schemaMismatch` string:
- the query itself errors (bad server/name/auth/connectivity — DB unusable)
- the query succeeds but returns an unexpected version (DB usable, just old schema)

`RequireAuth` redirects any non-empty `schemaMismatch` to `/login`, and
`RequireAuthOnceConnected` (gating `/settings`) only bypasses auth when
`h.database() == nil`. For a real connectivity failure, login is *also*
impossible (`LoginGet`/`LoginPost` call `h.userCount()`, which hits the same
broken connection and 500s), so the user is stuck: `/settings` requires login,
login 500s.

Per #748, `RequireAuthOnceConnected` deliberately keeps requiring login on a
mere schema-version mismatch (real, working DB) to stop unauthenticated
`/settings` writes (password exfiltration). That protection must stay intact.
So the fix distinguishes the two failure modes rather than bypassing on any
`schemaMismatch`.

## Resolved decision

Split `CheckSchemaVersion`'s single return string into two: a connectivity
error (query itself failed) vs. a version mismatch (query worked, wrong
version). Only the connectivity-error case bypasses auth on `/settings`
(GET+POST) and short-circuits `/login`, matching `h.database() == nil`
behavior. The version-mismatch case keeps requiring login, unchanged.

## Changes

### 1. `arxlib/config/config.go` — `CheckSchemaVersion`
Change signature to return two strings instead of one:
```go
// CheckSchemaVersion queries app_config for schema_version. connErr is
// non-empty when the query itself failed (DB unreachable/misconfigured —
// distinct from a working DB on an old schema). mismatch is non-empty when
// the query succeeded but returned an unexpected version.
func CheckSchemaVersion(ctx context.Context,
	queryRow func(context.Context, string, ...any) *sql.Row,
	appConfigTable string) (mismatch string, connErr string) {
	var val string
	err := queryRow(ctx,
		`SELECT setting_value FROM `+appConfigTable+` WHERE setting_key = 'schema_version'`,
	).Scan(&val)
	if err != nil {
		return "", fmt.Sprintf("could not read schema_version (%v)", err)
	}
	if val != ExpectedSchemaVersion {
		return fmt.Sprintf("DB schema v%s, app expects v%s", val, ExpectedSchemaVersion), ""
	}
	return "", ""
}
```

### 2. `arx_go/handlers.go`
- Add field to `Handler` struct next to `schemaMismatch string` (line ~43):
  `dbConnError string`.
- Update `(h *Handler) CheckSchemaVersion(ctx)` (line ~226):
```go
func (h *Handler) CheckSchemaVersion(ctx context.Context) {
	if h.database() == nil {
		h.schemaMismatch = ""
		h.dbConnError = ""
		return
	}
	h.schemaMismatch, h.dbConnError = arxbase.CheckSchemaVersion(ctx, h.queryRowContext, h.cfg.AppConfigTable())
}
```
- `RequireAuth` (line ~372-391): bypass to `/settings` on connectivity error too:
```go
func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.database() == nil || h.dbConnError != "" {
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
		if h.schemaMismatch != "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		...
```
  Update the doc comment above it to mention the connectivity-error bypass.
- `RequireAuthOnceConnected` (line ~400-413): bypass on connectivity error too:
```go
func (h *Handler) RequireAuthOnceConnected(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.database() == nil || h.dbConnError != "" {
			next.ServeHTTP(w, r)
			return
		}
		...
```
  Update its doc comment: still requires login on a plain schema-version
  mismatch (per #748); only a genuine connectivity failure bypasses.
- `render` (line ~515-518): add `m["DBConnError"] = h.dbConnError` alongside
  the existing `m["SchemaMismatch"] = h.schemaMismatch`.

### 3. `arx_go/render_records.go`
Same addition in `renderRecords` (line ~20): `m["DBConnError"] = h.dbConnError`.

### 4. `arx_go/auth.go`
- `LoginGet` (line ~232) and `LoginPost` (line ~258): extend the existing
  `if h.database() == nil { redirect to /settings }` guard to also catch the
  connectivity-error case, so `/login` never calls `h.userCount()` against a
  broken connection (avoids the 500 dead end if `/login` is hit directly):
```go
if h.database() == nil || h.dbConnError != "" {
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
	return
}
```

### 5. `arx_go/settings.go`
- `settingsData` (line ~186-229): add `"DBConnError": h.dbConnError,` to the
  returned map (alongside `"Connected"`).

### 6. `arx_go/templates/settings/settings.html`
Add a banner near the existing `{{if not .Connected}}` block (line ~6-11) so
the user understands why they landed here even though `.Connected` shows true
(the socket opened; the queries don't work):
```html
{{if .DBConnError}}
<div class="alert alert-danger d-flex align-items-center gap-2" role="alert">
    <span class="fs-5">&#9888;</span>
    <span><strong>Database connection error:</strong> {{.DBConnError}} — fix the connection details below.</span>
</div>
{{end}}
```

## Out of scope
- `layout.html` / `login.html` DBConnError banners: with the redirect changes
  above, a connectivity failure never reaches those pages (redirected to
  `/settings` first), so no banner wiring needed there.
- No change to `RequireAuthOnceConnected`'s behavior for a genuine
  schema-version mismatch — login is still required (#748 protection intact).

## Tests to add (arx_go/middleware_test.go, following existing patterns)
- `TestRequireAuth_RedirectsToSettingsOnConnError`: `h.dbConnError` set,
  `h.schemaMismatch` empty → `RequireAuth` redirects to `/settings`, not `/login`.
- `TestRequireAuthOnceConnected_AllowsOnConnError`: `h.dbConnError` set, no
  session → `RequireAuthOnceConnected` lets the request through unauthenticated.
- `TestRequireAuthOnceConnected_StillRedirectsOnSchemaMismatchAlone`: only
  `h.schemaMismatch` set (not `dbConnError`) → still redirects to `/login`
  (regression guard for the #748 protection).
- Optionally a `arxlib/config` unit test for `CheckSchemaVersion`'s two-value
  return (query error → connErr only; version string mismatch → mismatch only;
  match → both empty).

## Verification
- `cd arx_go; .\build.bat` (runs `go test ./...`).
- Manual: point `config/local.json` DB fields at a nonexistent server, start
  the app, confirm `/settings` loads directly (no login redirect loop) and
  shows the new connection-error banner.
