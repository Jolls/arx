-- build: one manufacturing build event — consume components, produce output (#675).
-- Part of the #568 lot control epic. Each row records building `qty` of the output
-- part (`part_id`) from its `bom` component lines. The build handler writes, in one
-- transaction: an inventory_transaction 'issue' row per component consumed and one
-- 'receipt' row for the output produced (see recordInventoryTxn), keeping
-- part.stock_on_hand in sync on both sides.
-- output_lot_id is a nullable placeholder for the produced lot; lot control is a
-- later PR in the epic, so there is no lot table or FK yet.

IF OBJECT_ID('dbo.build', 'U') IS NOT NULL DROP TABLE dbo.build;

CREATE TABLE build (
  id             INT           PRIMARY KEY IDENTITY,
  part_id        INT           NOT NULL,                                          -- FK to part.id (output/parent part built).
  output_lot_id  INT           NULL,                                             -- Produced lot; wired to lot table in a later PR (#568).
  qty            DECIMAL(15,5) NOT NULL DEFAULT 1,                               -- Number of output parts built.
  build_date     DATE          NOT NULL CONSTRAINT DF_build_date DEFAULT GETDATE(),
  username       VARCHAR(128)  NOT NULL CONSTRAINT DF_build_user DEFAULT '',     -- App user login handle.
  note           VARCHAR(MAX),                                                   -- Optional free-text comment.
  created_at     DATETIME      NOT NULL CONSTRAINT DF_build_created DEFAULT GETDATE()
);

ALTER TABLE dbo.build ADD CONSTRAINT FK_build_part FOREIGN KEY (part_id) REFERENCES dbo.part (id);
CREATE INDEX IX_build_part ON dbo.build (part_id, build_date);

-- FK_inv_txn_build (inventory_transaction.build_id → build.id, #677) is added here,
-- after build is created, so inventory_transaction can be created first in DDL run
-- order (mirrors how FK_inv_txn_lot is added in SQL/lot.sql after lot).
ALTER TABLE dbo.inventory_transaction ADD CONSTRAINT FK_inv_txn_build FOREIGN KEY (build_id) REFERENCES dbo.build (id);
