-- part_types: Superseded — this table was never created in the live database.
-- Capabilities (BOM, etc.) are now encoded as BIT columns directly on PN (e.g. has_bom).
-- Category labels are stored in PN.category with a CHECK constraint. See part_number.sql.

IF OBJECT_ID('dbo.part_types', 'U') IS NOT NULL DROP TABLE part_types;

CREATE TABLE part_types (
  id        INT          PRIMARY KEY IDENTITY,
  name      VARCHAR(127) UNIQUE,                    -- TODO: add NOT NULL.
  has_list  INT          NOT NULL DEFAULT 0,        -- 1 = part has a BOM / parts-made-from list.
  has_links INT          NOT NULL DEFAULT 0         -- 1 = part has supplier info, file, or web links.
);