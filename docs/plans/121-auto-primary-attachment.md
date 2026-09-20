# #121 — Auto-set / auto-promote primary attachment (parts + suppliers)

Branch: `feature/121-auto-primary-attachment`. Model: Sonnet (no schema change; data-only backfill).

## Design (single rule, one helper)

One idempotent statement, "ensure primary", run after every insert and every soft-delete:

> Set parent.primary_attachment_id to the lowest (`sort_order`, then id) active attachment of that parent, but only when the current primary is NULL or points at an inactive attachment.

Covers all acceptance criteria:
- first attachment added, primary NULL: set.
- later attachment added, primary is active: unchanged (WHERE clause skips it).
- primary deleted/deactivated: pointer now references an inactive row, so it is repointed to next active; if none remain the subselect yields NULL, so it is cleared.
- Manually chosen primary (`PartSetPrimaryAttachment` / `SupplierSetPrimaryAttachment`): untouched, no code change there.

FK note (migrate_735c, `FK_part_primary_attachment`, `FK_company_primary_attachment`): attachments are only soft-deleted (`is_active=0`) by app code, never hard-deleted, so the FK never blocks and no delete-ordering issue exists. The ensure UPDATE runs AFTER the deactivate UPDATE and only ever points at an existing active row or NULL, so it always satisfies the FK. (Integration tests hard-delete; they must NULL the primary first, as `integration_test.go:3207` already does.)

## File changes

### 1. `arx_go/attachments.go` — add helper next to `setPrimaryAttachment` (line ~541)

Add `ensurePrimaryAttachment` on `*Handler`, plus a pure SQL builder for unit testing:

- `func (h *Handler) primaryAttachmentEnsureSQL(parentTable, parentPK, primaryCol, attTable, attPK, ownerCol string) string` builds:
  ```
  UPDATE <parentTable> SET <primaryCol> = (
      SELECT <TopClause("1")> a.<attPK> FROM <attTable> a
      WHERE a.<ownerCol> = <parentTable>.<parentPK> AND a.is_active = <BoolLiteral(true)>
      ORDER BY a.sort_order, a.<attPK> <LimitClause("1")>)
  WHERE <parentPK> = @p1 AND (<primaryCol> IS NULL OR NOT EXISTS (
      SELECT 1 FROM <attTable> x WHERE x.<attPK> = <parentTable>.<primaryCol> AND x.is_active = <BoolLiteral(true)>))
  ```
  Use `h.dia().TopClause("1")` / `LimitClause("1")` exactly as `findDuplicateAttachment` (attachments.go ~line 809) does. Table names come only from the caller (`cfg.*Table()`), never literals.
- `func (h *Handler) ensurePrimaryAttachment(ctx, exec func(context.Context, string, ...any) (sql.Result, error), parentTable, parentPK, primaryCol, attTable, attPK, ownerCol string, parentID any) error` — runs the SQL with `parentID` as `@p1`. `exec` is `h.execContext` or `tx.ExecContext`.
- Two thin wrappers used by the call sites:
  - `ensurePartPrimaryAttachment(ctx, exec, partID any)` → `PartsTable(), "id", "primary_attachment_id", AttachmentsTable(), "id", "part_id"`
  - `ensureSupplierPrimaryAttachment(ctx, exec, supplierID any)` → `CompanyTable(), "id", "primary_attachment_id", CompanyAttachmentsTable(), "supplier_attachment_id", "supplier_id"`

NULL `sort_order`: see Open question 2.

### 2. Part insert sites — call `ensurePartPrimaryAttachment` right after the INSERT succeeds

- `arx_go/parts.go` `insertAttachmentRow` (line ~1810): after the INSERT, call ensure with `h.execContext`. This covers `PartAttachmentCreate` (single file) and `importAttachmentBatch` (attachments.go ~447). Wrap INSERT+ensure in one `h.beginTx`/`tx.ExecContext`/`Commit` (`defer tx.Rollback()`); `partID` param is a string, pass as-is.
- `arx_go/api.go` `APIPartPasteAttachment` (line ~313): same (INSERT then ensure, in one tx).
- `arx_go/api.go` `upsertGeneratedAttachment` INSERT branch (line ~541): subject to Open question 1.
- `arx_go/sourcing.go` DigiKey import loop (line ~266): already inside a tx. After the `for _, pf := range preparedFiles` loop, call `ensurePartPrimaryAttachment(ctx, tx.ExecContext, partID)` once (only if `len(preparedFiles) > 0`); a failure returns the existing `(false, nil, fmt.Errorf(...))` shape.

### 3. Part deactivate — `arx_go/parts.go` `PartAttachmentDelete` (line ~2052)

Replace the bare `h.softDeleteAttachment(...)` call with: `tx, err := h.beginTx(ctx)`; `defer tx.Rollback()`; deactivate via the same `UPDATE ... SET is_active=<BoolLiteral(false)>` (refactor `softDeleteAttachment` at attachments.go ~528 to take an `exec` func param, or inline the UPDATE); then `ensurePartPrimaryAttachment(ctx, tx.ExecContext, id)`; `tx.Commit()`. Do not add `part_id` scoping beyond what exists now (the current call passes ownerCol "" — see Open question 4). `softDeleteAttachment` has one caller (parts.go:2055), so changing its signature is safe.

### 4. Supplier insert — `arx_go/suppliers.go` `SupplierAttachmentCreate` (line ~639)

INSERT then `ensureSupplierPrimaryAttachment(ctx, exec, id)` in one tx (id is the URL string).

### 5. Supplier deactivate — `arx_go/suppliers.go` `SupplierAttachmentDelete` (line ~682)

Wrap the existing `UPDATE ... SET is_active` and `ensureSupplierPrimaryAttachment` in one `h.beginTx` tx; commit at the end.

### 6. Not changed (verified)

- `PartAttachmentUpdate`, `SupplierAttachmentUpdate`, `APIPartPasteAttachmentReplace`: edit rows in place, never insert/deactivate.
- No app code hard-deletes attachment rows or re-activates them (`is_active=1` only appears in reads).
- `PartSetPrimaryAttachment`, `SupplierSetPrimaryAttachment`, `setPrimaryAttachment`: unchanged (selection UI out of scope).
- Named queries `pn_primary_attachment` / `form_primary_attachment`: unchanged.
- `arx_go/utilities.go` data-quality check "primary attachment is soft-deleted" (~line 231): unchanged; it will simply stop finding new rows.

### 7. Migration — `SQL/azure/migrations/<YYYYMMDDHHMMSS>_121_backfill_primary_attachment.sql`

Timestamp = authoring time at implementation; the filename version and both `schema_migrations` statements must match (`TestMigrationsSelfRegister`). Contents, single batch, no `GO`:
- Header comment: pinned to ArxDev, human changes to ArxProd before running; single-batch/no-GO note.
- `USE ArxDev;`
- Optional pre-view SELECT of rows that will change (parts and suppliers with NULL primary and >=1 active attachment).
- `UPDATE p SET primary_attachment_id = (SELECT TOP 1 a.id FROM dbo.part_attachment a WHERE a.part_id = p.id AND a.is_active = 1 ORDER BY a.sort_order, a.id) FROM dbo.part p WHERE p.primary_attachment_id IS NULL AND EXISTS (SELECT 1 FROM dbo.part_attachment a WHERE a.part_id = p.id AND a.is_active = 1);`
- Same for `dbo.company` / `dbo.company_attachment` (`a.supplier_attachment_id`, `a.supplier_id`).
- No new columns, so no `EXEC(N'...')` wrapper needed; no FK-promotion orphan check needed (no constraint added). The `IS NULL` guard makes it idempotent and leaves non-NULL primaries untouched.
- Last step: `IF NOT EXISTS (SELECT 1 FROM dbo.schema_migrations WHERE version_id = <ts>) INSERT INTO dbo.schema_migrations (version_id, is_applied) VALUES (<ts>, 1);`
- No `app_config.schema_version` bump (non-breaking).
- Postgres: no `SQL/postgres/migrations/` directory exists (Postgres DDL is fresh-load only, and `SQL/postgres/README.md` says the T-SQL set is deleted at cutover), so no Postgres counterpart is written; the Go code is dialect-neutral via `h.dia()`. See Open question 3.

### 8. Tests

Unit (no DB), `arx_go/attachments_test.go`:
- `TestPrimaryAttachmentEnsureSQL`: build with `arxdb.NewSQLServerDialect()` and `arxdb.NewPostgresDialect()` via a `Handler` or by having the builder take a `db.Dialect` param (preferred, so no Handler needed). Assert: SQL Server output contains `TOP (1)` and `is_active = 1`, no `LIMIT`; Postgres output contains `LIMIT 1` and `is_active = TRUE`, no `TOP`; both contain `IS NULL OR NOT EXISTS`, `ORDER BY a.sort_order, a.<pk>`, and the passed table/column names for both the part and company argument sets.

Integration, `arx_go/integration_test.go` (`//go:build integration`), new `TestIntegration_AutoPrimaryAttachment`, modeled on `TestIntegration_SetPrimaryAttachment` (line ~5581). Use throwaway rows, NOT seed rows: `seedThrowawayPart(t,h,ctx,"121")` for the part; a throwaway company inserted like the `set_on_supplier` subtest (line ~5617). The seed data is not modified. Seed reference only: part 3002 has active attachments 8101/8103/8104 and 3004 has 8102, all with NULL primary (the seed nulls it); do not rely on them.
- Part subtests: (a) insert first attachment via `h.insertAttachmentRow` (handler-level path), read `primary_attachment_id` == its id; (b) insert second, primary unchanged; (c) deactivate primary through the delete path (extract the tx body into a helper such as `deactivateAttachmentAndPromote` so the test can call it without HTTP, or drive `PartAttachmentDelete` via httptest with chi URL params), primary becomes the second; (d) deactivate the second, primary is NULL.
- Supplier subtests: same four steps with `company_attachment` (insert via a helper extracted from `SupplierAttachmentCreate`, or drive the handler with httptest), plus explicit check that a manually set primary (`setPrimaryAttachment`) survives a later insert.
- Cleanup: NULL the parent's `primary_attachment_id` before hard-deleting attachments (FK), then delete attachments, then parent (defer order: attachments before parent, same as line 3207).
- Backfill statement: add a subtest that runs the migration's part `UPDATE` text against a throwaway part whose primary is NULL with an active attachment inserted via `seedThrowawayAttachment` (which does not call ensure), and asserts it is set, and that a part with a non-NULL primary is unchanged. (Copy the SQL into the test; do not read the migration file.)

Run: `go build ./... && go vet ./... && go test ./...` in `arx_go`, then the integration tag per CLAUDE.md pre-commit step 2 (ArxDev only; check `local.json` `test_engine` is not postgres — see memory note).

### 9. CHANGELOG

New top entry `## [0.7.54] - <date>` (next patch after 0.7.53) in `CHANGELOG.md`:
```
### Added
- The first active attachment on a part or supplier with no primary is now set as primary automatically, and deleting the primary promotes the next active attachment (or clears it); a migration backfills existing records ([#121](https://github.com/Jolls/arx/issues/121))
```
Do not edit `arx_go/RELEASE_NOTES.md` (per-public-release only) and do not set `AppVersion`.

## Open questions

1. Do generated "PDF Preview" / "Thumbnail" rows (`upsertGeneratedAttachment`, DigiKey "Photo" rows) count as eligible for auto-primary? As written, any active attachment qualifies, so a generated Thumbnail could become primary for a part with no other attachment. Should generated categories be excluded from both the ensure statement and the backfill?
2. `sort_order` is nullable (part default 1, company no default). SQL Server sorts NULL first, Postgres last; the named queries use bare `ORDER BY sort_order ASC`. Should NULL sort_order be treated as lowest (SQL Server behavior; `COALESCE(sort_order, 0)`) or highest, or is bare `sort_order, id` acceptable?
3. Is a Postgres migration counterpart expected? No `SQL/postgres/migrations/` exists, so the plan writes none. Confirm, or specify where a Postgres backfill should live.
4. `PartAttachmentDelete` and `PartAttachmentUpdate` currently do not check the attachment belongs to the URL's part id (`softDeleteAttachment(..., "", 0)`), whereas supplier delete does. The ensure statement runs against the URL part id, so a mismatched id would promote on the wrong parent. Should the part delete gain the `part_id` ownership filter (unrequested behavior change), or leave as is?
5. Should the tx wrapping on the insert paths be required, or is INSERT followed by a separate ensure call acceptable (failure leaves NULL primary, self-heals on next insert/delete)? Plan currently specifies a tx.

## Resolved decisions
1. Generated categories (PDF Preview/Thumbnail) and DigiKey imports are NOT eligible for auto-primary; exclude them in both the ensure statement and the backfill migration.
2. Ordering uses `COALESCE(sort_order, 0), id` everywhere (ensure, promote, backfill).
3. No Postgres counterpart migration.
4. Add a `part_id` ownership check to `PartAttachmentDelete`, matching the supplier delete.
5. Insert paths wrap INSERT and ensure in one transaction.
