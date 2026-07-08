# #463 Part 2 — PO defaults: per-user

Part 1 (historical username backfill) shipped in PR #633. This plan covers **Part 2 only**:
make the PO default **contact** and **receiver** per-user and remove the old machine-level
global PO defaults entirely.

## Decisions (locked with user)

- **Storage:** two nullable columns on the existing `users` table — no new table.
- **Scope:** both contact and receiver become per-user.
- **No global fallback:** the machine-level global PODefaults (`config/local.json` +
  Settings → PO Defaults UI) are removed. There is no site-wide layer.
- **Precedence for a new PO/RFQ:** `user default → receiver company's own default_contact`
  (the company fallback only supplies the contact once a receiver is chosen). If the user has
  no defaults, the PO/RFQ opens with the receiver/contact blank.
- **Edit UI:** a **My Preferences** tab on the Settings page (`/settings#preferences`),
  saved via `POST /settings/preferences`. No standalone page.

## Changes

### Schema (human applies DDL to ArxDev, then ArxProd)
1. `SQL/users.sql` — add `default_po_contact_id INT NULL`, `default_po_receiver_id INT NULL`.
2. `SQL/migrations/migrate_users_po_defaults.sql` — guarded/idempotent ALTERs for existing DBs.
3. `SQL/schema.md` — document the two new `users` columns.
4. `SQL/seed_test_data.sql` — admin (8001) seeded with receiver 1003 + contact 2005;
   tester (8002) left NULL (covers the blank path). Requires an ArxDev reseed to take effect.
5. `arx_go/integration_test.go` — `TestIntegration_UserPODefaults` asserts `userByID` scans
   both columns (admin 1003/2005, tester 0/0).

### Go
4. `arx_go/auth.go` — add `DefaultPOContactID`, `DefaultPOReceiverID int` to `User`; select
   them in `userByID` (NULL → 0). User is cached, so profile save calls `invalidateUserCache`.
5. `arx_go/pos.go` — `applyPODefaults` reads only the logged-in user's defaults; keeps the
   final fallback to the receiver company's `default_contact`.
6. `arx_go/settings.go` — `settingsData` supplies the user's per-user defaults + contact/
   supplier options for the tab; `SettingsPreferencesSave` (POST) persists them (reusing
   `fetchContactOptions`/`fetchSupplierOptions`).
7. `arx_go/main.go` — register `POST /settings/preferences` inside the `RequireAuth` group.
8. **Global removal:** dropped `Config.PODefaults` + `PODefaults` type (`arxlib/config/config.go`),
   the `po_default_*` fields from `LocalConfig`/legacy migration (`arxlib/config/local.go`), and
   the PO-defaults read/parse/save in `arx_go/settings.go`.

### Templates
9. `arx_go/templates/pm/settings.html` — removed the old global PO Defaults section; added a
   **My Preferences** sub-tab + panel with the per-user PO-defaults form, reusing
   `supplier-typeahead.js` + `/api/suppliers/{id}/contacts`.
10. `arx_go/templates/shared/layout.html` — user name in header links to `/settings#preferences`.

### Docs
12. `CHANGELOG.md` — one entry, links #463.

## Note on existing installs
Old `config/local.json` files may still contain `po_default_contact_id` /
`po_default_receiver_id`. These keys are now ignored (unknown JSON fields) and harmless; they
are simply no longer read or written.
