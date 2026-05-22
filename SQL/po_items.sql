-- POL: Purchase Order Line items.
-- Each row is one line on a PO.
-- POLPOID links to PO.id (the parent PO).
-- POLPNID links to PN.PNID (the ordered part). POLPNPartNumber is denormalized for historical reference.
-- VendorPN is the supplier's own part number for this item (see also LNK.LNKVendorPN).
-- TRIGGER: trg_POL_part_count fires after INSERT/UPDATE/DELETE and updates PN.PNPOLinks.
--          Do not update PNPOLinks manually. See SQL/triggers.sql.

IF OBJECT_ID('dbo.POL', 'U') IS NOT NULL DROP TABLE POL;

CREATE TABLE POL (
  POLID           INT            PRIMARY KEY IDENTITY,
  POLPOID         INT            NOT NULL,       -- FK to PO.id.
  POLPNPartNumber VARCHAR(255),                  -- Denormalized part number at time of order.
  POLPNID         INT,                           -- FK to PN.PNID. Nullable — line items may not map to a catalog part.
  POLItem         INT            NOT NULL,        -- Line item number on the PO.
  POLDesc         VARCHAR(MAX),                  -- Description at time of order.
  POLQty          DECIMAL(11,2)  NOT NULL,        -- Quantity ordered.
  POLCost         DECIMAL(16,8)  NOT NULL DEFAULT 0, -- Unit cost at time of order.
  VendorPN        VARCHAR(55),                   -- Supplier's part number for this item.
);

ALTER TABLE dbo.POL ADD CONSTRAINT FK_POL_PO FOREIGN KEY (POLPOID) REFERENCES dbo.PO (id);
ALTER TABLE dbo.POL ADD CONSTRAINT FK_POL_PN FOREIGN KEY (POLPNID) REFERENCES dbo.PN (PNID);
