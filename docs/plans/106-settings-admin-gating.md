# 106 — Admin-gate the privileged settings routes

Issue: [#106](https://github.com/Jolls/arx/issues/106). Part of the pre-publication
review, [#102](https://github.com/Jolls/arx/issues/102).

## Problem

`requireAdmin` was called from six handlers, all user management. Every other
privileged settings route was gated by `RequireAuth` alone, so any active account could
change shop-wide and connection-level configuration.

## Resolved decision (confirmed with user)

Gate **connection, credentials, backup and diagnostics**. Leave **categories, part
numbering and company logo** open to any authenticated user — those are shop data a
technician legitimately edits, not a security boundary. Per-user preferences (accent
colour, timezone, default route, PO defaults) stay non-admin.

| Route | Gate |
|---|---|
| `POST /settings` | admin **once connected** |
| `POST /settings/digikey` | admin |
| `GET /settings/backup` | admin |
| `GET /settings/utilities` | admin |
| `POST /settings/{attachment-categories,categories,part-numbering,company-logo,company-logo/remove}` | unchanged — any authenticated user |
| `GET /settings` | unchanged — any authenticated user (the page also hosts My Preferences) |

## Changes

### 1. `arx_go/handlers.go` — `RequireAdminOnceConnected`

`POST /settings` cannot simply take `requireAdmin`: it must stay reachable during
first-run setup (no admin exists yet) and when the connection is broken (login is
impossible, and this is the only screen that can fix it). New middleware mirrors
`RequireAuthOnceConnected` and adds the admin check after the `dbUnusable()` bypass.

Uses `u.IsAdmin` from `withUser`'s return rather than `h.isAdmin(r)`, because the user
is not yet on the request context at that point.

### 2. `arx_go/main.go`

`POST /settings` switched from `RequireAuthOnceConnected` to `RequireAdminOnceConnected`.
`GET /settings` left alone.

### 3. In-handler `requireAdmin` calls

`SettingsDigiKeySave` (`settings.go`), `SettingsBackup` (`settings.go`),
`UtilitiesReport` (`utilities.go`). All three sit inside the `RequireAuth` group, so a
user is always present and `requireAdmin`'s plain-text 403 is the right shape.

### 4. `settingsData` — `CanEditConnection`

`h.dbUnusable() || h.isAdmin(r)`, mirroring the middleware so the view can't offer a
form the server will reject. Computed server-side rather than reassembled in the
template, so there is one definition of the rule.

### 5. `arx_go/templates/settings/settings.html`

- Connection tab link **and** panel gated on `.CanEditConnection`. The panel matters as
  much as the link: panels are shown by `#hash`, so leaving the markup would let a
  non-admin open it by URL fragment.
- DigiKey, Data Backup and Utilities sections gated on
  `{{if and .Connected .CurrentUser .CurrentUser.IsAdmin}}`, matching the existing Users
  tab idiom. They live inside the shared Configuration tab, which stays visible because
  categories/part-numbering/logo remain open.
- No default-tab change needed: the tab script falls back to `validTabs[0]`, so a
  non-admin with no Connection link lands on Configuration automatically.

### 6. Tests — `arx_go/settings_admin_gate_test.go`

`RequireAdminOnceConnected` across all four states (first run, broken connection,
connected non-admin, connected admin) and a table over the three in-handler gates for
non-admin and anonymous callers.

## Last-admin guard — deliberately NOT added

Recorded so this isn't re-litigated. The concern was that gating `POST /settings` opens
a lockout path if the last admin loses admin. **It does not.** Only two statements can
clear `is_admin`/`is_active` (`auth.go:486`, `auth.go:552`). Both sit behind
`requireAdmin`, and both refuse when the target is the acting user. The actor is
therefore always an admin who survives their own edit, so the active-admin count cannot
reach zero.

The only theoretical gap is two admins demoting each other in genuinely concurrent
requests — on a localhost single-shop tool, not worth guarding. Adding a count check
would be error-handling for an unreachable state.

Separately noted, not actioned: the self-guard is a blunt proxy. It also blocks an admin
from stepping down when other admins exist. Worth a follow-up issue if that ever annoys
anyone.

## Accepted consequence: admin rights are per-database

Raised by the high-effort review and accepted as intended, not a defect.

Because the gate reads `is_admin` from the **connected** database, switching Test Mode
swaps which user table is authoritative. Someone who is an admin on the production
database is not necessarily one on `ArxDev`, and after the swap they would see no
Connection tab and get 403 from `POST /settings`.

This is the correct semantics — "admin of the database you are connected to" — and it is
recoverable without touching files: log in as an admin **of that database**. Every
database with users has at least one active admin (first run creates one, and the
self-guards above keep the count above zero), so such an account always exists.

The residual case is connecting to a database where nobody knows an admin password. The
escape hatch there is the documented one: `config/local.json` wins over everything in
the config load order, so the connection can be pointed back by hand. Not worth widening
the gate for.

## Also fixed here: `/api/browse-folder`

`GET /api/browse-folder` carried a comment saying it was "gated the same way as
POST /settings" (#757), which this change would have falsified. Its only callers are the
four File Paths pickers on the Connection tab, which is now admin-only, so a non-admin
with no Connection UI could still have popped a native folder dialog on the host. Moved
to `RequireAdminOnceConnected` so the comment stays true and the DoS/nuisance reasoning
behind #757 still holds.

## Success criteria

- Non-admin gets 403 from `POST /settings`, `POST /settings/digikey`,
  `GET /settings/backup`, `GET /settings/utilities`.
- Non-admin can still save preferences, categories, part numbering and company logo.
- First-run setup (no DB) and broken-connection recovery still reach `POST /settings`
  without a login.
- Non-admin's Settings page shows no Connection tab, no DigiKey/Backup/Utilities
  sections, and lands on Configuration.
- `go build ./...`, `go vet ./...`, `go test ./...` pass in both modules.
