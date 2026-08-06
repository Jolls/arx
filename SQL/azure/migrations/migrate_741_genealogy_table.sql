-- migrate_741_genealogy_table.sql
-- Traceability epic (#736) slice 4 (#741): widen `lot_genealogy` → `genealogy` so one edge
-- table records provenance for both lots and serialized units. See
-- docs/plans/736-traceability-data-model.md §4.1 / §6 Q11 / §7 slice 4 and SQL/genealogy.sql.
--
-- Adds `parent_unit_id`/`child_unit_id` (nullable FKs to `unit`), relaxes the lot columns to
-- NULL, and adds the exactly-one-parent / exactly-one-child CHECKs (Q11): every edge names
-- precisely one parent FK and one child FK. ADDITIVE for data — existing lot→lot rows stay
-- valid (unit columns NULL, one lot parent + one lot child satisfies both CHECKs). No code
-- writes unit endpoints until slice 8; this slice only reshapes the table + renames it.
--
-- NON-BACKWARDS-COMPATIBLE: a pre-#741 binary queries the table by its old name
-- `lot_genealogy` (config.LotGenealogyTable). The Go change (GenealogyTable → "genealogy")
-- and this migration must ship together — an older binary breaks after the rename. Bumps
-- schema_version 7 -> 8 to gate the rollout via the mismatch banner. Depends on slice 3's
-- `unit` table (#740) existing for the new FKs.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line — nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on its old/new state; safe to re-run). Postgres equivalents
-- follow each block in comments (for the #625 migration).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. Rename the table lot_genealogy -> genealogy. FKs, indexes, and the default follow the
--    table automatically (sp_rename keeps their names); step 6 renames them for parity with
--    the from-scratch DDL. Guarded on the old table existing and the new one not.
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.lot_genealogy', 'U') IS NOT NULL AND OBJECT_ID('dbo.genealogy', 'U') IS NULL
    EXEC sp_rename 'dbo.lot_genealogy', 'genealogy';
-- Postgres: ALTER TABLE lot_genealogy RENAME TO genealogy;

-- ------------------------------------------------------------------
-- 2. Add the unit endpoint columns (nullable FKs to unit).
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.genealogy', 'parent_unit_id') IS NULL
    ALTER TABLE dbo.genealogy ADD parent_unit_id INT NULL;
IF COL_LENGTH('dbo.genealogy', 'child_unit_id') IS NULL
    ALTER TABLE dbo.genealogy ADD child_unit_id INT NULL;
-- Postgres:
--   ALTER TABLE genealogy ADD COLUMN IF NOT EXISTS parent_unit_id INTEGER NULL;
--   ALTER TABLE genealogy ADD COLUMN IF NOT EXISTS child_unit_id  INTEGER NULL;

-- ------------------------------------------------------------------
-- 3. Relax the lot columns to NULL (an edge may now be unit-parented / unit-childed instead).
--    Nullability-only change; the columns are indexed but the type is unchanged, so this is
--    allowed. Re-running is harmless (sets NULL columns to NULL).
-- ------------------------------------------------------------------
ALTER TABLE dbo.genealogy ALTER COLUMN parent_lot_id INT NULL;
ALTER TABLE dbo.genealogy ALTER COLUMN child_lot_id  INT NULL;
-- Postgres:
--   ALTER TABLE genealogy ALTER COLUMN parent_lot_id DROP NOT NULL;
--   ALTER TABLE genealogy ALTER COLUMN child_lot_id  DROP NOT NULL;

-- ------------------------------------------------------------------
-- 4. Unit FKs + indexes on the new columns (mirrors the existing lot FKs/indexes).
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.FK_gen_parent_unit', 'F') IS NULL
    ALTER TABLE dbo.genealogy ADD CONSTRAINT FK_gen_parent_unit FOREIGN KEY (parent_unit_id) REFERENCES dbo.unit (id);
IF OBJECT_ID('dbo.FK_gen_child_unit', 'F') IS NULL
    ALTER TABLE dbo.genealogy ADD CONSTRAINT FK_gen_child_unit  FOREIGN KEY (child_unit_id)  REFERENCES dbo.unit (id);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = 'IX_gen_parent_unit' AND object_id = OBJECT_ID('dbo.genealogy'))
    CREATE INDEX IX_gen_parent_unit ON dbo.genealogy (parent_unit_id);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = 'IX_gen_child_unit' AND object_id = OBJECT_ID('dbo.genealogy'))
    CREATE INDEX IX_gen_child_unit ON dbo.genealogy (child_unit_id);
-- Postgres:
--   ALTER TABLE genealogy ADD CONSTRAINT FK_gen_parent_unit FOREIGN KEY (parent_unit_id) REFERENCES unit (id);
--   ALTER TABLE genealogy ADD CONSTRAINT FK_gen_child_unit  FOREIGN KEY (child_unit_id)  REFERENCES unit (id);
--   CREATE INDEX IF NOT EXISTS IX_gen_parent_unit ON genealogy (parent_unit_id);
--   CREATE INDEX IF NOT EXISTS IX_gen_child_unit  ON genealogy (child_unit_id);

-- ------------------------------------------------------------------
-- 5. Exactly-one-parent / exactly-one-child CHECKs (Q11). Existing lot→lot rows satisfy both
--    (one lot col set, both unit cols NULL). Written portably (SUM of CASEs = 1) so the same
--    text works on SQL Server and Postgres.
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.CK_gen_one_parent', 'C') IS NULL
    ALTER TABLE dbo.genealogy ADD CONSTRAINT CK_gen_one_parent CHECK (
        (CASE WHEN parent_lot_id  IS NULL THEN 0 ELSE 1 END) +
        (CASE WHEN parent_unit_id IS NULL THEN 0 ELSE 1 END) = 1);
IF OBJECT_ID('dbo.CK_gen_one_child', 'C') IS NULL
    ALTER TABLE dbo.genealogy ADD CONSTRAINT CK_gen_one_child CHECK (
        (CASE WHEN child_lot_id  IS NULL THEN 0 ELSE 1 END) +
        (CASE WHEN child_unit_id IS NULL THEN 0 ELSE 1 END) = 1);
-- Postgres: identical ADD CONSTRAINT ... CHECK blocks (CASE expression is dialect-agnostic).

-- ------------------------------------------------------------------
-- 6. Rename the inherited lot constraints/indexes/default to the `gen` prefix, matching the
--    from-scratch DDL (SQL/genealogy.sql) so a migrated DB and a fresh build agree. Each
--    guarded on the old name still existing.
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.FK_lot_gen_parent', 'F') IS NOT NULL AND OBJECT_ID('dbo.FK_gen_parent_lot', 'F') IS NULL
    EXEC sp_rename 'dbo.FK_lot_gen_parent', 'FK_gen_parent_lot', 'OBJECT';
IF OBJECT_ID('dbo.FK_lot_gen_child', 'F') IS NOT NULL AND OBJECT_ID('dbo.FK_gen_child_lot', 'F') IS NULL
    EXEC sp_rename 'dbo.FK_lot_gen_child', 'FK_gen_child_lot', 'OBJECT';
IF OBJECT_ID('dbo.DF_lot_gen_qty', 'D') IS NOT NULL AND OBJECT_ID('dbo.DF_gen_qty', 'D') IS NULL
    EXEC sp_rename 'dbo.DF_lot_gen_qty', 'DF_gen_qty', 'OBJECT';
IF EXISTS (SELECT 1 FROM sys.indexes WHERE name = 'IX_lot_gen_parent' AND object_id = OBJECT_ID('dbo.genealogy'))
    EXEC sp_rename 'dbo.genealogy.IX_lot_gen_parent', 'IX_gen_parent_lot', 'INDEX';
IF EXISTS (SELECT 1 FROM sys.indexes WHERE name = 'IX_lot_gen_child' AND object_id = OBJECT_ID('dbo.genealogy'))
    EXEC sp_rename 'dbo.genealogy.IX_lot_gen_child', 'IX_gen_child_lot', 'INDEX';
-- Postgres:
--   ALTER TABLE genealogy RENAME CONSTRAINT FK_lot_gen_parent TO FK_gen_parent_lot;
--   ALTER TABLE genealogy RENAME CONSTRAINT FK_lot_gen_child  TO FK_gen_child_lot;
--   ALTER INDEX IX_lot_gen_parent RENAME TO IX_gen_parent_lot;
--   ALTER INDEX IX_lot_gen_child  RENAME TO IX_gen_child_lot;
--   (the qty default is unnamed in the Postgres DDL — nothing to rename.)

-- ------------------------------------------------------------------
-- 7. Bump schema_version 7 -> 8 (gates the rollout; guarded, only advances 7 -> 8).
-- ------------------------------------------------------------------
UPDATE dbo.app_config
   SET setting_value = '8'
 WHERE setting_key = 'schema_version'
   AND setting_value = '7';
