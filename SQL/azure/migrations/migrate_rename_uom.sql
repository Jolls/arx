-- migrate_rename_uom.sql
-- Traceability epic (#736) slice 2 (#739, absorbs #712): rename the unit-of-measure
-- reference table and its FK columns, freeing the `unit` name for the Tier-3 serialized
-- instance table (see docs/plans/736-traceability-data-model.md §3). Pure renames, no
-- behavior change:
--     unit                  -> uom
--     unit.unit_id          -> uom.uom_id      (PK)
--     part.unit_id          -> part.uom_id
--     supplier_part.unit_id -> supplier_part.uom_id
-- and renames the embedded FK constraint names to match, plus the unit_Test clone table.
-- Bumps schema_version 5 -> 6.
--
-- No stored named_queries reference `unit`/`unit_id`, so none need rewriting here (sp_rename
-- does not touch named_queries row data — verified none exist for this rename).
--
-- NON-BACKWARDS-COMPATIBLE: a pre-#739 binary queries these tables/columns by name. The Go
-- change (config.UnitTable() -> "uom", uom_id column literals) and this migration must ship
-- together — an older binary breaks after the rename, and this build breaks against
-- un-renamed objects. schema_version 5 -> 6 gates the rollout via the mismatch banner. Run
-- once every client is on a #739 build.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line — nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on the old object existing / new not existing; safe to
-- re-run). Postgres equivalents follow each block in comments (for the #625 migration).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. Rename the table (and its _Test clone).
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.unit', 'U') IS NOT NULL AND OBJECT_ID('dbo.uom', 'U') IS NULL
    EXEC sp_rename 'dbo.unit', 'uom';
IF OBJECT_ID('dbo.unit_Test', 'U') IS NOT NULL AND OBJECT_ID('dbo.uom_Test', 'U') IS NULL
    EXEC sp_rename 'dbo.unit_Test', 'uom_Test';
-- Postgres: ALTER TABLE unit RENAME TO uom;   (no _Test clone — see SQL/postgres/README.md)

-- ------------------------------------------------------------------
-- 2. Rename the PK column unit_id -> uom_id (after the table rename above). The FK
--    constraints on part/supplier_part follow the referenced column automatically.
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.uom', 'unit_id') IS NOT NULL AND COL_LENGTH('dbo.uom', 'uom_id') IS NULL
    EXEC sp_rename 'dbo.uom.unit_id', 'uom_id', 'COLUMN';
IF COL_LENGTH('dbo.uom_Test', 'unit_id') IS NOT NULL AND COL_LENGTH('dbo.uom_Test', 'uom_id') IS NULL
    EXEC sp_rename 'dbo.uom_Test.unit_id', 'uom_id', 'COLUMN';
-- Postgres: ALTER TABLE uom RENAME COLUMN unit_id TO uom_id;

-- ------------------------------------------------------------------
-- 3. Rename the FK columns on part and supplier_part.
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.part', 'unit_id') IS NOT NULL AND COL_LENGTH('dbo.part', 'uom_id') IS NULL
    EXEC sp_rename 'dbo.part.unit_id', 'uom_id', 'COLUMN';
IF COL_LENGTH('dbo.supplier_part', 'unit_id') IS NOT NULL AND COL_LENGTH('dbo.supplier_part', 'uom_id') IS NULL
    EXEC sp_rename 'dbo.supplier_part.unit_id', 'uom_id', 'COLUMN';
-- Postgres:
--   ALTER TABLE part          RENAME COLUMN unit_id TO uom_id;
--   ALTER TABLE supplier_part RENAME COLUMN unit_id TO uom_id;

-- ------------------------------------------------------------------
-- 4. Rename the embedded FK constraint names to match. Cosmetic (constraints keep
--    functioning under their old names), guarded on the old-named object existing.
-- ------------------------------------------------------------------
IF OBJECT_ID('FK_part_number_unit', 'F')  IS NOT NULL EXEC sp_rename 'FK_part_number_unit',  'FK_part_number_uom';
IF OBJECT_ID('FK_supplier_part_unit', 'F') IS NOT NULL EXEC sp_rename 'FK_supplier_part_unit', 'FK_supplier_part_uom';
-- Postgres:
--   ALTER TABLE part          RENAME CONSTRAINT FK_part_number_unit  TO FK_part_number_uom;
--   ALTER TABLE supplier_part RENAME CONSTRAINT FK_supplier_part_unit TO FK_supplier_part_uom;

-- ------------------------------------------------------------------
-- 5. Bump schema_version 5 -> 6 (gates the rollout; guarded, only advances 5 -> 6).
-- ------------------------------------------------------------------
UPDATE dbo.app_config
   SET setting_value = '6'
 WHERE setting_key = 'schema_version'
   AND setting_value = '5';
