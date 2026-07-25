# 810: End-to-end test coverage for config.Load() precedence chain

## Background (from investigation, no need to re-derive)

`Load()` in `arxlib/config/config.go`:
- Calls `godotenv.Load("../.env")` then `godotenv.Load()` (i.e. `./.env`) as fallback. **`godotenv.Load` never overrides a variable that already exists in the process environment** — it only fills gaps. This is *why* "env vars" effectively outrank ".env file" even though .env loads first: an already-set env var is left untouched, then `os.Getenv` reads it.
- Builds `cfg` from `os.Getenv(...)` / `GetEnv(key, fallback)` calls. `GetEnv` returns `fallback` only when the env var is unset or `""` (`arxlib/config/base.go:133`). `TestDBName` fallback is `"ArxDev"`.
- DATABASE_DSN fallback only applies when `cfg.DBServer == ""` — not relevant to the fields this plan asserts on if DB_SERVER/local.json DBServer are set, but the test setup must not accidentally leave DBServer empty when DSN parsing isn't the thing under test.
- Applies `LoadLocal()` (reads `config/local.json`, path is the **relative literal** `"config/local.json"` — relative to process cwd, not injectable) — overrides only when the local field is non-empty/non-nil (`TestMode *bool`, `Engine *string` use nil-check; strings use `!= ""`; `DebugMode` only overrides when `true`, never forces false).
- Expands `%USERPROFILE%` tokens in folder roots (not in scope for this issue's assertions).
- Calls `LoadSecrets()` (reads `os.UserConfigDir()/Arx/local.json`) and applies `DBPassword`/`TestDBPassword` only — does not touch DBServer/DBName/TestDBName/TestMode. Per-user secrets store is out of scope for the precedence fields under test (issue only asks about DBServer/DBName/TestDBName/TestMode), but the test must still isolate it so a real per-user secrets file is never touched, and so `resolveSessionSecret` doesn't write into it unexpectedly.
- Calls `resolveSessionSecret(secrets)`, which may call `SaveSecrets` — writes to the per-user store when no `SESSION_SECRET` env and no persisted secret. Must isolate this too.
- `migrateLegacy()` is only invoked from inside `LoadLocal()`, and only when `config/local.json` does **not exist** on disk AND its `os.ReadFile` returns `os.IsNotExist`. As long as the test always writes a `config/local.json` file (even an empty `{}`) before calling `Load()`, `migrateLegacy` is never reached — no need to avoid placing `config/local.pm.json`/`config/local.tr.json`, but the plan will avoid creating those files anyway as a belt-and-suspenders measure.

## Existing isolation pattern to reuse

`arxlib/config/session_secret_test.go` already has `isolateStores(t *testing.T)`:
- `os.Chdir` into a fresh `t.TempDir()`, restored via `t.Cleanup`.
- `t.Setenv("AppData", cfgDir)` and `t.Setenv("XDG_CONFIG_HOME", cfgDir)` pointing at a second `t.TempDir()`, isolating `os.UserConfigDir()` (the per-user secrets store) on both Windows and Linux.

Since the new test file is in the same package (`config`), it can call `isolateStores(t)` directly — no need to duplicate it.

## File to add

`arxlib/config/config_test.go` (new file, package `config`).

## Test helper additions in the new file

1. `clearConfigEnv(t *testing.T)` — calls `t.Setenv` with `""` for every env var `Load()` reads: `PM_PORT, PORT, DB_SERVER, DB_ENGINE, DB_NAME, TEST_DB_SERVER, TEST_DB_ENGINE, TEST_DB_NAME, TEST_DB_USER, DB_USER, DOC_CONTROL_ROOT, TEST_MODE, DEBUG_MODE, PO_FOLDER_ROOT, SUPPLIER_FILES_ROOT, IMAGE_ROOT, DATABASE_DSN, SESSION_SECRET`. This prevents the developer's real shell environment from leaking into the test (matches `GetEnv`'s `!= ""` semantics, so blank-via-Setenv is equivalent to unset for every field `Load()` reads).
2. `writeLocalJSON(t *testing.T, contents string)` — `os.MkdirAll("config", 0755)` then `os.WriteFile("config/local.json", []byte(contents), 0600)`, called after `isolateStores(t)` has already `Chdir`'d into the temp dir. Always write this file (even `{}`) in every test in this file, per the migrateLegacy note above.

Each test in this file must call, in order: `isolateStores(t)`, then `clearConfigEnv(t)`, then set only the specific env vars / .env file / local.json contents relevant to that test, then call `writeLocalJSON`, then call `config.Load("test-version")`.

## Tests to add

### `TestLoad_Defaults`
Purpose: baseline with everything isolated/empty — confirms code defaults.
- `isolateStores(t)`, `clearConfigEnv(t)`, `writeLocalJSON(t, "{}")`.
- Call `Load("v0")`.
- Assert `cfg.TestDBName == "ArxDev"` (code fallback).
- Assert `cfg.TestMode == false` (zero value; no local.json `TestMode` pointer set).
- Assert `cfg.DBServer == ""` and `cfg.DBName == ""`.

### `TestLoad_EnvVarsApply`
Purpose: env vars alone (no .env file, no local.json values) reach the Config.
- `isolateStores(t)`, `clearConfigEnv(t)`, `writeLocalJSON(t, "{}")`.
- `t.Setenv("DB_SERVER", "envserver")`, `t.Setenv("DB_NAME", "envdb")`, `t.Setenv("TEST_DB_NAME", "EnvTestDB")`, `t.Setenv("TEST_MODE", "true")`.
- Call `Load("v0")`.
- Assert `cfg.DBServer == "envserver"`, `cfg.DBName == "envdb"`, `cfg.TestDBName == "EnvTestDB"` (env overrides the `"ArxDev"` code default), `cfg.TestMode == true`.

### `TestLoad_EnvVarWinsOverDotEnvFile`
Purpose: directly test the issue's specific ask — verify a real env var beats a value from `.env` for the same key, and confirms which source actually wins in the code (godotenv-does-not-override-existing-env-var behavior described above).
- `isolateStores(t)`, `clearConfigEnv(t)`.
- Write a `.env` file in the temp cwd (`os.WriteFile(".env", []byte("DB_SERVER=dotenvserver\nDB_NAME=dotenvdb\n"), 0600)`).
- `t.Setenv("DB_SERVER", "realenvserver")` (DB_NAME left unset so only .env supplies it).
- `writeLocalJSON(t, "{}")`.
- Call `Load("v0")`.
- Assert `cfg.DBServer == "realenvserver"` (env var beat .env for the same key).
- Assert `cfg.DBName == "dotenvdb"` (.env alone, no env var, still applies — proves .env is read at all).
- This matches CLAUDE.md's documented order (.env < env vars). If it does not hold — i.e. `cfg.DBServer` comes back `"dotenvserver"` — do not "fix" the assertion to match observed behavior; that is a real precedence bug or doc error and must be flagged back to the reporter instead of silently accepted by the test.

### `TestLoad_LocalJSONOverridesEnvAndDotEnv`
Purpose: local.json (highest of the three) overrides both env var and .env for the same fields, per field type (string, `TestDBName` with code default, `*bool` for TestMode).
- `isolateStores(t)`, `clearConfigEnv(t)`.
- Write `.env` with `DB_SERVER=dotenvserver`.
- `t.Setenv("DB_SERVER", "realenvserver")`, `t.Setenv("DB_NAME", "envdb")`, `t.Setenv("TEST_DB_NAME", "EnvTestDB")`, `t.Setenv("TEST_MODE", "true")`.
- `writeLocalJSON(t, `{"db_server":"localserver","db_name":"localdb","test_db_name":"LocalTestDB","test_mode":false}`)`.
- Call `Load("v0")`.
- Assert `cfg.DBServer == "localserver"`, `cfg.DBName == "localdb"`, `cfg.TestDBName == "LocalTestDB"` (local.json overrides even the env-supplied value, which itself overrode the code default).
- Assert `cfg.TestMode == false` — this specifically exercises the `*bool` nil-vs-explicit-false semantics: TEST_MODE env sets `cfg.TestMode = true`, and local.json's explicit `"test_mode": false` (non-nil pointer, dereferenced value false) must still override it back to `false`, per `config.go`'s `if local.TestMode != nil { cfg.TestMode = *local.TestMode }`.

### `TestLoad_LocalJSONAbsentFieldsDoNotOverride`
Purpose: confirms the "only if non-empty/non-nil" partial-override semantics — local.json present but a given field blank/absent must NOT clobber an env-var-supplied value with a zero value.
- `isolateStores(t)`, `clearConfigEnv(t)`.
- `t.Setenv("DB_SERVER", "envserver")`, `t.Setenv("TEST_MODE", "true")`.
- `writeLocalJSON(t, `{"db_name":"localonlydb"}`)` — `db_server` and `test_mode` absent from the JSON.
- Call `Load("v0")`.
- Assert `cfg.DBServer == "envserver"` (local.json omitted this field, so env value survives).
- Assert `cfg.DBName == "localonlydb"` (local.json did set this one).
- Assert `cfg.TestMode == true` (local.json's `TestMode` is nil since the key is absent — env-set `true` must survive, proving the nil-pointer path does not force `false`).

## Verification
- `cd arx_go; .\build.bat` runs `go test ./...` across the workspace including `arxlib/config`, but since `arxlib` is a separate module in the go.work, also run directly: from `arxlib/`, `go test ./config/...`.
- All 5 new tests must pass; no existing test in `arxlib/config` should regress (run the full `arxlib/config` package, not just the new file, to confirm `isolateStores` reuse didn't collide with anything).

## Open questions
None — investigation confirmed an injectable-enough path (cwd-relative `config/local.json`, `os.UserConfigDir()`-relative secrets, both already isolated by the existing `isolateStores` helper) and confirmed the actual precedence order in code matches CLAUDE.md's documented order. If `TestLoad_EnvVarWinsOverDotEnvFile` fails when implemented, that is a genuine discrepancy to report, not a planning gap.
