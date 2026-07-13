-- lot: one lot (batch) instance of a part (#676, part of the #568 lot control epic).
-- A row is created at goods receipt for a lot-tracked purchased part (po_line_id set,
-- lot_number defaults to the PO number) or by a build that produces a lot-tracked
-- output part (po_line_id NULL, lot_number defaults to the build reference).
-- vendor_lot_number captures the supplier's own lot/batch ID for purchased lots.
-- part.is_lot_tracked gates which parts get a lot; see SQL/part.sql.
-- Requires part and po_line to exist first (FKs below), and build to exist for the
-- deferred output-lot FK at the bottom.

IF OBJECT_ID('dbo.lot', 'U') IS NOT NULL DROP TABLE dbo.lot;

CREATE TABLE lot (
  id                 INT           PRIMARY KEY IDENTITY,
  part_id            INT           NOT NULL,                                          -- FK to part.id (the part this lot is of).
  lot_number         VARCHAR(255)  NOT NULL CONSTRAINT DF_lot_number    DEFAULT '',   -- Internal lot #; defaults to PO number (purchased) / build ref (manufactured), editable.
  vendor_lot_number  VARCHAR(255)  NULL,                                              -- Supplier's own lot/batch ID (purchased lots); NULL otherwise.
  po_line_id         INT           NULL,                                              -- FK to po_line.id for purchased receipts; NULL for manufactured lots.
  created_at         DATETIME      NOT NULL CONSTRAINT DF_lot_created   DEFAULT GETDATE(),
  is_active          BIT           NOT NULL CONSTRAINT DF_lot_is_active DEFAULT 1
);

ALTER TABLE dbo.lot ADD CONSTRAINT FK_lot_part    FOREIGN KEY (part_id)    REFERENCES dbo.part (id);
ALTER TABLE dbo.lot ADD CONSTRAINT FK_lot_po_line FOREIGN KEY (po_line_id) REFERENCES dbo.po_line (id);
CREATE INDEX IX_lot_part ON dbo.lot (part_id, is_active);

-- build.output_lot_id was a nullable placeholder (#675); wire it to lot now that the
-- table exists (#676). Added here rather than in build.sql so build can be created
-- before lot in the DDL run order.
ALTER TABLE dbo.build ADD CONSTRAINT FK_build_output_lot FOREIGN KEY (output_lot_id) REFERENCES dbo.lot (id);
