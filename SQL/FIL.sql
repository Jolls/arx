-- FIL: File and URL attachments linked to a Part Number (PN).
-- Each row is one attachment. FILPNID links to PN.PNID.
-- FILFileName holds either a file path (UNC/local) or a full URL.
-- order_id controls display sort order within a part's attachment list.
-- Deletions are soft-delete only: SET is_active=0. Never hard-delete FIL rows.
-- TRIGGER: trg_FIL_part_count fires after INSERT/UPDATE/DELETE and updates PN.PNFILLinks (active rows only).
--          Do not update PNFILLinks manually. See SQL/triggers.sql.

IF OBJECT_ID('dbo.FIL', 'U') IS NOT NULL DROP TABLE FIL;

CREATE TABLE FIL (
  FILID       INT           PRIMARY KEY IDENTITY,
  FILPNID     INT NOT NULL     REFERENCES dbo.PN (PNID),  -- FK to PN.PNID.
  FILFileName VARCHAR(1000),  -- File path or URL.
  FILNotes    VARCHAR(500),   -- Description or notes.
  FILPNRev    VARCHAR(10),    -- Part revision this file is associated with.
  order_id    INT            CONSTRAINT DF_FIL_order_id  DEFAULT 1,  -- Display sort order. TODO: rename to FILOrder for naming consistency.
  is_active   BIT NOT NULL  CONSTRAINT DF_FIL_is_active DEFAULT 1
);
