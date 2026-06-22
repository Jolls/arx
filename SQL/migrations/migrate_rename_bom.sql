-- migrate_rename_bom.sql
-- Commit 2 of db-table-rename effort: rename table PL → bom and all columns
-- to snake_case per go-forward convention. See docs/plans/db-table-rename-plan.md.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (each step guarded, safe to re-run).
-- sp_rename preserves all indexes, constraints, and FKs automatically.
--
-- No data is changed. The only schema change is naming.

-- ============================================================
-- STEP 1: RENAME TABLE
-- ============================================================

IF OBJECT_ID('dbo.PL', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.PL', 'bom';

-- ============================================================
-- STEP 2: RENAME COLUMNS
-- ============================================================

IF COL_LENGTH('dbo.bom', 'PLID') IS NOT NULL
    EXEC sp_rename 'dbo.bom.PLID', 'id', 'COLUMN';

IF COL_LENGTH('dbo.bom', 'PLListID') IS NOT NULL
    EXEC sp_rename 'dbo.bom.PLListID', 'parent_part_id', 'COLUMN';

IF COL_LENGTH('dbo.bom', 'PLPartID') IS NOT NULL
    EXEC sp_rename 'dbo.bom.PLPartID', 'component_part_id', 'COLUMN';

IF COL_LENGTH('dbo.bom', 'PLItem') IS NOT NULL
    EXEC sp_rename 'dbo.bom.PLItem', 'line_number', 'COLUMN';

IF COL_LENGTH('dbo.bom', 'PLQty') IS NOT NULL
    EXEC sp_rename 'dbo.bom.PLQty', 'qty', 'COLUMN';

-- ============================================================
-- STEP 3: RENAME CONSTRAINTS THAT EMBED THE OLD NAMES
-- ============================================================

-- PK constraint (auto-named PK__PL__* — sp_rename finds it by the old table's PK name)
-- FK constraints renamed to drop the "PL_PN" prefix
IF OBJECT_ID('FK_PL_PN_List') IS NOT NULL
    EXEC sp_rename 'FK_PL_PN_List', 'FK_bom_PN_parent';

IF OBJECT_ID('FK_PL_PN_Part') IS NOT NULL
    EXEC sp_rename 'FK_PL_PN_Part', 'FK_bom_PN_component';
