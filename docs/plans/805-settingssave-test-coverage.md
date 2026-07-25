# #805 — Test coverage for SettingsSave (DB connection / secrets swap)

Add coverage for `(*Handler).SettingsSave` in `arx_go/settings.go` (starts ~line 356). The
handler builds a DSN from posted prod/test connection fields, calls `arxdb.Connect`, swaps
the live connection on success, persists local config + secrets to disk, re-runs
`CheckSchemaVersion`/`loadCompanyLogo`/`loadPartCategories`, and can force a re-login.

## Key facts driving the design

- **`arxbase.LoadLocal`/`SaveLocal`** (`arxlib/config/local.go`) read/write the *relative* path
  `config/local.json`. A test that does not change cwd will read and **clobber the developer's
  real `arx_go/config/local.json`**. Isolate with `t.Chdir(t.TempDir())` (Go 1.26 in `go.work` —
  `t.Chdir` available). `SaveLocal` does `os.MkdirAll("config", …)` under cwd, so the temp dir works.
- **`arxbase.LoadSecrets`/`SaveSecrets`** (`arxlib/config/secrets.go`) resolve via
  `os.UserConfigDir()` → `<dir>/Arx/local.json`. On Windows `os.UserConfigDir` reads `%AppData%`;
  on Linux `$XDG_CONFIG_HOME` (else `$HOME/.config`). A test that does not override these will
  **write real credentials into the developer's real per-user secrets file**. Isolate with BOTH
  `t.Setenv("AppData", tmp)` and `t.Setenv("XDG_CONFIG_HOME", tmp)` (harmless on the other OS) so
  the suite is cross-platform. Point them at a fresh `t.TempDir()`.
- **`render`** (`handlers.go` ~458) parses from `h.tmplFS`. The `connErr` and "saved, enter
  password" branches render `settings/settings.html`, so the Handler must be built with
  `tmplFS = templatesFS` (the package embed used by `templates_parse_test.go`). `New` also needs
  `cfg.SessionSecret` set (non-empty) so the cookie store / `csrfToken` / `session` work — see
  `testHandler()` in `middleware_test.go`.
- **`settingsData`** guards all DB reads behind `h.database() != nil`, so it renders safely with a
  nil connection (this is the real first-run GET /settings path). No DB needed for the render paths.
- **`arxdb.Connect`** (`arxlib/db/db.go`) is called as a package function inside `SettingsSave`
  (not injected), so it cannot be stubbed without a refactor. `DBEngine()` falls back to
  `"sqlserver"` for unknown engine strings (`base.go` ~65), so an "invalid engine" does NOT yield a
  fast error — a failure path must instead point at an unreachable host.
- **`connectWith`** precedence (`settings.go` ~434): test mode →
  `firstNonEmpty(testPassword, cfg.TestDBPassword, password, cfg.DBPassword)`; prod mode →
  `firstNonEmpty(password, cfg.DBPassword)`. `firstNonEmpty` is already a package function.
  `connectWith` itself is not observable without a real connect (see Open questions).

## File 1 — `arx_go/settings_save_test.go` (new, no build tag; runs in default sweep + build.bat)

Package `main`. Add a local helper that returns an isolated Handler + does the fs isolation:

```
func isolatedSettingsHandler(t *testing.T, cfg *arxbase.Config) *Handler {
    t.Helper()
    t.Chdir(t.TempDir())                 // isolates config/local.json
    t.Setenv("AppData", t.TempDir())     // isolates per-user secrets (Windows)
    t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolates per-user secrets (Linux)
    cfg.SessionSecret = "test-secret"
    return New(nil, nil, cfg, templatesFS, nil)
}
```

Reuse `postForm` — it lives in the `integration`-tagged file, so add a small untagged `postForm`
equivalent here (or a local `postSettings(vals url.Values) *http.Request`) to build the
`application/x-www-form-urlencoded` POST. (Do not move the existing one; keep changes surgical.)

### Test A — `TestSettingsSave_ConnectFailure` (issue case 2)
- Build handler with `cfg.TestMode = true`, `cfg.TestDBName = "ArxDev"`.
- POST test-mode fields pointing at an unreachable local port so `Connect` fails fast with a
  connection-refused (no external network, never ArxProd): `test_mode=1`, `test_db_server=127.0.0.1:1`,
  `test_db_name=ArxDev`, `test_db_user=sa`, `test_db_password=whatever`. `test_mode=1` keeps
  `TestMode` unchanged so the relogin branch is NOT taken.
- Assert:
  - status is 200 (rendered, not redirected); body contains `Connection failed:`.
  - `h.conn.Load() == nil` (no swap; started nil).
  - `config/local.json` on disk exists and reflects the posted overrides (read back via
    `arxbase.LoadLocal` from the temp cwd — e.g. `TestDBServer == "127.0.0.1:1"`).
  - secrets file: `arxbase.LoadSecrets` returns no `TestDBPassword`/`DBPassword` (secrets are only
    written on a *successful* swap, per the handler).

### Test B — `TestSettingsSave_FieldSemantics` (issue case 5)
- Seed a `config/local.json` in the temp cwd BEFORE posting (write via `arxbase.SaveLocal` with
  `TestDBServer:"oldsrv"`, `TestDBName:"ArxDev"`) so there is a pre-existing value to (not) clear.
- Build handler with a `cfg` whose engine cannot succeed-connect: leave `connectWith` empty by
  NOT posting any password and `cfg` having no stored passwords, so `Connect` is skipped entirely
  (`connectWith == ""`) and the handler only exercises the field-apply + persist logic. `TestMode`
  false, `test_mode` not posted → `testModeChanged` false → falls to the final branch;
  `h.database()==nil` → renders "saved, enter password" (status 200).
- POST: `test_db_server=""` (blank), `test_db_name=""` (blank).
- Assert via `arxbase.LoadLocal` on disk:
  - `TestDBServer == ""` — blank-clearable field WAS cleared.
  - `TestDBName == "ArxDev"` — "only if non-empty" field was NOT cleared by the blank submit.

### Test C — `TestSettingsSave_ConnectPasswordPrecedence` (issue case 4)
- Pure table test of `firstNonEmpty` mirroring both branch argument orderings:
  - prod: `firstNonEmpty(posted, storedDB)` → posted wins over stored; stored used when posted blank.
  - test: `firstNonEmpty(testPosted, storedTest, prodPosted, storedDB)` → fresh test wins;
    falls back through to prod password when test values unset.
- This covers the ordering rule. The branch *selection* inside `SettingsSave` (which arg list is
  used) is verified by the integration swap test below (secret actually persisted). See Open questions.

## File 2 — `arx_go/settings_save_integration_test.go` (new, `//go:build integration`)

Package `main`. Runs only under `go test -tags integration` against ArxDev (excluded from the
default sweep and build.bat). Reuse `postForm`, `assert*` from `integration_test.go`. Both tests
MUST also isolate `config/local.json` + the per-user secrets file (same `t.Chdir` + `t.Setenv`
trio as File 1) because a successful swap writes the **real ArxDev password** to the secrets file —
never let that land in the developer's real store.

Add a helper to parse the ArxDev connection out of `ARX_TEST_DSN` (guard: DSN must contain
`arxdev`, else `t.Skip`/`t.Fatal` like `liveHandler`):

```
func arxDevProfile(t *testing.T) (server, user, password, database string) // via url.Parse of ARX_TEST_DSN
```

### Test D — `TestIntegration_SettingsSave_TestModeSwap` (issue case 1)
- Build handler pointed at ArxDev in test mode: set `cfg` (from `arxbase.Load("dev")`), set
  `TestMode=true`, `TestDBServer/TestDBUser/TestDBName` from the parsed ArxDev profile. Connect an
  initial live DB (via `arxdb.Connect`) and `h.conn.Store` it so there is an "old" connection to
  swap out. Set `tmplFS=templatesFS`, `SessionSecret`.
- Safety guard before invoking: `t.Fatal` unless
  `strings.Contains(strings.ToLower(h.cfg.ActiveDBName()), "arxdev")`.
- POST `test_mode=1` (unchanged → no relogin branch) + `test_db_server/test_db_user/test_db_name`
  = ArxDev + `test_db_password` = ArxDev password.
- Assert:
  - redirect `303 SeeOther` to `/` (testMode unchanged, `h.database() != nil`).
  - `h.conn.Load()` is non-nil and a **different pointer** than the pre-swap conn; `db.Ping()` OK.
  - secrets on disk (`arxbase.LoadSecrets`): `TestDBPassword` == the posted ArxDev password
    (proves the success-path secret persist + the test-mode branch selection).
  - `config/local.json` on disk reflects posted test fields.
  - Side effects fired without panic: `h.schemaMismatch` is set to the real ArxDev value (empty if
    schema matches) and `h.partCategories` is populated (loadPartCategories ran).

### Test E — `TestIntegration_SettingsSave_TestModeToggleForcesRelogin` (issue case 3)
- Build handler whose **prod** profile points at ArxDev (`DBServer/DBUser/DBName/DBPassword` =
  parsed ArxDev), `TestMode=false`, initial live conn stored, `tmplFS`/`SessionSecret` set.
- Seed a session carrying `user_id` and `csrf_token` (cookie round-trip like the middleware tests:
  build a request, `h.session(req)`, set values, `sess.Save`, capture cookie, replay on the POST).
- POST `test_mode=1` (flips → `testModeChanged=true`), `test_db_name=ArxDev` (needed so
  `ActiveDBName()` is ArxDev in test mode), test server/user/password left blank → inherit the prod
  ArxDev profile, and the password falls back to the prod ArxDev password via `firstNonEmpty`.
  Connect therefore succeeds against ArxDev → `dbSwapped=true`.
- Safety guard: `t.Fatal` unless `ActiveDBName()` contains `arxdev`.
- Assert:
  - redirect `303 SeeOther` to `/login?notice=…` (decode `Location`, check path `/login` and a
    non-empty `notice` query param).
  - decode the session cookie the handler wrote (replay onto a fresh request, `h.session`): both
    `user_id` and `csrf_token` are gone.

## Verification
- `cd arx_go; .\build.bat` (runs `go test ./...` incl. File 1; File 2 excluded by build tag).
- Integration: `$env:ARX_TEST_DSN="sqlserver://…database=ArxDev…"; go test -tags integration ./arx_go/...`
  (report pass/fail; if it trips on stale seed, ask user to reseed — never reseed yourself).

## Resolved decisions

1. **`connectWith` observability (case 4).** Extract password selection out of `SettingsSave` into
   a small testable helper, e.g.:
   ```go
   func selectConnectPassword(testMode bool, testPosted, storedTest, posted, storedDB string) string {
       if testMode {
           return firstNonEmpty(testPosted, storedTest, posted, storedDB)
       }
       return firstNonEmpty(posted, storedDB)
   }
   ```
   Call it from `SettingsSave` in place of the inline `if h.cfg.TestMode { … } else { … }` block.
   Unit-test `selectConnectPassword` directly (replaces Test C's `firstNonEmpty`-only coverage —
   test both branches and the fallback ordering within each).

2. **Loopback dial in the default sweep (case 2).** Refactor `SettingsSave` to accept an injectable
   connector so the failure-path test needs no dial at all. Add a `Handler` field (or package-level
   var, matching whatever seam is least invasive given `New`'s existing signature) defaulting to
   `arxdb.Connect`, e.g.:
   ```go
   // on Handler:
   connectDB func(engine, dsn string) (*sql.DB, arxdb.Dialect, error) // defaults to arxdb.Connect in New()
   ```
   `SettingsSave` calls `h.connectDB(...)` instead of `arxdb.Connect(...)` directly. Test A stubs
   `h.connectDB` to return a connection-refused-style error synchronously — no network dial.
   Integration tests D/E leave `h.connectDB` at its default (real `arxdb.Connect` against ArxDev).

3. **Handler construction for integration tests D/E**: keep direct field access
   (`h.conn.Store`/`h.cfg` set directly), no new shared constructor — as recommended.

Implementer note: since `SettingsSave` itself now changes (not just new test files), re-check the
"File 1" test bodies above against the actual post-refactor code before writing them — the plan's
Test A/B/C bodies were written against the pre-refactor structure.
