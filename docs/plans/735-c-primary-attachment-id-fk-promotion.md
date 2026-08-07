# #735 Group C — `part.primary_attachment_id`: 0→NULL sentinel rework + FK promotion

`part.primary_attachment_id` FKs to `part_attachment.id`. It currently uses a `DEFAULT 0`
sentinel, is a plain `int` in the Go `Part` struct, and has **no DB-enforced FK**. The
`company.primary_attachment_id` column (→ `company_attachment.id`) already went through this
exact conversion — it is `NULL`-based, FK-enforced, and represented as `*int` in
`models.Supplier`. **Follow that pattern exactly**; do not invent a new one.

Reference implementation to copy from:
- `SQL/azure/company.sql` lines 23, 27 (`primary_attachment_id INT NULL`, `FK_company_primary_attachment`)
- `arx_go/models/supplier.go:17` (`PrimaryAttachmentID *int`)
- `arx_go/suppliers.go:640,655,685-688` (scan via `sql.NullInt64` → `if primaryAttID.Valid { v := int(...); s.PrimaryAttachmentID = &v }`)
- `arx_go/suppliers.go:692-700` (`SupplierSetPrimaryAttachment` — already nil-write capable, no change needed)
- `arx_go/templates/suppliers/supplier_attachments.html:27` (`{{$isPrimary := and $.Supplier.PrimaryAttachmentID (eq .SupplierAttachmentID (derefInt $.Supplier.PrimaryAttachmentID))}}`)

## Files to change

### 1. `arx_go/models/part.go` line 22
```go
PrimaryAttachmentID int
```
→
```go
PrimaryAttachmentID *int // part_attachment.id of the primary attachment; nil = none set
```

### 2. `arx_go/parts.go` — `fetchPartBasic` (lines 29-48)
Line 33 `var filIDPrimary sql.NullInt64` stays (already the right scan type). Line 38 scan stays.
Line 43:
```go
p.PrimaryAttachmentID = int(filIDPrimary.Int64)
```
→
```go
if filIDPrimary.Valid {
    v := int(filIDPrimary.Int64)
    p.PrimaryAttachmentID = &v
}
```

### 3. `arx_go/parts.go` — `PartDetail` (lines 225-330ish)
Line 279, same change as above:
```go
p.PrimaryAttachmentID = int(filIDPrimary.Int64)
```
→
```go
if filIDPrimary.Valid {
    v := int(filIDPrimary.Int64)
    p.PrimaryAttachmentID = &v
}
```

Line 318:
```go
if p.PrimaryAttachmentID > 0 {
```
→
```go
if p.PrimaryAttachmentID != nil {
```
and line 324's `p.PrimaryAttachmentID` (passed as the query param) becomes `*p.PrimaryAttachmentID`
— safe inside this `if` block since the nil-check guards it.

Line 351:
```go
if att.ID != p.PrimaryAttachmentID && len(topAtts) < 5 {
```
→
```go
if (p.PrimaryAttachmentID == nil || att.ID != *p.PrimaryAttachmentID) && len(topAtts) < 5 {
```

### 4. `arx_go/parts.go` — `PartSetPrimaryAttachment` (lines 1727-1740)
Already builds `val any` (nil when `filid` is `0`/unparsable, else the int) and calls the shared
`setPrimaryAttachment` helper — **no change needed**, it already writes NULL correctly. Confirm
after the struct change that this function doesn't also assign into `models.Part` anywhere in this
range (it doesn't per current read) — just the DB write, so it's unaffected by the struct type
change.

### 5. `arx_go/templates/parts/part_attachments.html` line 51
```
{{$isPrimary := eq .ID $.Part.PrimaryAttachmentID}}
```
→ (copy the supplier template idiom exactly)
```
{{$isPrimary := and $.Part.PrimaryAttachmentID (eq .ID (derefInt $.Part.PrimaryAttachmentID))}}
```

### 6. `arx_go/utilities.go`
**`checkOrphanPointers`** (~line 151-197): no logic change needed — `p.%[1]s > 0` is already
NULL-safe (see Group B plan's note: `NULL > 0` evaluates false in SQL Server, same exclusion as
the old `0` sentinel). After this group lands, update the doc comment (lines 151-154) to drop the
`primary_attachment_id` "sentinel of 0" carve-out entirely, since now all three columns
(`default_supplier_id`, `price_id`, `primary_attachment_id`) are NULL-based — comment can simplify
to something like: "Unset values are `NULL`; the `> 0` guard excludes them since `NULL > 0` is
never true."

**`checkSoftDeletedAttachmentPointers`** (~line 199-240+): line 236
```go
`WHERE p.primary_attachment_id > 0 AND f.is_active = %[3]s`,
```
No change needed for the same NULL-safety reason — leave as-is (matches the existing
`company.primary_attachment_id` query in this same function, which is already NULL-based and
already uses `> 0` without issue — check that query nearby to confirm the existing pattern, since
it's the working precedent already in this exact function).

### 7. `SQL/azure/part.sql`
Line 50: change
```sql
primary_attachment_id INT            CONSTRAINT DF_part_number_primary_attachment_id DEFAULT 0,  -- part_attachment.id of the primary attachment.
```
to
```sql
primary_attachment_id INT            NULL,  -- part_attachment.id of the primary attachment.
```
Add FK after the existing `FK_part_number_uom` line (68) — and after Group B's `FK_part_price` if
that landed first:
```sql
ALTER TABLE dbo.part ADD CONSTRAINT FK_part_primary_attachment FOREIGN KEY (primary_attachment_id) REFERENCES dbo.part_attachment (id);
```

### 8. `SQL/postgres/part.sql`
Line 34: change
```sql
primary_attachment_id INTEGER        DEFAULT 0,
```
to
```sql
primary_attachment_id INTEGER        NULL,
```
Add FK after line 50 (and after Group B's if landed first):
```sql
ALTER TABLE part ADD CONSTRAINT FK_part_primary_attachment FOREIGN KEY (primary_attachment_id) REFERENCES part_attachment (id);
```

### 9. New migration: `SQL/azure/migrations/migrate_735c_primary_attachment_id_fk_promotion.sql`
Same pattern as Group B's migration (USE ArxDev pin, idempotent guards, orphan-check comment,
Postgres-equivalent comments):
```sql
USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- 1. Convert the 0-sentinel to NULL.
UPDATE dbo.part SET primary_attachment_id = NULL WHERE primary_attachment_id = 0;
-- Postgres: UPDATE part SET primary_attachment_id = NULL WHERE primary_attachment_id = 0;

-- 2. Drop the DEFAULT 0 constraint.
IF EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_part_number_primary_attachment_id')
    ALTER TABLE dbo.part DROP CONSTRAINT DF_part_number_primary_attachment_id;
-- Postgres: ALTER TABLE part ALTER COLUMN primary_attachment_id DROP DEFAULT;

-- 3. Promote to a real FK. ORPHAN CHECK FIRST on a live DB — must return zero rows:
--      SELECT p.id, p.primary_attachment_id FROM dbo.part p
--      LEFT JOIN dbo.part_attachment fa ON fa.id = p.primary_attachment_id
--      WHERE p.primary_attachment_id IS NOT NULL AND fa.id IS NULL;
IF OBJECT_ID('dbo.FK_part_primary_attachment', 'F') IS NULL
    ALTER TABLE dbo.part ADD CONSTRAINT FK_part_primary_attachment FOREIGN KEY (primary_attachment_id) REFERENCES dbo.part_attachment (id);
-- Postgres: ALTER TABLE part ADD CONSTRAINT FK_part_primary_attachment FOREIGN KEY (primary_attachment_id) REFERENCES part_attachment (id);

-- 4. Patch stored named_queries text: 'pn_primary_attachment' and 'form_primary_attachment' both
--    embed `part.primary_attachment_id > 0` in their CASE expression. Functionally this still
--    works after the NULL conversion (NULL > 0 is never true, same as the old 0-sentinel
--    exclusion), but update the text anyway so it doesn't read as stale/misleading — mirrors the
--    named_queries content-update pattern in migrate_794_named_queries_drop_alias.sql.
UPDATE dbo.named_queries
SET sql = 'SELECT TOP 1 part_attachment.file_name, COALESCE(part_attachment.category, part_attachment.file_name) FROM part_attachment JOIN part ON part_attachment.part_id = part.id WHERE part.part_number = @pn AND part_attachment.is_active = 1 ORDER BY CASE WHEN part.primary_attachment_id IS NOT NULL AND part_attachment.id = part.primary_attachment_id THEN 0 ELSE 1 END, part_attachment.sort_order ASC'
WHERE name = 'pn_primary_attachment';
UPDATE dbo.named_queries
SET sql = 'SELECT TOP 1 part_attachment.file_name, COALESCE(part_attachment.category, part_attachment.file_name) FROM part_attachment JOIN part ON part_attachment.part_id = part.id WHERE part.id = @pnid AND part_attachment.is_active = 1 ORDER BY CASE WHEN part.primary_attachment_id IS NOT NULL AND part_attachment.id = part.primary_attachment_id THEN 0 ELSE 1 END, part_attachment.sort_order ASC'
WHERE name = 'form_primary_attachment';
-- Postgres: named_queries is dialect-agnostic row data — identical UPDATEs apply.
```
No `ON DELETE` clause (defaults to `NO ACTION`).

**Do not bump `schema_version`**: an old binary that still writes `0` via some code path this
review missed would violate the FK loudly (insert/update error) rather than silently corrupt data
— acceptable since the audit above found `PartSetPrimaryAttachment` already writes NULL, not 0,
via `setPrimaryAttachment`'s `any` param. If any other write path is discovered during
implementation that still writes literal `0`, flag it — that would need fixing as part of this
same change, not deferred.

### 10. `SQL/azure/named_queries.sql` and `SQL/azure/seed_test_data.sql` (reference copies)
Update the same `part.primary_attachment_id > 0` → `IS NOT NULL` text in:
- `SQL/azure/named_queries.sql` lines 69, 81
- `SQL/azure/seed_test_data.sql` lines 133, 136
- `SQL/postgres/named_queries.sql` (grep for the same query names to find the equivalent lines)
so the reference DDL/seed files match what the migration produces (these files are never
auto-run, but must stay in sync with the live schema per CLAUDE.md).

### 11. `SQL/schema.md`
`part` row (~line 140): the row currently reads `` `primary_attachment_id` → `part_attachment.id`. ``
with no "deferred" language — add `(FK enforced)` for consistency with how `default_supplier_id`
now reads after Group A, e.g. `` `primary_attachment_id` → `part_attachment.id` (FK enforced). ``

## Success criteria
- `go build ./...`, `go vet ./...`, `go test ./...` pass.
- Any test fixture that assigns `Part.PrimaryAttachmentID` as a bare `int` needs updating to `*int`
  — grep `arx_go/**/*_test.go` for `PrimaryAttachmentID` during implementation and fix any hits
  (not found in the research pass, but the research pass was not exhaustive over test files).
- Migration is idempotent, single-batch (no `GO`), ArxDev-pinned, includes the orphan-check
  comment for the human to run first against ArxProd, and patches `named_queries`.
- Both SQL dialects updated and stay in sync.
- Manual check: on `/part/{id}/attachments`, setting/clearing the primary attachment star still
  works, and the star renders on the correct row (exercises the template fix).

## Not in scope here
`part.price_id` — Group B, much smaller Go surface, plan separately (already written).
