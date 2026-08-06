-- migrate_742_form_record_unit_fk.sql
-- Traceability epic (#736) slice 5 (#742): add the nullable `form_record.unit_id` FK (Q8) and
-- promote the `part_id` logical reference to a real FK (§4.4). See
-- docs/plans/736-traceability-data-model.md §4.1 / §4.4 / §6 Q8 / §7 slice 5 and
-- SQL/form_record.sql.
--
-- ADDITIVE: `unit_id` is a new nullable column (NULL on every existing whole-lot/batch record;
-- set only on a unit-testing/retest record). No form_record<->unit m2m — at most one unit per
-- record, so a plain FK, no join table. The FK-consistency invariant (Q8: read lot/build THROUGH
-- the unit when unit_id is set) is an app-layer rule enforced in slice 8; a CHECK can't span the
-- FK join, so nothing here enforces it. Depends on slice 3's `unit` table (#740) existing.
--
-- part_id has always been a logical reference; promoting it to a real FK requires that no
-- form_record row points at a missing part. The seed is clean; on a live DB a human runs the
-- orphan check below FIRST and reconciles any hits before the ADD CONSTRAINT (which fails loudly
-- on an orphan rather than silently). Kept ON DELETE NO ACTION (the default), matching #677.
--
-- BACKWARD-COMPATIBLE — schema_version is deliberately NOT bumped. unit_id is a new nullable
-- column no pre-#742 binary references, and the two new FK constraints reject only rows that
-- point at a missing part/unit, which the old binary never writes. An old binary keeps running
-- against the migrated DB, so the mismatch banner must not fire (mirrors slice 3 / #740).
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line — nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on its old/new state; safe to re-run). Postgres equivalents
-- follow each block in comments (for the #625 migration).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. Add the nullable unit_id column (Q8).
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.form_record', 'unit_id') IS NULL
    ALTER TABLE dbo.form_record ADD unit_id INT NULL;
-- Postgres: ALTER TABLE form_record ADD COLUMN IF NOT EXISTS unit_id INTEGER NULL;

-- ------------------------------------------------------------------
-- 2. unit_id FK + supporting index (reverse lookup: a unit's records / retests).
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.FK_form_record_unit', 'F') IS NULL
    ALTER TABLE dbo.form_record ADD CONSTRAINT FK_form_record_unit FOREIGN KEY (unit_id) REFERENCES dbo.unit (id);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = 'IX_form_record_unit' AND object_id = OBJECT_ID('dbo.form_record'))
    CREATE INDEX IX_form_record_unit ON dbo.form_record (unit_id);
-- Postgres:
--   ALTER TABLE form_record ADD CONSTRAINT FK_form_record_unit FOREIGN KEY (unit_id) REFERENCES unit (id);
--   CREATE INDEX IF NOT EXISTS IX_form_record_unit ON form_record (unit_id);

-- ------------------------------------------------------------------
-- 3. Promote part_id to a real FK (§4.4). ORPHAN CHECK FIRST on a live DB — this must return
--    zero rows before the ADD CONSTRAINT below, or reconcile the offending records:
--        SELECT fr.id, fr.part_id FROM dbo.form_record fr
--        LEFT JOIN dbo.part p ON p.id = fr.part_id
--        WHERE fr.part_id IS NOT NULL AND p.id IS NULL;
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.FK_form_record_part', 'F') IS NULL
    ALTER TABLE dbo.form_record ADD CONSTRAINT FK_form_record_part FOREIGN KEY (part_id) REFERENCES dbo.part (id);
-- Postgres: ALTER TABLE form_record ADD CONSTRAINT FK_form_record_part FOREIGN KEY (part_id) REFERENCES part (id);
