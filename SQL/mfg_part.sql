-- mfg_part: One row per (internal part, manufacturer, manufacturer PN).
-- Tracks the manufacturer's identity and their part number for an internal part.
-- When a sourcing link in supplier_part is via a distributor, supplier_part.mfg_part_id
-- points here to identify who actually makes the part and under what PN.
-- part_id FKs to part_number.id. mfg_id FKs to company.id.
-- Use company.is_manufacturer = 1 to identify manufacturer-role companies.

IF OBJECT_ID('dbo.mfg_part', 'U') IS NOT NULL DROP TABLE mfg_part;

CREATE TABLE mfg_part (
  id               INT           PRIMARY KEY IDENTITY,
  part_id          INT           NOT NULL,    -- FK to part_number.id.
  mfg_id           INT           NOT NULL,    -- FK to company.id (the manufacturer).
  mfg_part_number  VARCHAR(100)  NOT NULL,    -- Manufacturer's part number.
  description      VARCHAR(250),             -- Manufacturer's description for this part.
  is_active        BIT           NOT NULL  DEFAULT 1,  -- Soft-delete; 0 = deleted.
);

ALTER TABLE dbo.mfg_part ADD CONSTRAINT FK_mfg_part_pn  FOREIGN KEY (part_id) REFERENCES dbo.part_number (id);
ALTER TABLE dbo.mfg_part ADD CONSTRAINT FK_mfg_part_mfg FOREIGN KEY (mfg_id)  REFERENCES dbo.company (id);
-- One active row per (part, manufacturer, mfg_part_number) combination.
-- Filtered index allows re-adding an MPN after soft-delete.
CREATE UNIQUE INDEX UQ_mfg_part_combo ON dbo.mfg_part (part_id, mfg_id, mfg_part_number) WHERE is_active = 1;
