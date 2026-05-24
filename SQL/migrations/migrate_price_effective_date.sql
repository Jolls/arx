-- migrate_price_effective_date.sql
-- Adds effective_date to the price table and replaces the plain unique constraint
-- with a filtered unique index (active rows only), enabling price history via
-- inactive rows.
-- Run against the PartsMaster database.

-- ============================================================
-- STEP 1: ADD COLUMN
-- ============================================================

ALTER TABLE dbo.price      ADD effective_date DATE NOT NULL CONSTRAINT DF_price_effective_date      DEFAULT GETDATE();
ALTER TABLE dbo.price_Test ADD effective_date DATE NOT NULL CONSTRAINT DF_price_Test_effective_date DEFAULT GETDATE();

-- ============================================================
-- STEP 2: REPLACE UNIQUE CONSTRAINT WITH FILTERED UNIQUE INDEX
-- Only active rows (is_active = 1) are subject to uniqueness.
-- Inactive rows are retained as price history.
-- ============================================================

-- prod
IF EXISTS (SELECT 1 FROM sys.indexes WHERE name = 'UQ_price_part_supplier_pack' AND object_id = OBJECT_ID('dbo.price'))
    ALTER TABLE dbo.price DROP CONSTRAINT UQ_price_part_supplier_pack;
CREATE UNIQUE INDEX UQ_price_active_combo ON dbo.price (part_id, supplier_id, pack_size) WHERE is_active = 1;

-- test
IF EXISTS (SELECT 1 FROM sys.indexes WHERE name = 'UQ_price_Test_part_supplier_pack' AND object_id = OBJECT_ID('dbo.price_Test'))
    ALTER TABLE dbo.price_Test DROP CONSTRAINT UQ_price_Test_part_supplier_pack;
CREATE UNIQUE INDEX UQ_price_Test_active_combo ON dbo.price_Test (part_id, supplier_id, pack_size) WHERE is_active = 1;
