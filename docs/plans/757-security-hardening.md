# Plan: #757 Security hardening batch (settings race, unauth browse, DSN sslmode, session/CSRF)

Epic #719. Four independent findings; each can ship as its own commit on one branch, one combined PR.
Line numbers below verified against the working tree at plan time (they may drift as edits land).

> Scope note: the parts with a genuinely undecided design choice are collected in
> **Open questions** at the end. Everything outside that section is a decided fix.

---

## Finding 1 — Data race on the DB swap in `SettingsSave`

### Summary
`h` is a single process-global `*Handler` (declared `var h *Handler` in `main.go:24`) shared by
every concurrent chi request goroutine. `SettingsSave` swaps `h.db` and `h.dialect` in place and
calls `oldDB.Close()` with no synchronization, while other in-flight request goroutines read
`h.db`/`h.dialect` directly. This is a true data race under `-race`:

- `h.dialect` is an interface value (2 words); a torn read during the swap can crash, not merely
  return a stale value.
- `oldDB.Close()` runs while concurrent queries may still hold `h.db == oldDB`. (`database/sql`
  tolerates `Close` concurrent with active queries — they error rather than corrupt — so the
  memory-unsafe part is the pointer read/write, and the *behavioral* part is in-flight queries on
  the just-closed handle erroring out.)

### Current code
`arx_go/settings.go:440-467` (inside `SettingsSave`):
```go
var connErr string
dbSwapped := false
if connectWith != "" {
    dsn := h.cfg.BuildDSN(connectWith)
    newDB, newDialect, err := arxdb.Connect(h.cfg.DBEngine(), dsn)
    if err != nil {
        connErr = err.Error()
    } else {
        oldDB := h.db
        h.db = newDB
        h.dialect = newDialect
        dbSwapped = true
        ...
        if oldDB != nil {
            oldDB.Close()
        }
    }
}
```
`h.db`/`h.dialect` are declared as plain fields (`arx_go/handlers.go:29-30`) and read directly in
~40 (`h.db`) and ~124 (`h.dialect`) sites across 22 files, plus the four DB wrappers
(`queryContext`/`queryRowContext`/`execContext`/`beginTx`, `handlers.go:128-179`) and the
`h.db == nil` connection checks scattered through handlers.

### Decided fix — atomic snapshot of the swapped pair
This matches the issue's own suggestion ("atomic.Pointer wrapping the db/dialect fields") and gives
a lock-free single-load read on the hot path. Bulk read-site renames are done with the Edit tool's
`replace_all` per file (the CLAUDE.md-sanctioned mechanism for mechanical renames; preserves line
endings).

1. **New immutable snapshot type + field** (`arx_go/handlers.go`, struct at line 28):

   Before:
   ```go
   type Handler struct {
       db             *sql.DB
       dialect        arxdb.Dialect
       cfg            *arxbase.Config
   ```
   After:
   ```go
   // dbConn is an immutable snapshot of the active connection + its dialect,
   // swapped atomically so concurrent requests never see a torn pointer or use
   // a handle mid-close.
   type dbConn struct {
       db      *sql.DB
       dialect arxdb.Dialect
   }

   type Handler struct {
       conn           atomic.Pointer[dbConn] // holds the live *dbConn; nil-payload => not connected
       cfg            *arxbase.Config
   ```
   (Add `"sync/atomic"` to the import block; drop the now-unused `db`/`dialect` fields.)

2. **Accessors** (new, in `handlers.go` near the wrappers):
   ```go
   // database returns the live *sql.DB, or nil when not connected.
   func (h *Handler) database() *sql.DB {
       if c := h.conn.Load(); c != nil {
           return c.db
       }
       return nil
   }
   // dia returns the live dialect, or a default SQL Server dialect when not connected
   // (mirrors New()'s nil-dialect fallback so template/query building never nil-panics).
   func (h *Handler) dia() arxdb.Dialect {
       if c := h.conn.Load(); c != nil && c.dialect != nil {
           return c.dialect
       }
       return arxdb.NewSQLServerDialect()
   }
   ```

3. **`New()`** (`handlers.go:46-65`): replace the `db: db, dialect: dialect,` struct literal fields
   with a post-construct `h.conn.Store(&dbConn{db: db, dialect: dialect})` (keep the existing
   `if dialect == nil { dialect = NewSQLServerDialect() }` normalization before the Store).

4. **Mechanical read-site renames** (per file, `replace_all`):
   - `h.db` → `h.database()` for every *read* (the `== nil` / `!= nil` checks, `h.db.Close()`,
     `DB()` accessor at `handlers.go:268`, the four wrapper bodies at `handlers.go:131/137/143/174`).
   - `h.dialect` → `h.dia()` for all 124 read sites (query builders calling `.BoolLiteral`,
     `.TopClause`, `.Rewrite`, `.ToggleBoolExpr`, `.LimitClause`, `.UpsertAppConfig`, etc.).
   - `CloseDB` (`handlers.go:271-275`): load once, close if non-nil.
   - Exclude the write sites in `SettingsSave` and `New` from the bulk rename (handled explicitly).

5. **`SettingsSave` swap** (`settings.go:448-465`) becomes:
   ```go
   old := h.conn.Load()
   h.conn.Store(&dbConn{db: newDB, dialect: newDialect})
   dbSwapped = true
   ...
   if old != nil && old.db != nil {
       old.db.Close()
   }
   ```
   Note: `CheckSchemaVersion`, `loadCompanyLogo`, `loadPartCategories` called between the Store and
   Close now read through `h.database()`/`h.dia()` and correctly see the new connection.

### Verify
- `go build ./... && go vet ./...` inside `arx_go`.
- `go test -race ./arx_go/...` — add a regression test (see below) that hammers `SettingsSave`'s
  swap concurrently with reads; must be clean under `-race`.
- Suggested regression test: a `-race` unit test that spins N goroutines calling `h.database()`/
  `h.dia()` in a loop while another goroutine repeatedly `h.conn.Store(&dbConn{...})`s and closes the
  old handle, asserting no race and no panic. (Offer; write only if user agrees.)

> The wider mutation race on `h.cfg.*`, `h.companyLogo`, `h.partCategories`, `h.schemaMismatch`
> (all written by `SettingsSave`/partial-save handlers and read on the hot render path) shares this
> root cause but is **not** covered by the atomic-pair fix — see Open question A.

---

## Finding 2 — Unauthenticated `browse-folder` / `browse-file` endpoints

### Summary
`GET /api/browse-folder` and `GET /api/browse-file` are registered in the "always accessible" block,
outside any auth middleware. Each spawns a native Windows picker via
`folderpick.BrowseFolderContext` / `BrowseFileContext` (PowerShell + a modal desktop dialog, up to
5 min). Any local process can hit these unauthenticated and pop blocking dialogs on the user's
desktop — a local DoS/nuisance.

### Current code
`arx_go/main.go:120-131`:
```go
// Always accessible — no DB connection required.
r.Get("/login", h.LoginGet)
...
r.With(h.RequireAuthOnceConnected).Post("/settings", h.SettingsSave)
r.Get("/whats-new", h.WhatsNew)
r.Get("/api/browse-folder", h.APIBrowseFolder)
r.Get("/api/browse-file", h.APIBrowseFile)
```
Handlers: `arx_go/api.go:190` (`APIBrowseFolder`) and `arx_go/api.go:196` (`APIBrowseFile`).

Callers (frontend): `browse-file` is fetched only from `templates/parts/part_attachments.html:293`
(a page already behind `RequireAuth`); `browse-folder` only from
`templates/settings/settings.html:1035` (the Settings page, whose GET is reachable unauthenticated
during first-run setup).

### Decided fix — gate with `RequireAuthOnceConnected`
Do **not** move them into the `RequireAuth` group. `RequireAuth` redirects to `/settings` whenever
`h.db == nil`, which would break the folder-browse buttons during first-run path configuration
(before any DB/login exists). `RequireAuthOnceConnected` (`handlers.go:305`) is the existing
middleware built for exactly this "reachable on first run, authenticated once connected" profile
(same shape already applied to `POST /settings`, #748):

- `h.db == nil` (first run) → allowed, so first-run folder browsing still works.
- connected + anonymous → redirect to `/login` (the `fetch()` gets login HTML, fails to parse JSON,
  no dialog spawns).
- connected + logged in → dialog opens as today.

This closes the persistent-unauth-DoS on a running, connected instance (the real threat) while
preserving legitimate first-run usage.

Before (`main.go:130-131`):
```go
r.Get("/api/browse-folder", h.APIBrowseFolder)
r.Get("/api/browse-file", h.APIBrowseFile)
```
After:
```go
r.With(h.RequireAuthOnceConnected).Get("/api/browse-folder", h.APIBrowseFolder)
r.With(h.RequireAuthOnceConnected).Get("/api/browse-file", h.APIBrowseFile)
```

### Verify
- `go build`/`vet`.
- Unit test (mirrors `TestRequireAuthOnceConnected_*` in `middleware_test.go`): connected+anonymous
  GET to each browse route → 303 to `/login`, handler not reached. (Offer.)
- Manual: first-run (no DB) Settings folder-browse still opens; after login it still opens; hitting
  the route while logged out on a connected instance redirects instead of popping a dialog.

> See Open question B — whether the stricter `RequireAuth` (block first-run browsing too) is
> preferred over `RequireAuthOnceConnected`.

---

## Finding 3 — Postgres DSN hardcodes `sslmode=prefer`

### Summary
`BuildDSN` builds the Postgres URL with `sslmode=prefer`, which silently falls back to plaintext if
the server doesn't advertise TLS. The SQL Server path uses `encrypt=true`. With #666 moving the DB
onto a separate StartOS box, a plaintext fallback would send credentials/data unencrypted over the
network.

### Current code
`arxlib/config/base.go:77-89`:
```go
if b.DBEngine() == "postgres" {
    // pgx/stdlib accepts a postgres:// URL. sslmode=prefer negotiates TLS
    // when the server offers it and falls back to plaintext otherwise, so
    // the same DSN works against managed and local Postgres.
    u := &url.URL{
        Scheme:   "postgres",
        User:     url.UserPassword(b.activeUser(), password),
        Host:     b.activeServer(),
        Path:     "/" + b.ActiveDBName(),
        RawQuery: url.Values{"sslmode": {"prefer"}}.Encode(),
    }
    return u.String()
}
```

### Decided fix — hardcode `sslmode=require`
Mirror the SQL Server path's non-configurable `encrypt=true` stance: hardcode `require` (encrypt the
connection, refuse plaintext fallback). No new config field is added now — that keeps the change
surgical and consistent with `encrypt=true`, which is likewise not toggleable.

Before:
```go
    // pgx/stdlib accepts a postgres:// URL. sslmode=prefer negotiates TLS
    // when the server offers it and falls back to plaintext otherwise, so
    // the same DSN works against managed and local Postgres.
    u := &url.URL{
        Scheme:   "postgres",
        User:     url.UserPassword(b.activeUser(), password),
        Host:     b.activeServer(),
        Path:     "/" + b.ActiveDBName(),
        RawQuery: url.Values{"sslmode": {"prefer"}}.Encode(),
    }
```
After:
```go
    // pgx/stdlib accepts a postgres:// URL. sslmode=require forces TLS and
    // refuses a plaintext fallback, matching the SQL Server path's encrypt=true.
    // (require encrypts but does not verify the server cert — see #757 notes;
    // #666 puts the DB on a separate box, so plaintext must never be silently used.)
    u := &url.URL{
        Scheme:   "postgres",
        User:     url.UserPassword(b.activeUser(), password),
        Host:     b.activeServer(),
        Path:     "/" + b.ActiveDBName(),
        RawQuery: url.Values{"sslmode": {"require"}}.Encode(),
    }
```

### Verify
- `go test ./arxlib/config/...` — update any `base_test.go` assertion that pins the DSN string to
  `sslmode=prefer` (grep the test file; adjust expected value to `require`).
- `go build`/`vet`.

> `require` vs `verify-full`, and whether a `db_sslmode` override is needed for local non-TLS
> Postgres dev, are in Open question C.

---

## Finding 4 — Session / CSRF low items

Four sub-items. All in `arx_go/handlers.go` (CSRF + session cookie) and `arx_go/auth.go`
(login/logout).

### 4a. Constant-time CSRF compare
**Current** (`handlers.go:488-492`):
```go
func (h *Handler) verifyCsrf(r *http.Request) bool {
    sess := h.session(r)
    token, _ := sess.Values["csrf_token"].(string)
    return token != "" && r.FormValue("csrf_token") == token
}
```
`==` on the token is a non-constant-time string compare.

**Fix** — use `crypto/subtle` (add to imports):
```go
func (h *Handler) verifyCsrf(r *http.Request) bool {
    sess := h.session(r)
    token, _ := sess.Values["csrf_token"].(string)
    got := r.FormValue("csrf_token")
    if token == "" || got == "" {
        return false
    }
    return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}
```
(`ConstantTimeCompare` returns 0 immediately on length mismatch, which is fine — the length of a
CSRF token is not secret.)

### 4b. Rotate CSRF token on login and logout
Currently the CSRF token lives in the session and persists across a login/logout boundary
(`csrfToken`, `handlers.go:473-486`, only mints one when absent). Rotate it so a token minted in a
pre-auth (e.g. login-page) session can't be reused after the privilege change.

- **Login** — in `saveSessionUser` (`auth.go:253-257`), delete the old CSRF token so the next
  `csrfToken()` call mints a fresh one:
  ```go
  func (h *Handler) saveSessionUser(w http.ResponseWriter, r *http.Request, u *User) {
      sess := h.session(r)
      sess.Values["user_id"] = u.ID
      delete(sess.Values, "csrf_token") // rotate CSRF across the privilege change (#757)
      sess.Save(r, w)
  }
  ```
- **Logout** — in `Logout` (`auth.go:246-251`), also drop the CSRF token (and the nav-context
  values already keyed to the session) so a fully fresh token is issued to the next user:
  ```go
  func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
      sess := h.session(r)
      delete(sess.Values, "user_id")
      delete(sess.Values, "csrf_token")
      sess.Save(r, w)
      http.Redirect(w, r, "/login", http.StatusSeeOther)
  }
  ```
Note the existing test-mode DB-switch path (`settings.go:488-490`) already `delete`s `user_id` and
`Save`s; add `delete(sess.Values, "csrf_token")` there too for the same reason.

> gorilla `CookieStore` is stateless (signed cookie, no server-side session ID), so there is no
> server-side session ID to regenerate; rotating the in-cookie CSRF token + clearing `user_id` is the
> available equivalent of "rotate session on login/logout." See Open question D on session-fixation.

### 4c. Basic login throttling
No throttling today — `LoginPost` (`auth.go:214-224`) compares the bcrypt hash on every attempt with
no rate limit. The app binds `127.0.0.1` only (`main.go:58`), so **every** request's RemoteAddr is
loopback — per-IP keying is meaningless here; key on **username**.

Mirror the existing `userCache` pattern (a `map` guarded by a `sync.Mutex`/`RWMutex`, already used in
`handlers.go`/`auth.go`). Add to `Handler`:
```go
loginMu       sync.Mutex
loginAttempts map[string]*loginAttempt // keyed by lowercased username
```
```go
type loginAttempt struct {
    fails   int
    lockCap time.Time // requests before this are rejected
}
const loginMaxFails = 5
const loginLockout  = 1 * time.Minute
```
Helpers (constant-time-independent; this is throttling, not comparison):
- `func (h *Handler) loginBlocked(user string) bool` — under `loginMu`, true if an entry exists,
  `fails >= loginMaxFails`, and `time.Now().Before(lockCap)`.
- `func (h *Handler) noteLoginFail(user string)` — increment `fails`; when it reaches
  `loginMaxFails`, set `lockCap = now + loginLockout`.
- `func (h *Handler) noteLoginOK(user string)` — delete the entry.

Wire into `LoginPost` (normal-login branch, `auth.go:213-224`): before the bcrypt compare, if
`h.loginBlocked(username)` → redirect to `/login?error=too+many+attempts,+try+again+shortly`
(HTTP 303). On failed compare → `noteLoginFail`; on success (before `saveSessionUser`) →
`noteLoginOK`. Initialize the map in `New()` alongside `userCache`.

> Per-username keying enables a nuisance lockout of a known username by a co-located local process;
> given the localhost-only, single-shop threat model a short fixed cooldown (not an escalating hard
> lock) is the intended tradeoff. Exact `loginMaxFails`/`loginLockout` values are tunable — see
> Open question E.

### 4d. Idle timeout distinct from the 30-day absolute lifetime
**Current** (`handlers.go:51-56`): the cookie store sets `MaxAge: 86400 * 30` (30-day absolute
lifetime for both the cookie and the signed-cookie validity). There is no idle timeout — an
unattended session stays valid for the full 30 days.

**Fix** — track last activity in the session and expire on inactivity in the auth middleware:
- On each authenticated request, in `withUser` (`auth.go:133-144`) after a successful user resolve,
  stamp `sess.Values["last_activity"] = time.Now().Unix()` and `sess.Save`. Before accepting, if a
  prior `last_activity` exists and `now - last_activity > idleTimeout`, treat as logged out (delete
  `user_id`, save, return no user) so `RequireAuth` redirects to `/login`.
- Add `const sessionIdleTimeout = 8 * time.Hour` (concrete default; see Open question E).

Because `withUser` is the single choke point used by both `RequireAuth` and
`RequireAuthOnceConnected`, putting the idle check there covers every gated route without touching
each middleware.

Precise shape inside `withUser` (replacing the trailing return at `auth.go:143`):
```go
now := time.Now().Unix()
if last, ok := sess.Values["last_activity"].(int64); ok && now-last > int64(sessionIdleTimeout/time.Second) {
    delete(sess.Values, "user_id")
    sess.Save(r, w) // NOTE: withUser currently has no ResponseWriter — see below
    return r, nil
}
sess.Values["last_activity"] = now
sess.Save(r, w)
return r.WithContext(context.WithValue(r.Context(), ctxUserKey, u)), u
```
**Signature change required:** `withUser(r *http.Request)` has no `http.ResponseWriter`, so it can't
`Save` a cookie. It's called in `RequireAuth`/`RequireAuthOnceConnected` (which have `w`),
`LoginGet` (has `w`), and `Settings` (has `w`). Change the signature to
`withUser(w http.ResponseWriter, r *http.Request)` and thread `w` through the ~4 call sites. This is
a small, contained change but does touch each caller — flagged so the implementer expects it.

### Verify (Finding 4)
- `go build`/`vet`, existing `middleware_test.go` CSRF tests still pass (constant-time change is
  behavior-preserving for valid/invalid tokens).
- New unit tests (offer): CSRF token differs before vs after login; login blocked after
  `loginMaxFails` within the window then allowed after cooldown; `withUser` rejects a session whose
  `last_activity` is older than `sessionIdleTimeout`.

---

## Open questions

**A. Scope of the wider mutation race (cfg / companyLogo / partCategories / schemaMismatch).**
The atomic-pair fix (Finding 1) makes `h.db`/`h.dialect` race-free, but `SettingsSave` also mutates
`h.cfg.*` fields (DBServer/DBName/TestMode/roots/DebugMode/…), and `SettingsSave`/partial-save
handlers plus startup mutate `h.companyLogo`, `h.partCategories`, `h.schemaMismatch` — all read by
concurrent requests on the render/hot path, all real `-race` findings. A complete fix is a
cross-cutting change (e.g. swap the whole `*Config` atomically + snapshot the cached fields, or a
coarse `RWMutex` around all mutable Handler state). Options:
  (1) Fix only db/dialect now (issue's emphasized "real under -race" item); file a follow-up for the
      rest. (2) Extend the atomic snapshot to also carry cfg + cached fields. (3) Coarse RWMutex over
  all mutable state. **Which scope for this PR?** (This is a schema-of-shared-state / architecture
  decision — recommend Opus review either way per CLAUDE.md.)

**B. Browse-endpoint middleware: `RequireAuthOnceConnected` (recommended) vs `RequireAuth`.**
The plan uses `RequireAuthOnceConnected` to preserve first-run folder browsing. If first-run folder
browsing is not actually needed (DB credentials are text fields; path roots could be configured only
after login), the stricter `RequireAuth` would also block the first-run window. Confirm whether
first-run path configuration via the browse button must keep working.

**C. Postgres TLS: `require` (recommended) vs `verify-full`, and whether a `db_sslmode` override is
needed.** `require` encrypts but does not verify the server certificate (no MITM protection);
`verify-full` verifies but needs a CA/root cert configured, which a self-signed StartOS Postgres
(#666) won't have out of the box. Also, if a local non-TLS Postgres dev instance is ever needed,
hardcoded `require` blocks it and a `db_sslmode` config field (Base + `config.go` env/local.json
parsing, default `require`) would be required. Decide: hardcode `require`, hardcode `verify-full`,
or add the config toggle.

**D. Session fixation.** With gorilla `CookieStore` (stateless signed cookie) there is no server-side
session identifier to regenerate on login, so 4b rotates the CSRF token + `user_id` only. If stronger
fixation resistance is wanted, that implies moving to a server-side session store (`FilesystemStore`
or DB-backed) — out of scope for this batch unless requested. Confirm the cookie-store approach is
acceptable.

**E. Concrete tunables.** Plan proposes `loginMaxFails = 5`, `loginLockout = 1m`,
`sessionIdleTimeout = 8h`. Confirm or adjust these values for the shop's workflow (e.g. an 8h idle
window roughly matches a work shift; a 1-minute lockout is a speed-bump, not a hard lock).
