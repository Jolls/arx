# F1 — User Identity Tracking ([#207](https://github.com/Jolls/arx-legacy/issues/207))

## Context

Arx has no authentication layer. Today the 4 lock/unlock handlers and the PO/part "requested by" defaults call `os.Getenv("USERNAME")` / `user.Current()` — which returns the server's OS user (whoever launched `Arx.exe`), not the browser client. So every shop user shows up identically in `record_events` / `form_events`, and the `trg_test_definition_history` trigger records `SYSTEM_USER` (the shared DB login). There is no way to audit who created, edited, locked, or unlocked anything.

F1 is the foundation PR for the v0.6 milestone: every downstream audit / approval / sign-off feature (AUD-1 [#289](https://github.com/Jolls/arx-legacy/issues/289), PO approval [#267](https://github.com/Jolls/arx-legacy/issues/267), TR reviewer [#249](https://github.com/Jolls/arx-legacy/issues/249), reopen-with-reason [#250](https://github.com/Jolls/arx-legacy/issues/250), per-result history [#251](https://github.com/Jolls/arx-legacy/issues/251), form revisions [#260](https://github.com/Jolls/arx-legacy/issues/260)) depends on a real app-level user identity and on `SET CONTEXT_INFO` plumbing so DB triggers can record the actual actor.

**Goal:** add a username+password login over a small `users` table, gate the app behind it, expose the logged-in user to handlers, and plumb that identity to the `test_definition` trigger via `CONTEXT_INFO`.

## Decisions (confirmed with user)

- **Auth model:** Username + password login (bcrypt), app gated until logged in.
- **User management:** Admin-managed in the existing Settings page.
- **Roles:** None now — identity only. (Approval PRs add their own role model later.)

## Design

### 1. Schema — new users table + trigger change

New table `SQL/users.sql`:

```sql
users (
  id            INT PRIMARY KEY IDENTITY,
  username      VARCHAR(64)  NOT NULL UNIQUE,
  display_name  VARCHAR(128) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,     -- bcrypt
  active        BIT NOT NULL DEFAULT 1,
  created_at    DATETIME NOT NULL DEFAULT GETDATE(),
  updated_at    DATETIME NOT NULL DEFAULT GETDATE()
)
```

Trigger change in `SQL/TestRecords.sql` — `trg_test_definition_history` reads the app user from `CONTEXT_INFO()`, falling back to `SYSTEM_USER` when unset (so the old 0.5.x binary still works on rollback). `CONTEXT_INFO` is a 128-byte VARBINARY padded with 0x00; strip the nulls:

```sql
changed_by = COALESCE(
  NULLIF(REPLACE(CONVERT(VARCHAR(128), CONTEXT_INFO()), CHAR(0), ''), ''),
  SYSTEM_USER)
```

Keep the column's `DEFAULT SYSTEM_USER` as a safety net. The matching backfill in `SQL/migrations/drop_pf_formula.sql` is unaffected (leave as-is).

Per the project's "new table" checklist, also update:

- `SQL/_test.sql` — add DROP + `SELECT * INTO` for `users` into ArxDev
- `arxlib/config/config.go` — add `UsersTable()` helper (bare name, like the others)
- `SQL/schema.md` — add `users` to the table reference

### 2. Auth layer (`arx_go/auth.go`, new file)

- `golang.org/x/crypto/bcrypt` for hashing (add to `arx_go/go.mod`).
- `User` struct (`ID`, `Username`, `DisplayName`); load helpers `userByUsername`, `userByID`.
- `GET/POST /login`, `POST /logout` handlers — always accessible (no DB-gate beyond redirecting to `/settings` when `h.db == nil`).
- **Session:** store `user_id` in the existing gorilla session (`arx-session` cookie, already wired in `handlers.go`). The cookie is HMAC-signed; storing an int id is fine. Add `h.currentUser(r) → *User` (nil if not logged in), reading the id from the session and looking up the row.
- **Middleware:** extend `RequireAuth` in `handlers.go`. Current behavior (`h.db == nil → /settings`) stays; add: when `h.currentUser(r) == nil`, redirect to `/login`. Also stash the user in `r.Context()` so write paths can reach it without re-querying.
- **Bootstrap (first run):** on `GET /login`, if `SELECT COUNT(*) FROM users WHERE active=1` is 0, render a "create first admin" form instead of the login form; its POST creates the first user and logs them in. This avoids a chicken-and-egg (the app is gated, but no users exist yet).
- Login/logout forms include the CSRF token via the existing `h.csrfToken` (works without a DB); `RequireCsrfOnPost` already guards all POSTs.

### 3. User management in Settings

Add a Users section to the Settings page (`templates/pm/settings.html` + `settings.go` `settingsData`): list users, add user (username + display name + password), reset password, toggle active. Mirror the existing attachment-categories / PO-defaults save pattern (POST `/settings/users`-style route registered alongside the other settings routes in `main.go`).

The Users section renders only when `h.currentUser(r) != nil` — DB config stays open for recovery, but account management requires being logged in (first admin comes from the bootstrap flow above).

### 4. CONTEXT_INFO plumbing (only the test_definition write needs it today)

Only `trg_test_definition_history` records an actor via the DB. The definition save handler (`records.go` ~613–723) issues a loop of UPDATE/INSERT on `test_definition`, each via a separate `h.execContext` (separate pool checkouts) — `SET CONTEXT_INFO` is connection-scoped, so it must run on the same connection as the writes.

**Approach:** wrap that save block in a transaction using the existing `h.beginTx(ctx)` (returns `*txLogger`). Immediately run `tx.ExecContext(ctx, "SET CONTEXT_INFO @p1", []byte(username))` once, then route the loop's UPDATE/INSERTs through `tx` and `Commit()`. All run on one connection, so the trigger sees the user. `username` comes from `currentUser` (via request context). `go-mssqldb` pads the `[]byte` to 128 bytes automatically.

Keep this minimal — one save path needs it now. Do not retrofit a general `CONTEXT_INFO` layer across every `execContext`; future audited writes can call the same tx pattern when they're built.

### 5. Replace the fake server-OS username with the real app user

Swap `os.Getenv("USERNAME")` / `user.Current()` for `h.currentUser(r)` in:

- `records.go` `LockRecord` (1463), `UnlockRecord` (1505), `LockForm` (1537), `UnlockForm` (1579) — these write `username` into `record_events`/`form_events` directly (no trigger), so they just need the real name.
- `parts.go` ~238 (`PNReqBy` default) and `pos.go` ~254 (`Orderer` default) — use the logged-in `DisplayName`/`Username` as the prefill default.

Remove the now-unused `os`/`os/user` imports where they become orphaned (check each file — `os` may still be used elsewhere).

## Critical files

| File | Change |
|------|--------|
| `SQL/users.sql` | new — users table DDL |
| `SQL/TestRecords.sql` | trigger reads `CONTEXT_INFO()` w/ `SYSTEM_USER` fallback |
| `SQL/_test.sql`, `SQL/schema.md` | new-table checklist |
| `arxlib/config/config.go` | `UsersTable()` helper |
| `arx_go/auth.go` | new — User model, login/logout, bootstrap, `currentUser` |
| `arx_go/handlers.go` | extend `RequireAuth`; stash user in request ctx |
| `arx_go/main.go` | register `/login`, `/logout`, `/settings/users` routes |
| `arx_go/settings.go` + `templates/pm/settings.html` | Users management section |
| `arx_go/records.go` | tx + `SET CONTEXT_INFO` around definition save; real user in 4 lock/unlock handlers |
| `arx_go/parts.go`, `arx_go/pos.go` | real-user prefill defaults |
| `arx_go/go.mod` | add `golang.org/x/crypto` |
| `CHANGELOG.md`, `arx_go/RELEASE_NOTES.md` | version entry |

## Migration / backward-compatibility notes

- Requires a manual DB step on upgrade (prod and ArxDev): create `users` and replace the trigger. `SQL/*.sql` are reference DDL, not auto-run.
- **Rollback-safe:** `users` is a new table the old binary ignores; the trigger keeps `SYSTEM_USER` as fallback, so the old 0.5.x binary still writes audit rows (just without app-user attribution). Matches the plan doc's "degraded but not broken" classification.
- Once all instances are on F1+, remove the `SYSTEM_USER` fallback from the trigger — tracked in [#461](https://github.com/Jolls/arx-legacy/issues/461).

## Verification

1. `cd arx_go && .\build.bat` (runs `go test ./...` + builds) — must pass.
2. Manual, against ArxDev (`TEST_MODE=true`):
   - Run `SQL/users.sql` + the trigger update on ArxDev.
   - First run → `/login` shows the create-first-admin form; create admin → lands logged in.
   - Log out → app routes redirect to `/login`; `/settings` and `/static/*` stay reachable.
   - Settings → Users: add a second user, reset a password, deactivate; confirm deactivated user can't log in.
   - Edit a test/form definition while logged in as user X → query `test_definition_history` and confirm `changed_by = 'X'` (not `SYSTEM_USER`).
   - Lock then unlock a record → `record_events.username` shows the logged-in user.
3. Suggested regression test: a unit test for the password hash/verify helper and for `currentUser` returning nil on an empty/garbage session (cheap, guards the auth gate). Offer it; don't add unless wanted.

## Post-migration follow-on (once all instances are on F1+)

These are not part of the F1 PR. They become possible only after every instance running against the production DB has been updated to the F1 binary.

### Historical username backfill

Before F1, several fields stored the Windows OS username (whoever launched `Arx.exe`) or `SYSTEM_USER` (the shared DB login):

| Table | Column | Old value | After F1 |
|-------|--------|-----------|----------|
| `PO` | `orderer` | OS username (e.g. `REDACTED`) | App user `display_name` |
| `record_events` | `username` | OS username | App username |
| `form_events` | `username` | OS username | App username |
| `test_definition_history` | `changed_by` | `SYSTEM_USER` | App username (via `CONTEXT_INFO`) |

Once the user roster is stable, a one-time migration script can map old OS-level names to app users. The mapping is manual (OS usernames don't necessarily match app usernames), so the script should be written and reviewed against the actual data before running. Rows that can't be mapped should be left as-is rather than guessed.

Tracked separately — file a migration issue when ready.

### PO defaults — per-user vs. global

Currently `PODefaults` (`ContactID`, `ReceiverID`) lives in `config/local.json` — a single global default applied to every new PO regardless of who is logged in. `ContactID` prefills `SupplierContact` (the company's point of contact for the supplier); `ReceiverID` prefills the ship-to company.

With F1, the `Orderer` field is already derived from the logged-in user's `display_name`. The open question is whether `SupplierContact` and `ReceiverID` should also become **per-user** defaults (stored in the DB against `users.id`) rather than a global machine-level default.

**Arguments for per-user defaults:**
- Different users may have different default contacts and receivers (e.g. buyer A ships to Site 1, buyer B ships to Site 2).
- The global default was a workaround for having no user identity; with F1 it's no longer necessary.

**Arguments for keeping global:**
- The current defaults are site-wide (same supplier contact and receiver for the whole shop) — per-user adds complexity for no benefit in a single-site shop.
- Changing the storage location (DB vs `local.json`) is a schema change and settings UI change.

**Decision needed before acting.** Leave as global default for now; revisit when there is a concrete case where users need different defaults.

## Out of scope (deferred to later PRs)

- Roles / permissions, reviewer sign-off, PO approval gating ([#249](https://github.com/Jolls/arx-legacy/issues/249), [#267](https://github.com/Jolls/arx-legacy/issues/267)).
- Password reset by email, lockout, audit of logins.
- A general per-write `CONTEXT_INFO` abstraction — added only where a trigger needs it today.
