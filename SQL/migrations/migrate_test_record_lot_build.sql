-- migrate_test_record_lot_build.sql
-- Link test records to the lot/build that produced their unit (issue #677, part of
-- the #568 lot-genealogy epic). Adds:
--   * test_record.lot_id           → lot.id    (the lot the tested unit belongs to)
--   * test_record.build_id         → build.id  (the build that produced the unit)
--   * inventory_transaction.build_id → build.id (the build that wrote the ledger row,
--                                       replacing the free-text "Build #N" note pointer)
--
-- SAFETY: this script is pinned to ArxDev via the USE below. To apply it to ArxProd,
-- remove (or change) that single USE line — nothing else in the script names a database.
--
-- Idempotent (guarded, safe to re-run). Additive and rollback-safe: all three columns
-- are nullable and a pre-#677 binary never references them. The build FKs are skipped
-- (guarded) if the #675 build table does not yet exist and can be applied later.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- 1. test_record.lot_id → lot.id
IF COL_LENGTH('dbo.test_record', 'lot_id') IS NULL
    ALTER TABLE dbo.test_record ADD lot_id INT NULL;
IF OBJECT_ID('dbo.lot', 'U') IS NOT NULL
   AND OBJECT_ID('dbo.FK_test_record_lot', 'F') IS NULL
    ALTER TABLE dbo.test_record ADD CONSTRAINT FK_test_record_lot
        FOREIGN KEY (lot_id) REFERENCES dbo.lot (id);

-- 2. test_record.build_id → build.id
IF COL_LENGTH('dbo.test_record', 'build_id') IS NULL
    ALTER TABLE dbo.test_record ADD build_id INT NULL;
IF OBJECT_ID('dbo.build', 'U') IS NOT NULL
   AND OBJECT_ID('dbo.FK_test_record_build', 'F') IS NULL
    ALTER TABLE dbo.test_record ADD CONSTRAINT FK_test_record_build
        FOREIGN KEY (build_id) REFERENCES dbo.build (id);

-- 3. inventory_transaction.build_id → build.id
IF COL_LENGTH('dbo.inventory_transaction', 'build_id') IS NULL
    ALTER TABLE dbo.inventory_transaction ADD build_id INT NULL;
IF OBJECT_ID('dbo.build', 'U') IS NOT NULL
   AND OBJECT_ID('dbo.FK_inv_txn_build', 'F') IS NULL
    ALTER TABLE dbo.inventory_transaction ADD CONSTRAINT FK_inv_txn_build
        FOREIGN KEY (build_id) REFERENCES dbo.build (id);
