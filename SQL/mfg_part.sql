-- mfg_part: One row per (internal part, manufacturer, manufacturer PN).
-- Tracks the manufacturer's identity and their part number for an internal part.
-- When a sourcing link in LNK is via a distributor, LNK.mfg_part_id
-- points here to identify who actually makes the part and under what PN.
-- part_id FKs to PN.PNID. mfg_id FKs to supplier.id.
-- Use supplier.is_manufacturer = 1 to identify manufacturer-role companies.

IF OBJECT_ID('dbo.mfg_part', 'U') IS NOT NULL DROP TABLE mfg_part;

CREATE TABLE mfg_part (
  id               INT           PRIMARY KEY IDENTITY,
  part_id          INT           NOT NULL,    -- FK to PN.PNID.
  mfg_id           INT           NOT NULL,    -- FK to supplier.id (the manufacturer).
  mfg_part_number  VARCHAR(100)  NOT NULL,    -- Manufacturer's part number.
  description      VARCHAR(250),             -- Manufacturer's description for this part.
);

ALTER TABLE dbo.mfg_part ADD CONSTRAINT FK_mfg_part_pn  FOREIGN KEY (part_id) REFERENCES dbo.PN (PNID);
ALTER TABLE dbo.mfg_part ADD CONSTRAINT FK_mfg_part_mfg FOREIGN KEY (mfg_id)  REFERENCES dbo.supplier (id);
-- One row per (part, manufacturer, mfg_part_number) combination.
CREATE UNIQUE INDEX UQ_mfg_part_combo ON dbo.mfg_part (part_id, mfg_id, mfg_part_number);
