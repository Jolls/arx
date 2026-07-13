-- migrate_lot_control.sql
-- Lot / batch control (issue #676, part of the #568 lot-genealogy epic). Adds the
-- part.is_lot_tracked flag and the lot + lot_genealogy tables, and wires the
-- build.output_lot_id placeholder (added in #675) to the new lot table.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (guarded, safe to re-run).
-- Additive and rollback-safe: one new column (NOT NULL DEFAULT 0) + two new tables
-- + one FK; no existing data is changed. Statements are ordered by dependency
-- (part → lot → lot_genealogy → build FK), so this single file resolves the run
-- order that the standalone SQL/lot.sql / SQL/lot_genealogy.sql reference DDL leaves
-- to the caller.
--
-- Requires the #675 build table to already exist for the output-lot FK; if it does
-- not, that one ALTER is skipped (guarded) and can be applied later.

-- 1. part.is_lot_tracked — gates which parts get a lot at receipt/build.
IF COL_LENGTH('dbo.part', 'is_lot_tracked') IS NULL
    ALTER TABLE dbo.part ADD is_lot_tracked BIT NOT NULL
        CONSTRAINT DF_part_number_is_lot_tracked DEFAULT 0;

-- 2. lot — one batch instance of a lot-tracked part.
IF OBJECT_ID('dbo.lot', 'U') IS NULL
BEGIN
    CREATE TABLE lot (
      id                 INT           PRIMARY KEY IDENTITY,
      part_id            INT           NOT NULL,
      lot_number         VARCHAR(255)  NOT NULL CONSTRAINT DF_lot_number    DEFAULT '',
      vendor_lot_number  VARCHAR(255)  NULL,
      po_line_id         INT           NULL,
      created_at         DATETIME      NOT NULL CONSTRAINT DF_lot_created   DEFAULT GETDATE(),
      is_active          BIT           NOT NULL CONSTRAINT DF_lot_is_active DEFAULT 1
    );
    ALTER TABLE dbo.lot ADD CONSTRAINT FK_lot_part    FOREIGN KEY (part_id)    REFERENCES dbo.part (id);
    ALTER TABLE dbo.lot ADD CONSTRAINT FK_lot_po_line FOREIGN KEY (po_line_id) REFERENCES dbo.po_line (id);
    CREATE INDEX IX_lot_part ON dbo.lot (part_id, is_active);
END

-- 3. lot_genealogy — edges linking a consumed parent lot to the child lot built from it.
IF OBJECT_ID('dbo.lot_genealogy', 'U') IS NULL
BEGIN
    CREATE TABLE lot_genealogy (
      id             INT            PRIMARY KEY IDENTITY,
      parent_lot_id  INT            NOT NULL,
      child_lot_id   INT            NOT NULL,
      qty_consumed   DECIMAL(15,5)  NOT NULL CONSTRAINT DF_lot_gen_qty DEFAULT 0
    );
    ALTER TABLE dbo.lot_genealogy ADD CONSTRAINT FK_lot_gen_parent FOREIGN KEY (parent_lot_id) REFERENCES dbo.lot (id);
    ALTER TABLE dbo.lot_genealogy ADD CONSTRAINT FK_lot_gen_child  FOREIGN KEY (child_lot_id)  REFERENCES dbo.lot (id);
    CREATE INDEX IX_lot_gen_child  ON dbo.lot_genealogy (child_lot_id);
    CREATE INDEX IX_lot_gen_parent ON dbo.lot_genealogy (parent_lot_id);
END

-- 4. build.output_lot_id → lot (the #675 placeholder, wired now that lot exists).
IF OBJECT_ID('dbo.build', 'U') IS NOT NULL
   AND OBJECT_ID('dbo.FK_build_output_lot', 'F') IS NULL
    ALTER TABLE dbo.build ADD CONSTRAINT FK_build_output_lot
        FOREIGN KEY (output_lot_id) REFERENCES dbo.lot (id);

-- 5. inventory_transaction.lot_id → lot (#676): the lot a receipt/issue/adjustment
--    touched. Nullable; NULL for non-lot-tracked parts and all legacy rows.
IF COL_LENGTH('dbo.inventory_transaction', 'lot_id') IS NULL
    ALTER TABLE dbo.inventory_transaction ADD lot_id INT NULL;
IF OBJECT_ID('dbo.FK_inv_txn_lot', 'F') IS NULL
    ALTER TABLE dbo.inventory_transaction ADD CONSTRAINT FK_inv_txn_lot
        FOREIGN KEY (lot_id) REFERENCES dbo.lot (id);
