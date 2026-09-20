# 103 — Gate the named-query editor behind admin

Issue: [#103](https://github.com/Jolls/arx/issues/103). Part of the pre-publication
review, [#102](https://github.com/Jolls/arx/issues/102).

## Problem

`POST /settings/named-queries/test` takes the `sql` form value straight from the
request and runs it against the live connection, returning rows as JSON.
`SettingsNamedQueryRowSave` persists arbitrary SQL the same way. Both sit inside the
`RequireAuth` group in `arx_go/main.go:167-168` with no admin check.

`isSafeQuery` (`arx_go/named_query.go:26`) only enforces a `SELECT` prefix plus a
DML/DDL keyword blocklist. That stops writes but is not an authorization control: a
plain `SELECT username, password_hash FROM <users>` passes unchanged, so any
authenticated non-admin can read every table the app DB user can — including the
password hashes the backup export deliberately withholds.

## Scope — what must NOT be gated

Two named-query paths are legitimately used by non-admins and must keep working:

- `GET /api/named-query` (`arx_go/main.go:395` → `APINamedQuery`) — resolves a
  `query:name(...)` spec to a **stored** query and runs it. The SQL comes from the DB,
  curated by an admin, never from the request. This is what `spec_nom` auto-fill in
  test-record forms calls.
- `listNamedQueries` (`arx_go/named_query.go:43`) — returns name/description/params
  metadata only, no SQL body, for the form-definition editor's reference panel.

Only the two editor routes that accept SQL from the request get gated.

## Changes

### 1. `arx_go/auth.go` — extract the admin predicate

`requireAdmin` writes a plain-text 403 via `http.Error`. The named-query handlers
speak JSON and their client JS calls `r.json()` unconditionally
(`settings.html:951`, `:1042`), so a plain-text body would surface as a parse error
instead of the intended message.

Add `isAdmin(r) bool` holding the rule, and have `requireAdmin` call it, so both
response shapes share one definition of "is an admin":

```go
// isAdmin reports whether the session user is an admin. requireAdmin wraps it with
// a plain-text 403; JSON endpoints call it directly so they can keep their own
// error shape.
func (h *Handler) isAdmin(r *http.Request) bool {
	cu := h.currentUser(r)
	return cu != nil && cu.IsAdmin
}
```

`requireAdmin` body becomes `if h.isAdmin(r) { return true }` + the existing 403.

### 2. `arx_go/named_query_settings.go` — gate both handlers

In `SettingsNamedQueryRowSave` (line 70) and `SettingsNamedQueryTest` (line 150), after
the existing `writeErr` closure is defined and before any other work:

```go
if !h.isAdmin(r) {
	writeErr(http.StatusForbidden, "Only an admin can edit named queries.")
	return
}
```

Placed before the `h.database() == nil` check so an unauthorized caller gets 403 rather
than leaking connection state.

### 3. `arx_go/templates/settings/settings.html` — hide the tab and the pane

Both the tab link (line 31-33) and the pane (line 453, `{{if and .Connected
.CurrentUser}}` around `#tab-named-queries`) gate on `.Connected .CurrentUser`. Add
`.CurrentUser.IsAdmin` to both, matching how the Users tab already does it at lines 34
and 569.

The pane must be gated too, not just the tab link: panes are shown by `#hash`/`data-tab`,
so leaving the markup in the page would let a non-admin reach the editor UI by URL
fragment even with the tab link hidden.

### 4. Tests — `arx_go/named_query_unit_test.go`

Add handler tests asserting a non-admin session gets 403 from both routes, and that an
admin still succeeds. Follow the existing helper conventions in that file / `helpers_test.go`.

## Success criteria

- Non-admin `POST /settings/named-queries/test` → 403, JSON body with an `error` key.
- Non-admin `POST /settings/named-queries/save` → 403, same shape.
- Admin → unchanged behaviour.
- `GET /api/named-query` still works for a non-admin (stored-query path untouched).
- Non-admin's Settings page renders no Named Queries tab and no `#tab-named-queries` pane.
- `go build ./...`, `go vet ./...`, `go test ./...` pass in both modules.
