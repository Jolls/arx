# #735 Group B — `part.price_id`: 0→NULL sentinel rework + FK promotion

`part.price_id` FKs to `price.id`. It currently uses a `DEFAULT 0` "no value" sentinel and has
**no DB-enforced FK**. Investigation confirms it is **not read or written anywhere in Go code**
except the generic orphan-pointer sweep in `arx_go/utilities.go` — no struct field, no scan, no
template reference. This makes it the simplest of the two sentinel columns (Group C /
`primary_attachment_id` has much more Go surface area).

## Files to change

### 1. `arx_go/utilities.go` — `checkOrphanPointers` doc comment only (lines ~151-154)
No logic change needed: the existing `p.%[1]s > 0` comparison is already NULL-safe (SQL Server
evaluates `NULL > 0` as unknown, which `WHERE` treats as false — same exclusion behavior as the
old `0` sentinel), so it continues to work correctly once `price_id` becomes NULL-based too.
Just update the doc comment (lines 151-154), which currently says "Unset values use a sentinel of
0 (or NULL for default_supplier_id)" — after this group, both `default_supplier_id` and
`price_id` are NULL-based; only `primary_attachment_id` (Group C, not part of this change) still
uses the `0` sentinel. Reword to something like: "Unset values use `NULL` for
`default_supplier_id`/`price_id`, and (until #735 Group C) a sentinel of `0` for
`primary_attachment_id`; the `> 0` guard excludes all of these since `NULL > 0` is never true."

### 2. `SQL/azure/part.sql`
Line 55: change
```sql
price_id            INT              CONSTRAINT DF_part_number_price_id         DEFAULT 0,    -- FK to price table; FK constraint deferred — see #213.
```
to a plain nullable column with no default (matches `default_supplier_id`'s style on line 56 —
`INT NULL` with no `DEFAULT`):
```sql
price_id            INT              NULL,                                             -- FK to price.id.
```
Add the FK after the existing `FK_part_number_uom` line (68):
```sql
ALTER TABLE dbo.part ADD CONSTRAINT FK_part_price FOREIGN KEY (price_id) REFERENCES dbo.price (id);
```
Update header comment line 13 (`-- price_id FKs to the price table; FK constraint deferred — see #213.`)
to `-- price_id FKs to the price table (real FK).`

### 3. `SQL/postgres/part.sql`
Line 39: change
```sql
price_id            INTEGER          DEFAULT 0,             -- FK to price; deferred (#213).
```
to
```sql
price_id            INTEGER          NULL,                  -- FK to price.id.
```
Add FK after line 50's `FK_part_number_uom`:
```sql
ALTER TABLE part ADD CONSTRAINT FK_part_price FOREIGN KEY (price_id) REFERENCES price (id);
```
Update header comment line 4 (already touched by Group A — if Group A already landed, just drop
`price_id` from the "still deferred" note; if applying standalone, update from `-- price_id /
default_supplier_id / uom_id FKs are mostly deferred - see #213.` to note price_id is no longer
deferred either).

### 4. New migration: `SQL/azure/migrations/migrate_735b_price_id_fk_promotion.sql`
Same structure/safety pattern as `migrate_742_form_record_unit_fk.sql` (USE ArxDev pin, idempotent
guards, orphan-check comment before the ADD CONSTRAINT, Postgres-equivalent comments). Three steps,
in order:

```sql
USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- 1. Convert the 0-sentinel to NULL (the semantic meaning of "no price" is unchanged;
--    only its DB representation moves from 0 to NULL).
UPDATE dbo.part SET price_id = NULL WHERE price_id = 0;
-- Postgres: UPDATE part SET price_id = NULL WHERE price_id = 0;

-- 2. Drop the DEFAULT 0 constraint (name may vary if it was never explicitly named on a given
--    DB — guard on existence).
IF EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_part_number_price_id')
    ALTER TABLE dbo.part DROP CONSTRAINT DF_part_number_price_id;
-- Postgres: ALTER TABLE part ALTER COLUMN price_id DROP DEFAULT;

-- 3. Promote to a real FK. ORPHAN CHECK FIRST on a live DB — must return zero rows:
--      SELECT p.id, p.price_id FROM dbo.part p
--      LEFT JOIN dbo.price pr ON pr.id = p.price_id
--      WHERE p.price_id IS NOT NULL AND pr.id IS NULL;
IF OBJECT_ID('dbo.FK_part_price', 'F') IS NULL
    ALTER TABLE dbo.part ADD CONSTRAINT FK_part_price FOREIGN KEY (price_id) REFERENCES dbo.price (id);
-- Postgres: ALTER TABLE part ADD CONSTRAINT FK_part_price FOREIGN KEY (price_id) REFERENCES price (id);
```
No `ON DELETE` clause (defaults to `NO ACTION`).

**Do not bump `schema_version`** — this is a data representation change (0→NULL) plus an
additive-safe FK. An old binary never writes `price_id` at all (confirmed: no Go code path sets
it), so it can't violate the new constraint or be confused by the NULL vs 0 change.

### 5. `SQL/schema.md`
`part` row (~line 140): remove any "FK deferred" phrasing tied to `price_id` — it isn't currently
called out by name there beyond the `default_supplier_id` note already handled in Group A (grep
for `price_id` in schema.md before editing to confirm there's no separate mention needing an
update).

## Not in scope here
`arx_go/utilities.go`'s `checkOrphanPointers` change above is the only Go code touched — no
struct/template changes, since `price_id` has no other Go references.
`part.primary_attachment_id` — see Group C plan; do not combine, its Go surface is much larger.

## Success criteria
- `go build ./...`, `go vet ./...`, `go test ./...` pass.
- Migration is idempotent, single-batch (no `GO`), ArxDev-pinned, includes the orphan-check
  comment for the human to run first against ArxProd.
- Both SQL dialects updated and stay in sync.
