# #120 — Admin gates as route-group middleware

Goal: every admin-only endpoint's requirement is visible in `arx_go/main.go`; handler bodies stop calling `requireAdmin` / `isAdmin` for gating. One pass, all eleven endpoints.

## Verified endpoint list (11)

Currently inside the single `r.Group(... r.Use(h.RequireAuth) ...)` in `buildRouter` (`main.go` ~157):

Plain-text 403 (`requireAdmin`, `http.Error(w, "forbidden", 403)`):
1. `POST /settings/digikey` -> `SettingsDigiKeySave` (`settings.go` ~288)
2. `GET /settings/backup` -> `SettingsBackup` (`settings.go` ~643)
3. `GET /settings/utilities` -> `UtilitiesReport` (`utilities.go` ~35)
4. `POST /settings/users` -> `SettingsUsersCreate` (`auth.go` ~425)
5. `POST /settings/users/{userID}/password` -> `SettingsUsersResetPassword` (~448)
6. `POST /settings/users/{userID}/toggle-active` -> `SettingsUsersToggleActive` (~481)
7. `POST /settings/users/{userID}/toggle-approve` -> `SettingsUsersToggleApprove` (~505)
8. `POST /settings/users/{userID}/toggle-approve-records` -> `SettingsUsersToggleApproveRecords` (~525)
9. `POST /settings/users/{userID}/toggle-admin` -> `SettingsUsersToggleAdmin` (~547)

JSON 403 (`isAdmin`, hand-rolled; `named_query_settings.go`):
10. `POST /settings/named-queries/save` -> `SettingsNamedQueryRowSave` (~77): `{"error":"Only an admin can edit named queries."}`
11. `POST /settings/named-queries/test` -> `SettingsNamedQueryTest` (~164): `{"error":"Only an admin can test named queries."}`

Not touched (already middleware, stay as-is): `POST /settings` and `GET /api/browse-folder` via `RequireAdminOnceConnected` (`main.go` 145, 154). `canEditConnection` (`handlers.go` ~450) keeps calling `h.isAdmin` — different rule (`dbUnusable() || isAdmin`), so `isAdmin` stays.

No other `requireAdmin`/`isAdmin` callers exist in non-test Go code (grep verified). Other route-level admin candidates not in scope: attachment-categories, categories, part-numbering, company-logo saves (deliberately not admin, #106 test comment).

## Behavior-preservation facts

- No handler uses the admin check for anything beyond gating. `SettingsUsersToggleActive` and `SettingsUsersToggleAdmin` read `h.currentUser(r)` themselves for the self-target guard; that stays. The user is still on the context because `RequireAuth` runs first and passes `r2` (from `withUser`) downstream.
- 403 shapes must stay byte-identical: plain-text = `http.Error(w, "forbidden", http.StatusForbidden)` (body `forbidden\n`, `text/plain; charset=utf-8`); JSON = `Content-Type: application/json`, status 403, `json.NewEncoder(w).Encode(map[string]string{"error": msg})`.
- Anonymous callers: in the real router they were already redirected (303 `/login`) by `RequireAuth` before reaching handlers; the in-handler "anonymous -> 403" only ever fired in direct-handler tests. Real-router behavior unchanged.
- The JSON handlers' other checks (`h.database() == nil` -> 503, etc.) and their own `Content-Type` header set stay in the handlers.

## Changes

### 1. `arx_go/handlers.go` (next to `RequireAdminOnceConnected`, ~467)
Add two middlewares (methods on `*Handler`, signature `func(http.Handler) http.Handler` like the neighbors), both gating on `h.isAdmin(r)`, with doc comments saying they must run after `RequireAuth`:
- `RequireAdmin`: non-admin -> plain-text 403 as above.
- `RequireAdminJSON`: non-admin -> JSON 403 as above (message: see Open question 1).

### 2. `arx_go/main.go`
- Split the group. In the existing `RequireAuth` group, delete the route lines for the 9 plain-text routes, the 2 named-query routes, and their now-orphaned comment blocks ("User management", "Data backup", "Data diagnostics", named-queries comment) — relocate the comments with the routes. Keep in the existing group: attachment-categories, categories, part-numbering, company-logo (+remove), preferences, accent-color, default-route, timezone, and everything after.
- Add, directly after the existing `RequireAuth` group's settings routes (placement: adjacent, before or after that group; order irrelevant to chi since paths are distinct), two new groups:
  - `r.Group(func(r chi.Router){ r.Use(h.RequireAuth, h.RequireAdmin) ...})` containing the 9 plain-text routes (digikey, backup, utilities, six users routes), with a comment stating "admin-only, plain-text 403".
  - `r.Group(func(r chi.Router){ r.Use(h.RequireAuth, h.RequireAdminJSON) ...})` containing the 2 named-query routes, comment noting JSON 403 is required because the settings page parses every response with `r.json()`.
- Keep the existing comments' substance (#103, #106, #750, #771 references).

### 3. Remove in-handler gates
- `auth.go`: delete the `if !h.requireAdmin(w, r) { return }` block from the six `SettingsUsers*` handlers. Delete `requireAdmin` (no callers left). Keep `isAdmin`; update its doc comment (currently says "requireAdmin wraps it ... JSON endpoints call it directly") to reference the middlewares and `canEditConnection`. Update the #750 comment on the deleted func's rationale by moving it onto the `RequireAdmin` middleware doc.
- `settings.go`: delete the two-line gate (+ its "#106" comment line) in `SettingsDigiKeySave` and `SettingsBackup`.
- `utilities.go`: same in `UtilitiesReport`.
- `named_query_settings.go`: delete the `isAdmin` blocks (+ #103 comments) in `SettingsNamedQueryRowSave` and `SettingsNamedQueryTest`; move the "why admin" rationale into the `main.go` group comment.
- Check for now-unused imports in each edited file after the deletions (`net/http` etc. are still used; verify with build).

### 4. Tests (all in `arx_go/`)
Handler-level 403 tests stop being valid (handlers no longer gate), so replace them with router/middleware-level tests before deleting:
- `settings_admin_gate_test.go` `TestSettingsEndpointsRequireAdmin`: rewrite to drive `buildRouter(h)` with `testHandlerWithDB()` and `sessionFor(...)` (existing helper in this file) for a non-admin: assert 403, body `forbidden\n`, for digikey/backup/utilities; extend the table to all nine plain-text routes (users routes need CSRF cookie/token on POSTs — reuse the csrf seeding pattern from `middleware_test.go` `TestBuildRouter_AllAppRoutesRequireAuth`). Drop the "anonymous is refused" subtests (now a 303 to `/login`, already covered by `TestBuildRouter_AllAppRoutesRequireAuth`).
- Add direct middleware unit tests for `RequireAdmin` and `RequireAdminJSON` (non-admin refused with the exact shape; admin reaches `sentinel`; nil user refused), replacing `TestRequireAdmin_RejectsNonAdminAndNilUser` (`auth_test.go` ~171, which calls the deleted `requireAdmin`) — delete that test.
- `auth_test.go` `TestSettingsUsers_ForbiddenForNonAdmin` (~207): delete (covered by the router table above); fix nothing else in the file.
- `named_query_unit_test.go` `TestNamedQuerySettings_AdminOnly`: rewrite non-admin cases through the router (403 + `Content-Type: application/json` + JSON body with non-empty `error`); the "admin passes the gate" case goes through the router too (status != 403 with `testHandlerWithDB` and no real DB is acceptable only if the handler does not panic on the nil/stub DB — see Open question 2). Update the stale comment about `requireAdmin`'s plain-text 403.
- `integration_test.go` (build-tag `integration`, not run by default; still must compile) `TestIntegration_UserAdminRequiresAdmin` (~2686): calls the six handlers directly and expects 403; will now fail. Rewrite to go through `buildRouter(h)` with a non-admin session, or delete in favor of the unit router test — see Open question 3. Success-path tests using `adminCtx` are unaffected.
- `middleware_test.go` `TestBuildRouter_AllAppRoutesRequireAuth` needs no change (RequireAuth is first in both new groups); confirm it still passes and `visited >= 150`.
- Optional route-table guard test (only if answered yes in Open question 4): walk `chi.Walk` and assert the eleven routes carry the admin middleware (chi cannot introspect middleware identity easily; behavioral check via the router test above already covers it), so recommend skipping.

### 5. Docs
- `CHANGELOG.md`: new top entry `## [0.7.54] - <date of the PR>` (current top is 0.7.53), heading `### Changed` (or `### Security`; see Open question 5): "Admin-only endpoints are now enforced by route middleware declared in `main.go` instead of per-handler checks; responses for non-admins are unchanged ([#120](https://github.com/Jolls/arx/issues/120))". PR body must include `Closes #120`.
- `RELEASE_NOTES.md`: no change (not user-facing).
- Existing plans in `docs/plans/` are history; do not edit.

## Verification
- `cd arx_go; go build ./... ; go vet ./... ; go test ./...` and same in `arxlib` (or `.\build.bat` from `arx_go`).
- `grep -n "requireAdmin\|isAdmin(" arx_go/*.go` returns only: the `isAdmin` definition, `canEditConnection`, and the two new middlewares (and test references).
- `main.go` shows all eleven admin-only routes under admin groups or `RequireAdminOnceConnected`.
- Integration tests (`-tags integration`, ArxDev only) compile; run per pre-commit sequence since integration-covered code changed.
- Manual (user): as non-admin, POST digikey/users routes -> 403 "forbidden"; named-query save/test from the Settings page -> inline JSON error message (not a parse failure); as admin, all still work; anonymous -> redirect to /login.

## Open questions
1. `RequireAdminJSON` serves two routes whose messages differ ("edit" vs "test"). Options: (a) one generic message, e.g. "Only an admin can do this." (small user-visible text change); (b) constructor taking the message, making the group's two routes need separate groups/`With`; (c) derive from path. Recommendation: (a), unless exact text must be preserved.
2. Router-level "admin passes the gate" test for named queries: `testHandlerWithDB` DB may be a stub; if the handler would touch it, keep only the middleware-level admin-passes assertion (via `sentinel`). Recommendation: middleware-level only.
3. Integration test `TestIntegration_UserAdminRequiresAdmin`: rewrite through the router, or delete (equivalent unit router test added)? Recommendation: delete, since it needs no DB after this change.
4. Add a route-table guard test? Recommendation: no (behavioral router test suffices).
5. CHANGELOG subheading `### Changed` vs `### Security`? Recommendation: `### Changed` (no behavior change).
6. Template cleanup (`{{if and .Connected .CurrentUser .CurrentUser.IsAdmin}}` x6 in `arx_go/templates/settings/settings.html` at lines 33, 36, 269, 433, 461, 577; precomputed field like `CanEditConnection` in `settingsData`, `settings.go` ~203): in or out of scope? Recommendation: out — cosmetic, touches a template plus `settingsData`, and the issue itself marks it optional; do as a separate small PR if wanted.

## Resolved decisions
1. `RequireAdminJSON` uses one generic 403 message for both named-query routes.
2. Named-query "admin passes the gate" test: middleware level only.
3. Delete `TestIntegration_UserAdminRequiresAdmin`; the new router-level unit test replaces it.
4. No route-table guard test.
5. CHANGELOG under `### Changed`.
6. `settings.html` IsAdmin cleanup is OUT of scope.
