-- supplier: Supplier / vendor records.
-- default_contact FKs to CN.CNID (the primary contact for this supplier).
-- SUNumOfLNKs and SUNumOfPOs are denormalized counts kept in sync by database triggers
-- (dbo.trg_LNK_supplier_count, dbo.trg_PO_supplier_count — see SQL/triggers.sql).

IF OBJECT_ID('dbo.supplier', 'U') IS NOT NULL DROP TABLE supplier;

CREATE TABLE supplier (
  id                INT            PRIMARY KEY IDENTITY,
  name              VARCHAR(127)   NOT NULL CONSTRAINT UQ_supplier_name UNIQUE,
  SUWeb             VARCHAR(127),                    -- TODO: drop — confirmed dead; no Go or VBA references; superseded by supplier_attachment. Migration: ALTER TABLE supplier DROP COLUMN SUWeb;
  SUContact1        VARCHAR(127),                     -- TODO: drop — confirmed dead; no Go or VBA references; superseded by default_contact FK to CN. Migration: ALTER TABLE supplier DROP COLUMN SUContact1;
  SUNotes           VARCHAR(4000),
  date_modified     DATETIME       CONSTRAINT DF_supplier_date_modified DEFAULT GETDATE(),
  is_active         BIT            CONSTRAINT DF_supplier_is_active DEFAULT 1,
  SUNumOfLNKs       INT            CONSTRAINT DF_supplier_SUNumOfLNKs DEFAULT 0, -- Denormalized count of LNK rows for this supplier.
  SUNumOfPOs        INT            CONSTRAINT DF_supplier_SUNumOfPOs  DEFAULT 0, -- Denormalized count of PO rows for this supplier.
  SUSupplierCode    VARCHAR(12),
  default_contact        INT            NULL,              -- FK to CN.CNID. NULL = no contact assigned.
  primary_attachment_id  INT            NULL               -- FK to supplier_attachment.supplier_attachment_id.
);

ALTER TABLE dbo.supplier ADD CONSTRAINT FK_supplier_default_contact    FOREIGN KEY (default_contact)        REFERENCES dbo.CN (CNID);
ALTER TABLE dbo.supplier ADD CONSTRAINT FK_supplier_primary_attachment FOREIGN KEY (primary_attachment_id) REFERENCES dbo.supplier_attachment (supplier_attachment_id);
