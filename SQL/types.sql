-- part_types: Defines the categories of parts in the PartsMaster catalog.
-- Controls which features are enabled per type (BOM list, supplier/file links, etc.).
-- TODO: populate with initial rows for known PNType values (PS, DWG, DOC, CAT, FORM, PL).
-- WIP: this table does not yet exist in the live database.

IF OBJECT_ID('dbo.part_types', 'U') IS NOT NULL DROP TABLE part_types;

CREATE TABLE part_types (
  id        INT          PRIMARY KEY IDENTITY,
  name      VARCHAR(127) UNIQUE,                    -- TODO: add NOT NULL.
  has_list  INT          NOT NULL DEFAULT 0,        -- 1 = part has a BOM / parts-made-from list.
  has_links INT          NOT NULL DEFAULT 0         -- 1 = part has supplier info, file, or web links.
);