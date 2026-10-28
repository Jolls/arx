# #380 Friendly database connection errors in Settings

## Findings (current code)
- `arx_go/settings.go:530-582` (`SettingsSave`): `h.connectDB(dsn)` error -> `connErr = err.Error()`; on `connErr != ""` renders `settings/settings.html` via `h.settingsData(w, r, map[string]any{"Error": "Connection failed: " + connErr})`. `settingsData(w, r, extra)` (settings.go:118) merges `extra` into template data.
- Template `arx_go/templates/settings/settings.html:20-24` renders `{{.Error}}` in an `alert alert-danger`. html/template auto-escapes.
- `internal/db/db.go:12-22` `Connect` wraps errors as `"open: %w"` / `"ping: %w"` (wrapping preserved, so `errors.As` works). `Handler.connectDB` defaults to `arxdb.Connect` (handlers.go:142) and is stubbed in tests.
- pgx v5.11.0 (go.mod): ping failure is `*pgconn.ConnectError` (`Unwrap` -> inner). Its text is "failed to connect to `user=U database=D`: <inner>", never the password. Inner is `*pgconn.PgError` (fields `Code`, `Message`) for server rejects; `*net.OpError` / `*net.DNSError` / `net.Error` with `Timeout()` for network (wrapped in unexported `perDialConnectError`, `Unwrap` works); TLS failures are `perDialConnectError{"tls error"}` wrapping tls/x509 errors (`x509.UnknownAuthorityError`, `x509.HostnameError`, `x509.CertificateInvalidError`, `tls.RecordHeaderError`) or a plain `errors.New("server refused TLS connection")` (match by text). `*pgconn.ParseConfigError` (bad DSN) redacts the password best-effort.
- Password leak analysis: DSN built by `Base.BuildDSN` (internal/config/base.go:63); password lives only inside the DSN URL. pgx `ConnectError.Error()` prints user+database only; server messages ("password authentication failed for user X") do not echo the password. Only `ParseConfigError` could echo the DSN (redacted). Defense in depth: classifier also strips known secrets (see below).
- Docs convention: flat `docs/*.md` (e.g. `docs/conventions.md`); CLAUDE.md references docs by path. No docs index to update.
- `settings_save_test.go:65` asserts body contains `"Connection failed:"`.

## Design
Classifier lives in `internal/db` (new `connerr.go`, same package as `Connect`; pgx already imported there). Pure function, no I/O.

- `type ConnectErrorInfo struct { Code, Message, Details string }`
- `func ClassifyConnectError(err error, user string, secrets ...string) ConnectErrorInfo`
- `Details` = `err.Error()` with every non-empty `secrets` value (plus its `url.QueryEscape` / `url.PathEscape` forms) replaced by `***`.
- Classification order (first match wins), using `errors.As` / `errors.Is`:
  1. `*pgconn.PgError` whose message contains `pg_hba.conf` -> `db_firewall_blocked` (SQLSTATE 28000, client IP not allowed).
  2. PgError `28P01` -> `db_auth_failed` ("The server rejected the password for user <user>. Re-enter it below, or check whether it was changed on the server.").
  3. PgError `28000` -> `db_role_unknown`; PgError `3D000` -> `db_database_unknown`.
  4. TLS: `x509.UnknownAuthorityError`, `x509.HostnameError`, `x509.CertificateInvalidError`, `tls.RecordHeaderError`, `*tls.CertificateVerificationError`, or text contains `server refused TLS connection` / `tls error` -> `db_tls_failed`.
  5. `*net.DNSError`; `net.Error` with `Timeout()`; `*net.OpError`; `errors.Is(err, syscall.ECONNREFUSED)` -> `db_host_unreachable`.
  6. Else `db_connect_failed`, message "Could not connect to the database".
- Every message states cause + next step. `user` is interpolated only for `db_auth_failed` / `db_role_unknown`.

Handler (`arx_go/settings.go`): replace `connErr string` with a `*arxdb.ConnectErrorInfo` (nil = no failure). On failure: `info := arxdb.ClassifyConnectError(err, <active user>, connectWith, dsn)`; `log.Printf("settings: database connection failed [%s]: %s", info.Code, info.Details)`; render with `"Error": info.Message, "ConnErrCode": info.Code, "ConnErrDetails": info.Details`. Active user = the one `BuildDSN` uses (`activeUser` in base.go is unexported; see Open question 4).

Template (`settings.html`, inside the existing `{{if .Error}}` alert): after the message, `{{if .ConnErrCode}}` show `<span class="badge text-bg-secondary">{{.ConnErrCode}}</span>`, text "See docs/db-connection-errors.md, section <code>", and `<details class="mt-2"><summary>Details</summary><pre class="mb-0 small">{{.ConnErrDetails}}</pre></details>`. Bootstrap classes only; no inline styles.

Docs `docs/db-connection-errors.md`: short intro (what the code means, how to look it up), then one `##` heading per code, heading text exactly the code (`## db_auth_failed`, `db_role_unknown`, `db_database_unknown`, `db_firewall_blocked`, `db_host_unreachable`, `db_tls_failed`, `db_connect_failed`), each with Symptom / Causes / Fix.

## File changes
1. NEW `internal/db/connerr.go` - struct, `ClassifyConnectError`, redaction helper.
2. NEW `internal/db/connerr_test.go`.
3. EDIT `arx_go/settings.go` (~530-582) - use classifier, log, pass template keys.
4. EDIT `arx_go/templates/settings/settings.html` (20-24) - code badge + collapsed Details.
5. NEW `docs/db-connection-errors.md`.
6. EDIT `arx_go/settings_save_test.go` - update assertion at 65-66, add tests below.
7. If needed (Open question 4): export an active-user accessor in `internal/config/base.go`.
CHANGELOG: handled by manager, not planned here.

## Test plan
### 1) Coverage audit
- `internal/db` tests: only `namedparams_test.go`, `likeescape_test.go`; `Connect` has no unit test (needs live DB). No coverage of error classification.
- `arx_go/settings_save_test.go`: `TestSettingsSave_ConnectFailure` covers failure render (asserts "Connection failed:", h.database()==nil, local.json persisted, no secrets persisted); `TestSettingsSave_AuthenticatedReusesStoredPassword` exercises the failing-connect path with a stub. Nothing covers code/Details rendering or password absence in output/log.
- No test of the settings alert markup.

### 2) Characterization tests (pass before and after)
- Existing `TestSettingsSave_ConnectFailure` non-message assertions (status 200, db nil, local persisted, no secrets persisted) stay unchanged.
- Add `TestSettingsSave_ConnectFailure_PasswordNotRendered`: stub connectDB returns `errors.New("boom")`, post `test_db_password=s3cretPW`; assert body does not contain `s3cretPW`. Passes today; guards regression.

### 3) Red tests (stub `ConnectErrorInfo` + `ClassifyConnectError` returning zero value so they compile)
- `TestClassifyConnectError` (connerr_test.go, table-driven; each err wrapped as `fmt.Errorf("ping: %w", ...)`): PgError 28P01 -> `db_auth_failed` and message contains the user; PgError 28000 + "no pg_hba.conf entry" -> `db_firewall_blocked`; PgError 28000 plain -> `db_role_unknown`; 3D000 -> `db_database_unknown`; `*net.DNSError` -> `db_host_unreachable`; timeout net.Error -> `db_host_unreachable`; `*net.OpError` (conn refused) -> `db_host_unreachable`; `x509.UnknownAuthorityError` and `errors.New("server refused TLS connection")` -> `db_tls_failed`; `errors.New("something odd")` and PgError 53300 -> `db_connect_failed` with message "Could not connect to the database". Fails today: stub returns empty Code/Message.
- `TestClassifyConnectError_DetailsRedactsSecrets`: err text contains password and DSN passed as `secrets`; assert Details non-empty, contains `***`, contains neither secret; Message contains neither. Fails today: Details empty.
- `TestSettingsSave_ConnectFailure_FriendlyMessage`: stub returns wrapped `&pgconn.PgError{Code:"28P01"}`; assert body has the friendly message, `db_auth_failed`, a `<details` element containing raw text, and not the posted password. Fails today: body only has "Connection failed: ...".
- `TestSettingsSave_ConnectFailure_FallbackCode`: stub returns plain `errors.New("connection refused")`; assert `db_connect_failed` and "Could not connect to the database" (assumes Open question 2 = typed-only). Fails today: no code.
- `TestSettingsSave_ConnectFailure_LogsCodeNotSecret`: capture `log` output via `log.SetOutput`; assert contains the code and not the password. Fails today: code not logged.
- `TestDBConnectionErrorsDocHasSectionPerCode` (internal/db): reads `../../docs/db-connection-errors.md`, asserts a `## <code>` heading for every code in an exported list of codes in connerr.go (stub as empty slice/var). Fails today: file missing.
- Edit existing assertion at settings_save_test.go:65-66 from "Connection failed:" to "Could not connect to the database".

### 4) Manual-only
- Wrong password against ArxDev: friendly message, `db_auth_failed`, Details collapsed by default and expandable; terminal log shows code + raw text.
- Wrong hostname, closed port, wrong database name, client IP not in firewall allow-list (28000 vs 28P01 wording for real servers can only be confirmed live).
- TLS failure against a server without TLS.
- Visual check of badge/Details in light and dark theme.

## Open questions
1. Postgres typically returns 28P01 (not 28000) for a nonexistent role under password auth, so `db_role_unknown` may only fire for pg_hba/disabled-role cases. Issue lists "unknown role/database (28000/3D000)" as one class. Keep two codes (`db_role_unknown`, `db_database_unknown`) or one combined code?
2. Should plain-text errors like `errors.New("connection refused")` (no net types) classify as `db_host_unreachable` by string match, or only typed errors (plan assumes typed-only; plain text falls to `db_connect_failed`)?
3. Confirm code names: `db_auth_failed`, `db_role_unknown`, `db_database_unknown`, `db_firewall_blocked`, `db_host_unreachable`, `db_tls_failed`, `db_connect_failed`.
4. `Base.activeUser()` is unexported: OK to add an exported accessor in internal/config, or pass the posted/cfg user another way?
5. UI doc pointer: plain text path `docs/db-connection-errors.md#<code>` (app does not serve docs) or a GitHub URL?

## Resolved decisions
- Seven separate codes: db_auth_failed, db_role_unknown, db_database_unknown, db_firewall_blocked, db_host_unreachable, db_tls_failed, db_connect_failed.
- Typed errors only; plain-text errors fall back to db_connect_failed. `settings_save_test.go:65` assertion becomes "Could not connect to the database".
- Add an exported accessor in `internal/config` for the active DB user.
- UI shows the docs location as plain text `docs/db-connection-errors.md#<code>` (no link).
