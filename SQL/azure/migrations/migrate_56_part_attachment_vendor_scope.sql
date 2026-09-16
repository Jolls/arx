-- migrate_56_part_attachment_vendor_scope.sql
-- #56: scope a part attachment to one of the part's vendor links, so the Suppliers and
-- Mfg Parts tabs can show the attachments that belong to each supplier/manufacturer row.
-- Adds nullable `part_attachment.supplier_part_id` and `part_attachment.mfg_part_id`.
-- See SQL/azure/part_attachment.sql.
--
-- ADDITIVE: both columns are nullable and NULL on every existing row, which is exactly
-- today's semantics — an attachment with both NULL is a plain part-level attachment and
-- keeps showing on /part/{id}/attachments as before. At most one may be set (CK below).
--
-- ON DELETE SET NULL on both FKs: supplier_part rows are hard-deleted (mfg_part is
-- soft-deleted), so without it, deleting a supplier link that has a scoped attachment
-- would fail on the constraint. Falling back to part-level loses no attachment.
--
-- BACKWARD-COMPATIBLE — schema_version is deliberately NOT bumped. Both columns are new
-- and nullable, so an INSERT from an old binary (which names neither) still succeeds, and
-- the constraints reject only values no old binary ever writes. An old binary keeps running
-- against the migrated DB, so the mismatch banner must not fire.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line — nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on its old/new state; safe to re-run). Postgres equivalents
-- follow each block in comments (for the #625 migration).
--
-- Runs as a single batch (no `GO`): some clients — e.g. the Azure portal's query editor — send
-- the whole script to the server as one batch and don't split on `GO`, so a plain statement
-- referencing `supplier_part_id`/`mfg_part_id` right after the columns are added fails with
-- "Invalid column name" (batch compilation resolves column names against pre-batch metadata).
-- Steps 2 and 3 use dynamic SQL (`EXEC(N'...')`) to defer that resolution to runtime.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. Add the two nullable vendor-scope columns.
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.part_attachment', 'supplier_part_id') IS NULL
    ALTER TABLE dbo.part_attachment ADD supplier_part_id INT NULL;
IF COL_LENGTH('dbo.part_attachment', 'mfg_part_id') IS NULL
    ALTER TABLE dbo.part_attachment ADD mfg_part_id INT NULL;
-- Postgres:
--   ALTER TABLE part_attachment ADD COLUMN IF NOT EXISTS supplier_part_id INTEGER NULL;
--   ALTER TABLE part_attachment ADD COLUMN IF NOT EXISTS mfg_part_id INTEGER NULL;

-- ------------------------------------------------------------------
-- 2. FKs (deferred via dynamic SQL — see header note). Both columns are brand new, so no
--    existing row can be an orphan and no pre-`ADD CONSTRAINT` orphan check is needed.
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.FK_part_attachment_supplier_part', 'F') IS NULL
    EXEC(N'ALTER TABLE dbo.part_attachment ADD CONSTRAINT FK_part_attachment_supplier_part FOREIGN KEY (supplier_part_id) REFERENCES dbo.supplier_part (id) ON DELETE SET NULL');
IF OBJECT_ID('dbo.FK_part_attachment_mfg_part', 'F') IS NULL
    EXEC(N'ALTER TABLE dbo.part_attachment ADD CONSTRAINT FK_part_attachment_mfg_part FOREIGN KEY (mfg_part_id) REFERENCES dbo.mfg_part (id) ON DELETE SET NULL');
-- Postgres:
--   ALTER TABLE part_attachment ADD CONSTRAINT FK_part_attachment_supplier_part FOREIGN KEY (supplier_part_id) REFERENCES supplier_part (id) ON DELETE SET NULL;
--   ALTER TABLE part_attachment ADD CONSTRAINT FK_part_attachment_mfg_part FOREIGN KEY (mfg_part_id) REFERENCES mfg_part (id) ON DELETE SET NULL;

-- ------------------------------------------------------------------
-- 3. At most one scope may be set (deferred via dynamic SQL — see header note).
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.CK_part_attachment_vendor_scope', 'C') IS NULL
    EXEC(N'ALTER TABLE dbo.part_attachment ADD CONSTRAINT CK_part_attachment_vendor_scope CHECK (supplier_part_id IS NULL OR mfg_part_id IS NULL)');
-- Postgres: ALTER TABLE part_attachment ADD CONSTRAINT CK_part_attachment_vendor_scope CHECK (supplier_part_id IS NULL OR mfg_part_id IS NULL);
