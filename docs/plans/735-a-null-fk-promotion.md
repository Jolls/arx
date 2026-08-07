# #735 Group A — Promote NULL-based logical references to real FKs

Scope: `contact.company_id` → `company.id`, `part.default_supplier_id` → `company.id`.
Both are already nullable `INT` columns with no sentinel value — pure DDL, no Go code changes.
(`part.price_id` and `part.primary_attachment_id` are handled separately — Group B/C — because
they use a `DEFAULT 0` sentinel that needs a semantic 0→NULL rework first.)

## Files to change

### 1. `SQL/azure/contact.sql`
Add FK after the CREATE TABLE block (after line 23), matching the `company.sql` pattern of
appending `ALTER TABLE ... ADD CONSTRAINT` after the table body:
```sql
ALTER TABLE dbo.contact ADD CONSTRAINT FK_contact_company FOREIGN KEY (company_id) REFERENCES dbo.company (id);
```
Update the column comment on line 20 from `-- FK to company.id; FK constraint deferred — see #213.`
to `-- FK to company.id.` (drop the "deferred" note now that the constraint exists).
Update the header comment line 2 if it references deferral — currently reads "each contact belongs
to one company", no change needed there.

### 2. `SQL/azure/part.sql`
Add FK after the closing `ALTER TABLE dbo.part ADD CONSTRAINT FK_part_number_uom ...` line (line 68):
```sql
ALTER TABLE dbo.part ADD CONSTRAINT FK_part_default_supplier FOREIGN KEY (default_supplier_id) REFERENCES dbo.company (id);
```
Update the column comment on line 56 from `-- Preferred supplier for cost rollup (#465); FK to
company.id, deferred like price_id.` to `-- Preferred supplier for cost rollup (#465); FK to
company.id.`
Update the header comment lines 14-16 (default_supplier_id description) if it references deferral —
it doesn't currently, no change needed.

### 3. `SQL/postgres/contact.sql`
Same FK add, Postgres syntax (no `dbo.` schema prefix, matches existing `company.sql` postgres FK style):
```sql
ALTER TABLE contact ADD CONSTRAINT FK_contact_company FOREIGN KEY (company_id) REFERENCES company (id);
```
Same comment update on line 20 (`-- FK to company.id (FK deferred - see #213).` → `-- FK to company.id.`).

### 4. `SQL/postgres/part.sql`
```sql
ALTER TABLE part ADD CONSTRAINT FK_part_default_supplier FOREIGN KEY (default_supplier_id) REFERENCES company (id);
```
Update line 40 comment (`-- FK to company.id; deferred (#465/#213).` → `-- FK to company.id.`).
Also the header comment on line 4 (`price_id / default_supplier_id / uom_id FKs are mostly
deferred - see #213.`) — update to reflect that `default_supplier_id` is no longer deferred
(only `price_id` still is, until Group B lands): change to
`-- price_id FK is deferred - see #213 (default_supplier_id and uom_id already have real FKs).`

### 5. New migration: `SQL/azure/migrations/migrate_735a_null_fk_promotion.sql`
Follow the exact structure/idempotency/safety-comment pattern of
`SQL/azure/migrations/migrate_742_form_record_unit_fk.sql`: `USE ArxDev;` pin at top with the
same safety comment, `IF OBJECT_ID(...) IS NULL` guard per `ADD CONSTRAINT`, an orphan-check
`SELECT` comment immediately above each `ADD CONSTRAINT` for the human to run first on ArxProd,
and a trailing `-- Postgres:` comment block per statement giving the Postgres equivalent (for the
#625 migration). No `GO` batch separators (single-batch requirement).

Two FK additions, each preceded by its orphan-check SELECT comment:
```sql
-- Orphan check (run first on ArxProd; must return zero rows):
--   SELECT c.id, c.company_id FROM dbo.contact c
--   LEFT JOIN dbo.company co ON co.id = c.company_id
--   WHERE c.company_id IS NOT NULL AND co.id IS NULL;
IF OBJECT_ID('dbo.FK_contact_company', 'F') IS NULL
    ALTER TABLE dbo.contact ADD CONSTRAINT FK_contact_company FOREIGN KEY (company_id) REFERENCES dbo.company (id);
-- Postgres: ALTER TABLE contact ADD CONSTRAINT FK_contact_company FOREIGN KEY (company_id) REFERENCES company (id);

-- Orphan check (run first on ArxProd; must return zero rows):
--   SELECT p.id, p.default_supplier_id FROM dbo.part p
--   LEFT JOIN dbo.company co ON co.id = p.default_supplier_id
--   WHERE p.default_supplier_id IS NOT NULL AND co.id IS NULL;
IF OBJECT_ID('dbo.FK_part_default_supplier', 'F') IS NULL
    ALTER TABLE dbo.part ADD CONSTRAINT FK_part_default_supplier FOREIGN KEY (default_supplier_id) REFERENCES dbo.company (id);
-- Postgres: ALTER TABLE part ADD CONSTRAINT FK_part_default_supplier FOREIGN KEY (default_supplier_id) REFERENCES company (id);
```
No `ON DELETE` clause on either (defaults to `NO ACTION`, matching the CLAUDE.md rule that
parts/contacts soft-delete and must never cascade-delete).
Do not bump `schema_version` — this is additive-safe: old binaries never violate these new
constraints since they already only ever write valid company ids or NULL (no code path writes
a dangling reference today).

### 6. `SQL/schema.md`
Table reference section (~line 140), `part` row: change
`` `default_supplier_id` (issue #465) → `company.id` (FK deferred, like `price_id`) = the preferred supplier... `` 
to
`` `default_supplier_id` (issue #465) → `company.id` (FK enforced) = the preferred supplier... ``
(drop "deferred, like `price_id`" — `price_id` is still deferred, `default_supplier_id` no longer is).
`contact` row (~line 144) already reads `company_id` → `company.id` with no "deferred" language —
no change needed there.

## Success criteria
- `go build ./...` and `go vet ./...` still pass (no Go code touched, so this should be a no-op
  verification).
- `go test ./...` passes.
- Migration file follows the single-batch, idempotent, ArxDev-pinned, orphan-check-documented
  pattern from `migrate_742_form_record_unit_fk.sql`.
- Both SQL dialects (`SQL/azure/*.sql`, `SQL/postgres/*.sql`) stay in sync.

## Not in scope here
`part.price_id` and `part.primary_attachment_id` — see Group B and Group C plans (0→NULL sentinel
rework needed first, per the issue).
