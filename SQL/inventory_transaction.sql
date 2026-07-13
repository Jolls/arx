-- inventory_transaction: append-only ledger of stock movements (issues #272 / #274).
-- Every receipt, issue, adjustment, or count is one row with a SIGNED qty
--   (+ receipt, - issue, +/- adjustment), so a part's on-hand = SUM(qty).
-- part.stock_on_hand is a cached copy of that sum, maintained by the Go app in the
--   same transaction that writes a row here (see recordInventoryTxn) — not a trigger.
-- username holds the app user's login handle (consistent with PO_history.changed_by).
-- po_line_id links a 'receipt' back to the originating PO line (NULL otherwise).
-- lot_id links a movement to the lot it touched (#676): the lot created on a receipt,
--   the component lot consumed by a build 'issue', or the output lot produced by a
--   build 'receipt'; NULL for non-lot-tracked parts. FK to lot.id — see SQL/lot.sql,
--   which must exist before this table's lot FK is added (the standalone DDL below
--   creates lot separately; the guarded migration orders them).
-- reference is a free-text pointer (PO number, count sheet, note) for display/search.

IF OBJECT_ID('dbo.inventory_transaction', 'U') IS NOT NULL DROP TABLE dbo.inventory_transaction;

CREATE TABLE inventory_transaction (
  id          INT          PRIMARY KEY IDENTITY,
  part_id     INT          NOT NULL,                                                       -- FK to part.id.
  txn_type    VARCHAR(20)  NOT NULL CONSTRAINT CK_inv_txn_type CHECK (txn_type IN ('receipt','issue','adjustment','count')),
  qty         DECIMAL(16,8) NOT NULL,                                                      -- Signed: + adds to stock, - removes.
  txn_date    DATE         NOT NULL CONSTRAINT DF_inv_txn_date DEFAULT GETDATE(),          -- Effective date of the movement.
  username    VARCHAR(128) NOT NULL CONSTRAINT DF_inv_txn_user DEFAULT '',                 -- App user login handle.
  reference   VARCHAR(255),                                                               -- PO number / count sheet / free note.
  note        VARCHAR(MAX),                                                               -- Adjustment reason or comment.
  po_line_id  INT          NULL,                                                          -- FK to po_line.id for receipts (#269); NULL otherwise.
  lot_id      INT          NULL,                                                          -- FK to lot.id (#676): lot touched by this movement; NULL if part not lot-tracked.
  created_at  DATETIME     NOT NULL CONSTRAINT DF_inv_txn_created DEFAULT GETDATE()
);

ALTER TABLE dbo.inventory_transaction ADD CONSTRAINT FK_inv_txn_PN  FOREIGN KEY (part_id)    REFERENCES dbo.part (id);
ALTER TABLE dbo.inventory_transaction ADD CONSTRAINT FK_inv_txn_po_line FOREIGN KEY (po_line_id) REFERENCES dbo.po_line (id);
-- FK_inv_txn_lot (lot_id → lot.id) is added in SQL/lot.sql, after lot is created, so
-- this table can be created before lot in the DDL run order (like build's output-lot FK).
CREATE INDEX IX_inv_txn_part ON dbo.inventory_transaction (part_id, txn_date);
