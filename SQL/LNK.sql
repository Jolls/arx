-- LNK: Links suppliers to parts, capturing sourcing details like pricing, lead time, and order quantities.
-- LNKSUID FKs to supplier.id. LNKPNID FKs to PN.PNID.
-- LNKMFRID / LNKMFRPNID support manufacturer information when the supplier is a distributor.
-- LNKChoice indicates supplier preference order for a part (1 = preferred, 2 = alternate, etc.).
-- LNKAtQty is the quantity at which LNKCurrentCost applies (for quantity-break pricing).
-- TRIGGER: trg_LNK_supplier_count fires after INSERT/UPDATE/DELETE and updates supplier.SUNumOfLNKs.
--          Do not update SUNumOfLNKs manually. See SQL/triggers.sql.

IF OBJECT_ID('dbo.LNK', 'U') IS NOT NULL DROP TABLE LNK;

CREATE TABLE LNK (
  LNKID           INT              PRIMARY KEY IDENTITY,
  LNKSUID         INT              NOT NULL,  -- FK to supplier.id.
  LNKMFRPNID      INT,             -- Manufacturer's part ID (when supplier is a distributor). TODO: clarify — FK to PN.PNID or separate manufacturer PN?
  LNKMFRID        INT,             -- Manufacturer ID. TODO: clarify — FK to a manufacturer table?
  LNKUNID         INT,             -- Unit of measure ID. TODO: clarify — FK to a units table?
  LNKPNID         INT              NOT NULL,  -- FK to PN.PNID (the internal part this link is for).
  LNKToPNID       INT,             -- TODO: clarify purpose — alternate/equivalent part link to another PN.PNID?
  LNKUse          BIT            CONSTRAINT DF_LNK_LNKUse    DEFAULT 1,  -- Whether this supplier link is active/in use.
  LNKLeadtime     VARCHAR(55),               -- Lead time as descriptive text (e.g. "4-6 weeks"). TODO: consider INT (days) if always numeric.
  LNKChoice       INT            CONSTRAINT DF_LNK_LNKChoice DEFAULT 1,  -- Supplier preference order for this part (1 = preferred).
  LNKVendorPN     VARCHAR(55),     -- Supplier's own part number.
  LNKVendorDesc   VARCHAR(100),    -- Supplier's description for this part.
  LNKAtQty        DECIMAL(11,2),   -- Quantity at which LNKCurrentCost applies.
  LNKRFQDate      DATE,            -- Date of last Request for Quote.
  LNKMinIncrement DECIMAL(11,2),   -- Minimum order increment.
  LNKCurrentCost  DECIMAL(11,2)  CONSTRAINT DF_LNK_LNKCurrentCost DEFAULT 0,  -- Current unit cost at LNKAtQty.
  LNKSetupCost    DECIMAL(11,2)    -- One-time setup / tooling cost.
);

ALTER TABLE dbo.LNK ADD CONSTRAINT FK_LNK_supplier FOREIGN KEY (LNKSUID) REFERENCES dbo.supplier (id);
ALTER TABLE dbo.LNK ADD CONSTRAINT FK_LNK_PN       FOREIGN KEY (LNKPNID) REFERENCES dbo.PN (PNID);
