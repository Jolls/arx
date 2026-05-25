-- FIL: File and URL attachments linked to a Part Number (PN).
-- Each row is one attachment. FILPNID links to PN.PNID.
-- FILFileName holds either a file path (UNC/local) or a full URL.
-- order_id controls display sort order within a part's attachment list.
-- Deletions are soft-delete only: SET is_active=0. Never hard-delete FIL rows.
-- TRIGGER: trg_FIL_part_count fires after INSERT/UPDATE/DELETE and updates PN.PNFILLinks (active rows only).
--          Do not update PNFILLinks manually. See SQL/triggers.sql.

-- Migration (run once on live DB):
--   EXEC sp_rename 'dbo.FIL.FILNotes',      'category', 'COLUMN';
--   EXEC sp_rename 'dbo.FIL_Test.FILNotes',  'category', 'COLUMN';
--   INSERT INTO app_config (setting_key, setting_value) VALUES ('attachment_categories', 'Vendor Link,Drawing,CAD,Datasheet,Vendor Document,Fabrication,Schematic,Quote,BOM,SOP,Certificate,Photo');
--   INSERT INTO app_config_Test (setting_key, setting_value) VALUES ('attachment_categories', 'Vendor Link,Drawing,CAD,Datasheet,Vendor Document,Fabrication,Schematic,Quote,BOM,SOP,Certificate,Photo');

IF OBJECT_ID('dbo.FIL', 'U') IS NOT NULL DROP TABLE FIL;

CREATE TABLE FIL (
  FILID       INT           PRIMARY KEY IDENTITY,
  FILPNID     INT NOT NULL     REFERENCES dbo.PN (PNID),  -- FK to PN.PNID.
  FILFileName VARCHAR(1000),  -- File path or URL.
  category    VARCHAR(100),   -- Attachment category (e.g. Datasheet, Drawing). Options managed via app_config 'attachment_categories'.
  FILPNRev    VARCHAR(10),    -- Part revision this file is associated with.
  order_id    INT            CONSTRAINT DF_FIL_order_id  DEFAULT 1,  -- Display sort order. TODO: rename to FILOrder for naming consistency.
  is_active   BIT NOT NULL  CONSTRAINT DF_FIL_is_active DEFAULT 1
);
