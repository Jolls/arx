-- po_line: Purchase Order Line items (renamed from POL in db-table-rename commit 5).
-- Each row is one line on a PO.
-- po_id links to purchase_order.id (the parent PO).
-- part_id links to part.id (the ordered part). part_number_snapshot is denormalized for historical reference.
-- vendor_part_number is the supplier's own part number for this item (see also supplier_part.supplier_pn).
-- TRIGGER: trg_POL_part_count fires after INSERT/UPDATE/DELETE and updates part.po_line_count.
--          Do not update po_line_count manually. See SQL/triggers.sql.

IF OBJECT_ID('dbo.po_line', 'U') IS NOT NULL DROP TABLE po_line;

CREATE TABLE po_line (
  id                   INT            PRIMARY KEY IDENTITY,
  po_id                INT            NOT NULL,       -- FK to purchase_order.id.
  part_number_snapshot VARCHAR(255),                 -- Denormalized part number at time of order.
  revision_snapshot    VARCHAR(10),                  -- Denormalized revision at time of order. Snapshot of part.revision.
  part_id              INT,                           -- FK to part.id. Nullable — line items may not map to a catalog part.
  line_number          INT            NOT NULL,       -- Line item number on the PO.
  description          VARCHAR(MAX),                 -- Description at time of order.
  qty                  DECIMAL(11,2)  NOT NULL,       -- Quantity ordered.
  unit_cost            DECIMAL(16,8)  NOT NULL DEFAULT 0, -- Unit cost at time of order.
  vendor_part_number   VARCHAR(55),                   -- Supplier's part number for this item.
);

ALTER TABLE dbo.po_line ADD CONSTRAINT FK_po_line_po   FOREIGN KEY (po_id)   REFERENCES dbo.purchase_order (id);
ALTER TABLE dbo.po_line ADD CONSTRAINT FK_po_line_part FOREIGN KEY (part_id) REFERENCES dbo.part (id);
