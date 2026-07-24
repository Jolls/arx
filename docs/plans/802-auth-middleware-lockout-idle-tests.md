# Plan: Auth middleware composition + login lockout/idle-timeout tests (#802)

## Findings (verified current code)

- `buildRouter(h *Handler) *chi.Mux` — `arx_go/main.go:109-368`. Middleware order: `middleware.Logger`, `middleware.Recoverer`, `h.profileRequest`, `h.RequireCsrfOnPost` (global), then a `r.Group` with `h.RequireAuth` wrapping all app routes (main.go:138-363). Always-accessible (outside the auth group): `GET/POST /login`, `POST /logout`, `GET /whats-new`, `GET /static/*`; and `GET/POST /settings`, `GET /api/browse-folder`, `GET /api/browse-file` gated by `h.RequireAuthOnceConnected` (main.go:121-135). `r.NotFound(h.NotFound)` at main.go:365.
- `RequireAuth` — `arx_go/handlers.go:317-334`. Branches: db==nil → 303 `/settings`; schemaMismatch!="" → 303 `/login`; `withUser` returns nil → 303 `/login`; else `next.ServeHTTP(w, r2)`.
- `withUser` — `arx_go/auth.go:141-167`. Idle branch: if `last_activity` present and stale by more than `sessionIdleTimeout` → deletes `user_id`, saves, returns nil. Then `cachedUserByID`; then re-stamps `last_activity` only if stale by > 5min (`activityStampGranularity`).
- `sessionIdleTimeout = 7*24h` (auth.go:136). `activityStampGranularity = 5*time.Minute` (auth.go:161).
- Login throttle (auth.go:169-222): `loginMaxFails = 10`, `loginLockout = 1*time.Minute`, `maxTrackedLogins = 1024`. `loginBlocked` true when `fails >= 10 && time.Now().Before(lockCap)`. `noteLoginFail` increments; on a fully-elapsed prior lockout it resets the counter; sets `lockCap` once `fails >= 10`. `noteLoginOK` deletes the entry. Keyed by lowercased username, all in-memory, no DB.
- `LoginPost` (auth.go:253-310) calls `loginBlocked`/`noteLoginFail`/`noteLoginOK` around a bcrypt compare.
- Existing non-DB middleware test harness — `arx_go/middleware_test.go`: `testHandler()` (nil db), `testHandlerWithDB()` (nop-driver, non-nil `*sql.DB` never dialed), `sentinel(&reached)`, cookie-seeding pattern (`h.session`+`sess.Values`+`sess.Save`, replay `seedRec.Result().Cookies()`), plus `h.userCache[id]` seeding so `withUser`/`cachedUserByID` resolve without dialing the DB.
- Key insight: for an anonymous request, auth short-circuits before any DB dial. For a logged-in request, seeding `h.userCache[id]` (fresh TTL) makes `withUser` resolve without a DB round trip. So all gaps are coverable in a fast non-integration test using `testHandlerWithDB()` — no live ArxDev needed.
- Seed users (SQL/seed_test_data.sql:151-155): `admin`/`admin` (id 8001, is_admin), `tester`/`tester` (id 8002).

## Resolved decisions

1. **Placement**: non-integration only, in `arx_go/middleware_test.go` (tests 1-7). No live-DB end-to-end lockout test — the nop-driver harness already exercises every named branch without needing ArxDev.
2. **POST routes in the buildRouter walk test**: cover POSTs too — seed a valid CSRF cookie/token so the global CSRF middleware doesn't 403 before auth runs, then assert 303→`/login` same as GET routes.
3. **Re-stamp granularity assertion**: yes, assert it — two sub-cases in test 5 (fresh `last_activity` → no re-stamp cookie written; stale by >5min → re-stamp cookie written).
4. No live end-to-end CSRF-scraping test needed (moot given decision 1).
5. **Wildcard/param substitution**: numeric-looking params → `1`, string params/wildcards → `x`. Concrete values don't matter since RequireAuth short-circuits before the handler for anonymous requests.

## File changes

All additions to `arx_go/middleware_test.go` (existing file, no build tag). No production code changes. No new fixtures.

### 1. `TestBuildRouter_AllAppRoutesRequireAuth`
- `h := testHandlerWithDB()`, `r := buildRouter(h)`.
- Explicit always-accessible allowlist (method+path): `GET /login`, `POST /login`, `POST /logout`, `GET /whats-new`, any path under `/static/`. These run their real handler under the nop driver and would error for unrelated reasons — excluded, auth coverage N/A for them.
- `chi.Walk(r, ...)` to enumerate every registered route.
- For each non-allowlisted route, substitute path params (numeric-looking → `1`, else → `x`, wildcards → `x`) to form a concrete path.
  - GET/other non-POST: `httptest.NewRequest(method, path, nil)`, no cookies; assert `303` + `Location == "/login"`.
  - POST: obtain a valid CSRF cookie+token via `h.csrfToken(seedRec, seedReq)`, replay cookie, send `csrf_token=<token>` in body; assert `303` + `Location "/login"`.
- Assert the walk visited `>= 150` routes as a sanity guard.

### 2. `TestRequireAuth_RedirectsWhenAnonymous`
- `h := testHandlerWithDB()`, schemaMismatch empty, no session cookie.
- `h.RequireAuth(sentinel(&reached)).ServeHTTP(rec, GET /part/1)`.
- Assert `reached == false`, status `303`, Location `/login`.

### 3. `TestRequireAuth_ProceedsWhenLoggedIn`
- `h := testHandlerWithDB()`; seed `h.userCache[7]` (fresh TTL).
- Build a signed session cookie with `user_id=7` and `last_activity = time.Now().Unix()`.
- Use a sentinel variant that reads `h.currentUser(r)` off request context and records the ID.
- Assert `reached == true`, status `200`, context user ID == 7.

### 4. `TestWithUser_IdleTimeoutLogsOut`
- `h := testHandlerWithDB()`; seed `h.userCache[7]` fresh.
- Session with `user_id=7`, `last_activity = time.Now().Add(-(sessionIdleTimeout + time.Hour)).Unix()`.
- `r2, u := h.withUser(rec, req)`; assert `u == nil`, `r2 == req`.
- Decode the response `Set-Cookie` back through `h.store`/`h.session` and assert `user_id` was deleted.

### 5. `TestWithUser_ActiveSessionProceeds`
- Seed `h.userCache[7]` fresh.
- Sub-case A: session `last_activity = now-10s` (within granularity) → `u != nil`, `u.ID==7`, no re-stamp cookie written (`rec.Result().Cookies()` empty).
- Sub-case B: session `last_activity = now - 6*time.Minute` (past 5-min granularity) → `u != nil`, a new session cookie IS written (re-stamp), decoded `last_activity` advanced.

### 6. `TestLoginThrottle_LockoutLifecycle`
- `h := testHandler()` (no DB needed).
- `loginBlocked("bob") == false` initially.
- `noteLoginFail("bob")` × 9 → still false; 10th → `loginBlocked("bob") == true`.
- `noteLoginOK("bob")` → `loginBlocked("bob") == false`, entry removed (`len(h.loginAttempts)==0`).
- Use an already-lowercased key (functions don't lowercase themselves — `LoginPost` does that before calling).

### 7. `TestLoginThrottle_LockoutExpires`
- Drive `noteLoginFail` to 10 fails (blocked).
- Directly mutate `h.loginAttempts["bob"].lockCap = time.Now().Add(-time.Second)` (under `h.loginMu.Lock()`).
- Assert `loginBlocked("bob") == false`.
- `noteLoginFail("bob")` once → assert `h.loginAttempts["bob"].fails == 1` (counter restarted, not 11).

## Verification

`cd arx_go; go test ./...` (all 7 tests run under default build, no ArxDev needed).

This is a test-only change — no production code modified.

## Critical files

- arx_go/middleware_test.go (additions)
- arx_go/main.go (read only)
- arx_go/auth.go (read only)
- arx_go/handlers.go (read only)
