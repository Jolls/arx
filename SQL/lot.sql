-- lot: one lot (batch) instance of a part (#676, part of the #568 lot control epic).
-- A row is created at goods receipt for a lot-tracked purchased part (po_line_id set)
-- or by a build that produces a lot-tracked output part (po_line_id NULL). Auto-issued
-- lot_number defaults to the lot's own id (#687, unique by construction); lot_description
-- carries the human-readable provenance ("PO <number>" / "Build #<id>") instead.
-- vendor_lot_number captures the supplier's own lot/batch ID for purchased lots.
-- part.is_lot_tracked gates which parts get a lot; see SQL/part.sql.
-- Requires part and po_line to exist first (FKs below), and build to exist for the
-- deferred output-lot FK at the bottom.

IF OBJECT_ID('dbo.lot', 'U') IS NOT NULL DROP TABLE dbo.lot;

CREATE TABLE lot (
  id                 INT           PRIMARY KEY IDENTITY,
  part_id            INT           NOT NULL,                                          -- FK to part.id (the part this lot is of).
  lot_number         VARCHAR(255)  NOT NULL CONSTRAINT DF_lot_number    DEFAULT '',   -- Internal lot #; auto-generated lots default to the lot's own id (unique by construction), editable.
  lot_description    VARCHAR(255)  NOT NULL CONSTRAINT DF_lot_desc      DEFAULT '',   -- Human-readable provenance: "PO <number>" (purchased), "Build #<id>" (manufactured), "Manual entry" (adjustment tab).
  vendor_lot_number  VARCHAR(255)  NULL,                                              -- Supplier's own lot/batch ID (purchased lots); NULL otherwise.
  source             VARCHAR(10)   NULL CONSTRAINT CK_lot_source CHECK (source IN ('purchase', 'build', 'adjust')), -- How the lot originated (#744): purchase (receipt), build (manufactured), adjust (manual/cycle-count). Today inferred from po_line_id / owning build; made explicit. NULL = not yet classified (set going forward in the epic's slice-8 code).
  po_line_id         INT           NULL,                                              -- FK to po_line.id for purchased receipts; NULL for manufactured lots.
  created_at         DATETIME      NOT NULL CONSTRAINT DF_lot_created   DEFAULT GETDATE(),
  is_active          BIT           NOT NULL CONSTRAINT DF_lot_is_active DEFAULT 1
);

ALTER TABLE dbo.lot ADD CONSTRAINT FK_lot_part    FOREIGN KEY (part_id)    REFERENCES dbo.part (id);
ALTER TABLE dbo.lot ADD CONSTRAINT FK_lot_po_line FOREIGN KEY (po_line_id) REFERENCES dbo.po_line (id);
CREATE INDEX IX_lot_part ON dbo.lot (part_id, is_active);

-- Wire the lot_id references that other tables carry, now that lot exists. Added here
-- rather than in each table's own DDL so those tables can be created before lot in the
-- run order:
--   build.output_lot_id (#675 placeholder → lot, #676)
--   inventory_transaction.lot_id (#676): lot a receipt/issue/adjustment touched
ALTER TABLE dbo.build ADD CONSTRAINT FK_build_output_lot FOREIGN KEY (output_lot_id) REFERENCES dbo.lot (id);
ALTER TABLE dbo.inventory_transaction ADD CONSTRAINT FK_inv_txn_lot FOREIGN KEY (lot_id) REFERENCES dbo.lot (id);
