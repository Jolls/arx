# #251 / #247 / #224: auth + settings handlers → sqlc

Part of #251, #247, #224 / #190. One slice: every raw-SQL site in `auth.go` (12 statements, all on
`users`; the grep's 20 hits are multi-line) and `settings.go` (10 statements + the dynamic backup).
`named_query*.go` and `records*.go` are out of scope.

## Scope (raw sites)
- `auth.go`: `userByID`, `userByUsername`, `userCount`, `createUser`, `listUsers`, reset password,
  toggle active / can_approve_po / can_approve_records / is_admin.
  Sessions and CSRF live in the gorilla `arx-session` cookie, not the DB, so there is no session SQL.
- `settings.go`: `fetchContactOptions` (two variants: all / one company), `fetchSupplierOptions`,
  `loadAttachmentCategories`, `saveAttachmentCategories` (DELETE all + INSERT loop in one tx), the four
  `users` preference updates (accent color, timezone, default route, PO defaults), and the backup
  (`writeTableCSV`, `SELECT * FROM <table>` over ~29 tables + `users` + `app_config`).
- `appConfigGet/Set` already delegate to `internal/appconfig`; nothing else in settings.go touches
  `app_config` directly except the backup export.

## Where things live
- **`internal/auth`** (`auth.sql` + `Service`): every `users` statement, including the four
  per-user preference updates called from settings.go (same table, same owner). Service returns
  `auth.User` / `auth.Listed`; the handler `User` type is unchanged.
  Password hashing (bcrypt) stays in the handler layer: the service takes/returns hashes only.
- **`internal/settings`** (`settings.sql` + `Service`): contact/supplier dropdown options,
  attachment-category load/save (tx owned by the service), and the backup table read.
- **Backup**: not a flat query. `settings.BackupTables` is a literal allowlist of table names (replaces
  the `cfg.*Table()` list; same tables, same order) and `Service.QueryTable(ctx, name)` rejects any name
  not on it (or `users` / `app_config`, which have their own entry points) before running
  `SELECT * FROM <name>`. The raw SQL lives in `internal/settings`, so `settings.go` has no raw calls and
  joins `convertedFiles` with no lint exemption. Zip/CSV/row-filter logic stays in the handler.

## Flat-query decisions
- `fetchContactOptions`: one query, `(sqlc.arg(company_id)::int = 0 OR company_id = sqlc.arg(company_id)::int)`.
- `saveAttachmentCategories`: `DeleteAttachmentCategories :exec` + `InsertAttachmentCategory :exec`
  inside the service's tx (same order, same rollback-on-error, dedupe stays in Go).
- Nullable prefs: `default_po_*_id` params via `sqlc.narg(...)::int` (0 → NULL in the handler → nil pointer).
- The user-management UPDATEs keep `updated_at = CURRENT_TIMESTAMP`; the four preference updates do
  **not** touch `updated_at` (pinned in tests).

## Coverage audit (before)
Covered (integration_test.go etc.): login valid / wrong password / unknown user, user create
(bootstrap admin flag, missing fields), reset password, all four toggles + self-guards + cache
invalidation, `userByID` on seeded users (PO defaults / NULLs), attachment-category save order+dedupe,
backup `users.csv` has no `password_hash`, admin gate / middleware / idle timeout / login throttle
(unit).
Gaps → new `arx_go/auth_settings_integration_test.go` (all pass on the unchanged code first):
- `userByID` / `userByUsername` inactive → nil; NULL `default_route`/`accent_color` → ""; all fields
- login refuses an inactive user; `userCount` counts active only; `listUsers` order + inactive rows
- `createUser` stores a bcrypt hash and errors on duplicate username; toggles/reset bump `updated_at`
- toggle of a missing user id: no error (0 rows), redirect to success
- accent / timezone / default-route / PO-defaults saves persist, invalid input is a no-op, PO default
  0 → NULL, none of them bump `updated_at`
- contact options (all vs one company, inactive excluded), supplier options (inactive excluded)
- attachment categories: ordering by sort_order, empty list clears
- backup zip: csv file set is exactly the allowlist + users + app_config; app_config `secret_*` rows
  dropped

Not too big for one PR; no split.

## Helpers
`cfg.*Table()` helpers that lose their last non-test caller are deleted (user chose "all of them"),
tests switch to literal table names. Grep decides after conversion.

Verify: go build/vet (+ `-tags integration`), go test ./..., sqlc diff, gofmt -l, live ArxDev
integration run, then rerun `QueryDataQualityParts` /
`ReportsDataQualityMissingSupplierExportCSV`.

## Outcome / deviations
- Real counts at c4933c8: auth.go 12 statements (the grep's 20 hits are multi-line), settings.go 10
  flat statements + the dynamic backup read. No sessions table; sessions/CSRF are cookie-only.
- The four per-user preference UPDATEs in settings.go went to `internal/auth` (they are `users`
  writes), not `internal/settings`. `type User = auth.User` aliases the handler type so templates
  and tests compile unchanged.
- Backup: `settings.BackupTables` allowlist + `Service.QueryTable`; unit-tested to refuse anything
  else before touching the db. Same 28 tables + users + app_config, same order.
- No bug found: the nullable-column scans here already used `sql.Null*`/COALESCE-equivalent handling.
  Pinned quirks kept as-is: toggling / resetting a nonexistent user id is a silent success; the four
  preference saves leave `updated_at` alone and only invalidate the user cache on valid input.
- Helpers deleted (no non-test caller left; ~330 test call sites switched to literals): Users,
  AppConfig, AttachmentCategory, PartCategory, Uom, Company, Contact, PO, POLine, POHistory, Price,
  SupplierPart, MfgPart, Lot, Build, Genealogy, InventoryTxn. `LinksTable()` (no callers ever) left.
  Still used by unconverted code: Parts, Attachments, BOM, CompanyAttachments, Forms, Records, Steps,
  Results, NamedQueries, FormRowHistory, FormEvents, RecordEvents, RecordEventResults.
- `settings.go` and `auth.go` joined `convertedFiles`; no lint exemption needed.
- ArxDev seed drift seen: user 8001's accent_color is `green` (seed says `teal`); tests avoid it.
