# Test coverage plan: auth/user-admin, sanitizeLandingRoute, parseIDList, shared templates (#758)

Scope: tests only, no production code changes. Part of epic #719.

Established patterns found in the repo (do not deviate):
- `arx_go/middleware_test.go` — `testHandler()` (db==nil, no templates) and `testHandlerWithDB()` (non-nil never-dialed `*sql.DB` via a registered `nop-driver`) for unit tests of code paths that don't actually execute a query.
- `arx_go/auth_test.go` — already has 2 tests (`TestCachedUserByID_ReturnsFreshEntryWithoutDB`, `TestInvalidateUserCache_RemovesEntry`) using `testHandler()` and directly seeding `h.userCache`. New auth unit tests append to this file.
- `arx_go/integration_test.go` — `//go:build integration`, live ArxDev only, run manually via `ARX_TEST_DSN`. Helpers: `liveHandler(t)` (opens real DB, `t.Skip` if no DSN, `t.Fatal` if DSN doesn't contain "arxdev"), `withID`/`withIDAndAttID` (inject chi route params), `postForm` (URL-encoded POST body), `assert302`/`assertStatus` (recorder assertions). This is the ONLY existing pattern for handlers that issue real queries — there is no sqlmock/mock-DB layer in this repo.
- `arx_go/handlers_test.go`, `helpers_test.go`, `arx_go/models/category_test.go` — plain table-driven tests for pure functions, no Handler needed.

Key constraint driving the split below: `LoginPost` and all five `SettingsUsers*` handlers call `h.userCount`/`h.queryRowContext`/`h.execContext` (real SQL) before any business validation most of the time — they cannot be unit-tested with `testHandler()`/`testHandlerWithDB()` because those handlers panic/error on a real query against a nil or fake DB. Anything that requires an actual row round-trip goes in `integration_test.go` under the `integration` build tag. Anything that returns before touching the DB (the `h.db == nil` guard, `requireAdmin`'s 403 gate, `Logout`, pure helpers) is unit-tested in `auth_test.go` with no build tag, using `testHandler()`/`testHandlerWithDB()`.

---

## 1. Login / user-admin (auth.go)

### 1a. Unit tests — `arx_go/auth_test.go` (no build tag, no live DB)

- `TestLoginPost_RedirectsToSettingsWhenNoDB`
  - Setup: `h := testHandler()` (db == nil). POST `/login` with any form body (e.g. empty).
  - Assert: 303 See Other, `Location: /settings` (mirrors `TestRequireAuth_RedirectsWhenNoDB` pattern). Confirms the `h.db == nil` guard at the top of `LoginPost` fires before any form/DB work.

- `TestLoginPost_BadFormData`
  - Setup: `h := testHandlerWithDB()`. Build a request with an invalid `Content-Type` (e.g. `application/x-www-form-urlencoded` body with a malformed `%` percent-escape, or set `Content-Type: multipart/form-data` with a garbage body) so `r.ParseForm()` returns an error before `userCount` is reached.
  - Assert: 400 Bad Request, body contains "bad form data".
  - Note: verify experimentally that `ParseForm()` actually errors before reaching the DB call for the chosen malformed body — if `ParseForm` tolerates it, this case isn't reachable without a DB and should be dropped from the unit-test tier (covered instead by the integration bootstrap/login tests below, which exercise the same code path against a real DB).

- `TestLogout_ClearsSessionAndRedirects`
  - Setup: `h := testHandler()`. Seed a session cookie with `user_id` set (same pattern as `TestRequireAuthOnceConnected_AllowsLoggedInUser`: run a seed request through `h.session(seed)`, set `sess.Values["user_id"] = 7`, `sess.Save`, copy cookies onto the real request).
  - Call `h.Logout(rec, req)`.
  - Assert: 303 See Other, `Location: /login`. Then decode the session from `rec`'s Set-Cookie (open a fresh request, attach the returned cookie, call `h.session(req)`) and assert `sess.Values["user_id"]` is absent — confirms the key was deleted, not just that a redirect happened.

- `TestRequireAdmin_RejectsNonAdminAndNilUser`
  - Table-driven over: `cu == nil` (no user in context), `cu.IsAdmin == false`, `cu.IsAdmin == true`.
  - Setup: `h := testHandler()`. Build request, optionally attach `ctxUserKey` via `context.WithValue` (mirrors `TestAccentThemeClass`'s pattern in handlers_test.go).
  - Assert: for the two rejecting cases, `requireAdmin` returns `false` and `rec.Code == 403`; for `IsAdmin == true`, returns `true` and nothing is written to `rec` (Code still 200 default / no body).

- `TestSettingsUsers_ForbiddenForNonAdmin` (table-driven across the 5 handlers)
  - Handlers under test: `SettingsUsersCreate`, `SettingsUsersResetPassword`, `SettingsUsersToggleActive`, `SettingsUsersToggleApprove`, `SettingsUsersToggleApproveRecords`, `SettingsUsersToggleAdmin` (six, not five — issue text says "five", but re-reading auth.go: `SettingsUsersCreate`, `SettingsUsersResetPassword`, `SettingsUsersToggleActive`, `SettingsUsersToggleApprove`, `SettingsUsersToggleApproveRecords`, `SettingsUsersToggleAdmin` — six SettingsUsers* handlers exist today. Flagged under Open Questions.)
  - Setup: `h := testHandler()` (db == nil, but `requireAdmin` is checked first in every handler and doesn't touch the DB, so this is safe). For handlers needing a `userID` chi param (`ResetPassword`, `ToggleActive`, `ToggleApprove`, `ToggleApproveRecords`, `ToggleAdmin`), inject a chi route context carrying `userID` (mirror `withID` from integration_test.go, or add a local `withUserIDParam` helper in auth_test.go since integration_test.go is build-tagged out of this file's compilation unit).
  - No user in context (anonymous).
  - Assert: 403 Forbidden for every handler, and (for the DB-touching ones) that no DB call was attempted — implicitly verified since `testHandler()`'s db is nil and a real query would panic/error loudly if reached, making a false pass detectable.

### 1b. Integration tests — `arx_go/integration_test.go` (build tag `integration`, live ArxDev)

- `TestIntegration_LoginPost_ValidCredentials`
  - Create a test user via `h.createUser(ctx, username, displayName, "correct-horse-battery-staple", false)` (unique username via timestamp suffix, same convention as `TestIntegration_PartLifecycle`'s `partNumber`).
  - POST `/login` with matching username/password via `postForm`.
  - Assert 302, `Location` matches `landingRoute` fallback (`/parts`, since `DefaultRoute` is unset for a fresh user).
  - Assert a session cookie was set (`rec.Result().Cookies()` non-empty).
  - Cleanup: hard-delete the created user row.

- `TestIntegration_LoginPost_InvalidPassword`
  - Same setup, POST with wrong password.
  - Assert 302, `Location` starts with `/login?error=`.
  - Assert no session cookie is set (or if gorilla always sets one, assert the decoded session has no `user_id`).

- `TestIntegration_LoginPost_UnknownUsername`
  - POST with a username guaranteed not to exist (timestamp-suffixed nonsense).
  - Assert 302 to `/login?error=invalid+username+or+password` (same branch as invalid password — `userByUsername` returns `u == nil`).

- `TestIntegration_LoginPost_MissingFieldsAtBootstrap`
  - Only meaningful if `userCount == 0` is reachable in a test DB — ArxDev is seeded with users, so `n == 0` won't naturally occur. Skip this case (documented as untestable without wiping the seeded ArxDev users table, which is out of bounds) — see Open Questions.

- `TestIntegration_SettingsUsersCreate_Success`
  - Build an admin user in context (`context.WithValue(ctx, ctxUserKey, &User{IsAdmin: true})` on the request — `requireAdmin` only reads `h.currentUser(r)`, so no session/cookie needed for this handler-level test, consistent with how `handlers_test.go` injects `ctxUserKey` directly).
  - POST valid username/display_name/password.
  - Assert 302 to `/settings?tab=users`.
  - Query the DB directly to confirm the row exists with `is_admin = 0`.
  - Cleanup: delete the row.

- `TestIntegration_SettingsUsersCreate_MissingFields`
  - Admin context, POST with `password` empty.
  - Assert 302 to `/settings?tab=users&error=all+fields+required`.
  - Assert no row was inserted (query count before/after, or check no user with that pending username exists).

- `TestIntegration_SettingsUsersResetPassword_Success`
  - Create a user, admin context, POST new password to `/settings/users/{id}/password` (inject `userID` via `withID`).
  - Assert 302 to `/settings?tab=users`.
  - Query `password_hash` and `bcrypt.CompareHashAndPassword` against the new password to confirm it changed.
  - Cleanup: delete the user.

- `TestIntegration_SettingsUsersResetPassword_EmptyPassword`
  - Same setup, empty `password` form value.
  - Assert 302 to `/settings?tab=users&error=password+required`, and that `password_hash` is unchanged from before the call.

- `TestIntegration_SettingsUsersToggleActive_Success`
  - Create a user (is_active defaults true per DDL), admin context (different ID than the target), POST to `/settings/users/{id}/toggle-active`.
  - Assert 302, then query `is_active` and confirm it flipped to false. Toggle again and confirm it flips back to true (exercises `ToggleBoolExpr` both directions).
  - Cleanup: delete the user.

- `TestIntegration_SettingsUsersToggleActive_CannotDeactivateSelf`
  - Admin context where `cu.ID == id` (the target user IS the current admin).
  - Assert 302 to `/settings?tab=users&error=cannot+deactivate+your+own+account`, and DB row `is_active` unchanged.

- `TestIntegration_SettingsUsersToggleApprove_Success` / `TestIntegration_SettingsUsersToggleApproveRecords_Success`
  - Same shape as ToggleActive success case, toggling `can_approve_po` / `can_approve_records` respectively. One test function per field (no self-guard on these two, unlike ToggleActive/ToggleAdmin).

- `TestIntegration_SettingsUsersToggleAdmin_Success`
  - Same shape as ToggleActive success case, toggling `is_admin`.

- `TestIntegration_SettingsUsersToggleAdmin_CannotRemoveOwnAdmin`
  - Admin context where `cu.ID == id`.
  - Assert 302 to `/settings?tab=users&error=cannot+remove+your+own+admin+rights`, DB `is_admin` unchanged.

- Each `Toggle*_Success` test should also assert `h.invalidateUserCache` had an effect — simplest check: pre-seed `h.userCache[id]` with a stale entry before the call, then after the call assert the cache entry is gone (`h.userCache[id]` absent), confirming invalidation ran. This avoids needing a second full `cachedUserByID` round-trip.

---

## 2. `sanitizeLandingRoute` (arx_go/settings.go:50)

New file: `arx_go/settings_test.go` (doesn't exist yet — package main, no build tag, pure function, no Handler needed).

`TestSanitizeLandingRoute` — table-driven, one `t.Run` sub-test per case:

| input | want route | want ok | why |
|---|---|---|---|
| `""` | `""` | false | empty input rejected outright |
| `"   "` (whitespace only) | `""` | false | trimmed to empty |
| `"/parts"` | `"/parts"` | true | plain internal path |
| `"/pos?f0=as"` | `"/pos?f0=as"` | true | path + query preserved |
| `"//evil.com"` | `""` | false | protocol-relative — `RequestURI()` on a schemeless `//host` URL still starts with `//`, explicitly guarded |
| `"http://evil.com/x"` | `"/x"` | true | **not rejected** — `url.Parse` treats this as an absolute URL, `RequestURI()` strips scheme+host down to `/x`, which passes the `/`-prefix check. This is intentional per the existing doc comment ("drops scheme/host") but means a full absolute URL to another host is reduced to a same-origin-looking path rather than rejected — test should assert this documented (if surprising) behavior, not assume rejection. Flagged under Open Questions since it's the crux of what "open redirect guard" is supposed to catch.
| `"https://evil.com"` (no path) | `"/"` | true | `RequestURI()` of a bare host is `"/"` — passes `/`-prefix and doesn't hit the `//` guard, so `ok=true`, route=`"/"`. Downstream `landingRoute` treats `"/"` as invalid and falls back to `/parts`, but `sanitizeLandingRoute` itself returns true here — test documents this. |
| `"javascript:alert(1)"` | `""` | false | `url.Parse` succeeds with scheme `javascript`, opaque, no leading `/` in `RequestURI()` → fails prefix check |
| `"not a url \x7f"` (invalid escape, e.g. `"%zz"`) | `""` | false | `url.Parse` returns an error |
| `"relative/path"` (no leading slash) | `""` | false | fails `/`-prefix check |
| `"/"` | `"/"` | true | root path IS accepted by `sanitizeLandingRoute` (the "/" exclusion lives in `landingRoute`, not here) — test should not conflate the two functions |

Also add `TestLandingRoute` (the caller in auth.go:234) since it's the actual security boundary combining `DefaultRoute` + fallback:
- `nil` user → `/parts`
- user with `DefaultRoute == ""` → `/parts`
- user with `DefaultRoute == "/"` → `/parts` (loop guard)
- user with `DefaultRoute == "/?f=1"` → `/parts` (loop guard, query-only root)
- user with `DefaultRoute == "//evil.com"` → `/parts` (defense-in-depth guard)
- user with `DefaultRoute == "/pos"` → `/pos` (valid passthrough)

---

## 3. Pure functions: `parseIDList`, `OrderedTestIDs`, `validResultType`

- `parseIDList` and `OrderedTestIDs` live in `arx_go/models/trmodels.go` (package `models`). New file: `arx_go/models/trmodels_test.go`.
- `validResultType` lives in `arx_go/named_query_settings.go` (package `main`). Add to existing `arx_go/handlers_test.go` (pure function, no Handler needed, consistent with `TestFormatDate`/`TestAttachLabel` in that file).

### `arx_go/models/trmodels_test.go`

`TestParseIDList` — table-driven (function is unexported; test file must be in package `models`):

| input | want |
|---|---|
| `""` | `nil` |
| `"1,2,3"` | `[]int{1,2,3}` |
| `" 1 , 2 ,3 "` | `[]int{1,2,3}` (tabs/spaces trimmed per `trim`) |
| `"1,,3"` | `[]int{1,3}` (empty token between commas skipped) |
| `","` | `nil` (both tokens empty) |
| `"0,5"` | `[]int{5}` — **note**: `n > 0` guard means id `0` is silently dropped, not preserved as an id. Test must assert this (load-bearing: a real step ID of 0 would vanish from ordering).
| `"abc,5"` | `[]int{5}` — non-numeric token resets `n` to 0 mid-parse and is dropped (the `else { n = 0; break }` on a bad digit, combined with the `n > 0` guard, silently discards malformed tokens rather than erroring)
| `"12abc,5"` | `[]int{5}` — partial-numeric token also dropped entirely, not truncated to `12`
| `"5,5,5"` | `[]int{5,5,5}` — duplicates are NOT deduplicated (confirms no dedup happens, since callers may rely on / be surprised by this)
| `"-1"` | `nil` — leading `-` isn't a digit, `n` stays 0, dropped (no negative-ID support)

`TestOrderedTestIDs` — thin wrapper test confirming delegation:
- `(&TestForm{TestOrder: "3,1,2"}).OrderedTestIDs()` → `[]int{3,1,2}` (order preserved, not sorted — load-bearing per the issue's "drives step ordering" note)
- `(&TestForm{TestOrder: ""}).OrderedTestIDs()` → `nil`

### `arx_go/handlers_test.go` addition

`TestValidResultType` — table-driven:
- `"list"` → true
- `"single"` → true
- `"multi"` → true
- `""` → false
- `"List"` (wrong case) → false
- `"bogus"` → false

### `copyFormSteps` (arx_go/records.go:2579)

Not a pure function — takes `*txLogger` and issues real INSERT/SELECT/UPDATE against `h.cfg.FormsTable()` / test-step tables. No unit-test path (no mock DB layer exists). Covered only indirectly via `OrderedTestIDs`/`parseIDList` unit tests above (the ordering logic it depends on) and, if desired, a future integration test exercising the "copy form" endpoints (`records.go:2764`/`2874` callers) — out of scope for this issue's pure-function ask. Flagged under Open Questions.

---

## 4. Shared templates (templates/shared/*)

Existing file: `arx_go/templates_parse_test.go`. Shared dir contains: `layout.html`, `partials.html` (used as includes, already parsed alongside every page in `TestCoreTemplatesParse`), and four standalone pages currently untested: `login.html`, `error.html`, `not_found.html`, `local_dir.html`.

Add one new test function, `TestSharedStandaloneTemplatesParse`, to `arx_go/templates_parse_test.go`:

```go
// sharedStandalonePages are templates/shared pages rendered on their own
// (not as a tab under coreTabDirs), each via its own render call in the
// handler code — login.html via renderLogin, error.html/not_found.html/
// local_dir.html via their respective render helpers. Zero parse coverage
// today means a renamed {{define}} block breaks these pages silently while
// TestCoreTemplatesParse/TestRecordsTemplatesParse stay green (#758).
var sharedStandalonePages = []string{"login.html", "error.html", "not_found.html", "local_dir.html"}
```

- `TestSharedStandaloneTemplatesParse`
  - For each name in `sharedStandalonePages`, before writing the loop: **read the handler that renders it** (`renderLogin` in auth.go:462 already read — uses `template.New("").ParseFS(h.tmplFS, "templates/shared/login.html")` standalone, no layout). Grep for the render call sites of `error.html`, `not_found.html`, `local_dir.html` to confirm whether each is parsed standalone (like login.html) or together with `layout.html`/`partials.html` (like the coreTabDirs pages) — **do not assume**; match whatever the real handler does, since a parse test that doesn't mirror the real ParseFS call list is worthless (it can pass while the real render path 500s).
  - Whatever the confirmed pattern is per template, call `template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS, <the exact file list the handler uses>)` and `t.Errorf` on parse failure, same style as the existing loops.
  - If `error.html`/`not_found.html`/`local_dir.html` turn out to use a different Funcs map than `coreTemplateFuncs()` (e.g. none, if they're simple enough), use whatever the real render call passes — implementer must check each handler's `template.New(...).Funcs(...)` invocation, not copy `coreTemplateFuncs()` by default.

This is the one part of the plan where the implementer must do a small amount of source-reading (grep `local_dir.html`, `not_found.html`, `error.html` render call sites in arx_go/*.go) before writing the test, because the exact ParseFS file list and Funcs map differ per template and guessing wrong would make the "parse test" pass while the real page still 500s — defeating the point of #758's ask.

---

## Open questions

1. **Five vs six `SettingsUsers*` handlers.** The issue text says "all five SettingsUsers*" but auth.go currently defines six: `SettingsUsersCreate`, `SettingsUsersResetPassword`, `SettingsUsersToggleActive`, `SettingsUsersToggleApprove`, `SettingsUsersToggleApproveRecords`, `SettingsUsersToggleAdmin`. Plan above covers all six found in the source. Confirm with the issue author (or #729 synthesis) whether one was already covered/removed, or the "five" count is simply stale.

2. **Unit vs integration split for DB-touching handlers.** Confirmed above: no mock-DB/sqlmock layer exists in this repo, only `testHandler()`/`testHandlerWithDB()` (no real queries reachable) and live-ArxDev `integration_test.go` (build-tagged, manual run). This plan puts every DB-touching assertion (LoginPost's normal/bootstrap paths, all SettingsUsers* success/validation paths) into `integration_test.go`, and only DB-free paths (guard clauses, requireAdmin's 403, Logout, pure helpers) into unit tests. This matches existing convention but means most of the "highest-value gap" (issue's words) coverage only runs when a human manually sets `ARX_TEST_DSN` against ArxDev — it will NOT show up in default `go test ./...` / `build.bat` coverage numbers. Worth confirming this is acceptable before implementing, since it doesn't move the needle on CI-visible coverage %, only on manually-run integration coverage.

3. **Bootstrap path (`userCount == 0`) is untestable against ArxDev.** ArxDev is seeded with users (per CLAUDE.md), so `LoginPost`'s first-run bootstrap branch and its "missing fields" sub-case can't be exercised via `integration_test.go` without deleting all seeded users (destructive, out of bounds — reseeding is a human action per CLAUDE.md). Recommend either: (a) skip this branch entirely and note it as an accepted gap, or (b) if coverage of the bootstrap branch matters, ask whether a *unit* test is acceptable that calls `h.createUser`/`h.userByUsername` logic directly (not through the full `LoginPost` handler) with `testHandlerWithDB()` — but `userCount`'s query would still hit the fake nop-driver and error out, so this really has no clean test path today. Flag for discussion rather than guessing.

4. **`sanitizeLandingRoute`'s absolute-URL behavior.** Section 2 above shows `"http://evil.com/x"` and bare-host absolute URLs are NOT rejected by `sanitizeLandingRoute` — they're reduced to a path via `RequestURI()` and pass through as `ok=true`. This may be intentional (the function's doc comment says "reduces... to a safe same-origin relative path", implying stripping is the point, not rejecting) or may be a real gap the issue's "open-redirect guard" framing expected to be closed. The plan tests document current behavior rather than assuming it's a bug — but this is worth a second look/sign-off before treating the test suite as "coverage of a security control" rather than "characterization of current (possibly incomplete) behavior."

5. **`copyFormSteps`.** No unit-test path exists (real tx-based DB code, no mock layer). Out of scope for the "pure function" ask in the issue; only its ordering dependency (`OrderedTestIDs`/`parseIDList`) gets covered here. Flag if the issue intended deeper coverage of the copy-steps flow itself — that would need a new integration test against the `/records/.../copy` endpoints, not mentioned explicitly in the issue body.

6. **Exact ParseFS/Funcs invocation per shared template.** Section 4 requires reading the real render call sites for `error.html`, `not_found.html`, `local_dir.html` before writing the parse test (only `login.html`'s call site — `renderLogin` — was confirmed while producing this plan). This is a small, mechanical lookup, not a design ambiguity, but flagged so the implementer doesn't skip it and copy the coreTabDirs pattern blind.
