-- supplier_part: Sourcing links — maps internal parts to supplier catalog entries.
-- One row per (part, supplier, supplier_pn) sourcing option.
-- TRIGGER: trg_supplier_part_supplier_count fires after INSERT/UPDATE/DELETE
--          and updates supplier.SUNumOfLNKs. Do not update SUNumOfLNKs manually.
--          See SQL/triggers.sql.

IF OBJECT_ID('dbo.supplier_part', 'U') IS NOT NULL DROP TABLE supplier_part;

CREATE TABLE supplier_part (

  -- Identity
  id                 INT            PRIMARY KEY IDENTITY,

  -- Foreign keys
  supplier_id        INT            NOT NULL,    -- FK to supplier.id (who you buy from).
  part_id            INT            NOT NULL,    -- FK to PN.PNID (the internal part).
  mfg_part_id        INT,                        -- FK to mfg_part.id. NULL = buying direct or manufacturer unknown.
  -- unit_id removed — needs a units-of-measure table first (#314)
  -- alt_part_id removed — alternate/substitute parts is separate work (#278)

  -- Supplier catalog info
  supplier_pn        VARCHAR(55),                -- Supplier's own part number / SKU.
  supplier_desc      VARCHAR(100),               -- Supplier's description for this part.

  -- Purchasing
  -- current_cost / at_qty removed — covered by the price table (#304)
  min_increment      DECIMAL(11,2),              -- Minimum order increment (qty, not price).
  setup_cost         DECIMAL(11,2),              -- One-time setup / tooling cost.
  lead_time          VARCHAR(55),                -- Lead time as descriptive text (e.g. "4-6 weeks"). TODO: consider INT (days).
  -- rfq_date removed — superseded by full RFQ workflow (#270)

  -- Status
  is_active          BIT            CONSTRAINT DF_supplier_part_is_active  DEFAULT 1,
  preference         INT            CONSTRAINT DF_supplier_part_preference DEFAULT 1,  -- Supplier preference order (1 = preferred, 2 = alternate, etc.).

);

ALTER TABLE dbo.supplier_part ADD CONSTRAINT FK_supplier_part_supplier FOREIGN KEY (supplier_id)  REFERENCES dbo.supplier (id);
ALTER TABLE dbo.supplier_part ADD CONSTRAINT FK_supplier_part_pn       FOREIGN KEY (part_id)      REFERENCES dbo.PN (PNID);
ALTER TABLE dbo.supplier_part ADD CONSTRAINT FK_supplier_part_mfg_part FOREIGN KEY (mfg_part_id)  REFERENCES dbo.mfg_part (id);
