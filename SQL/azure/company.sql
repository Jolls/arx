-- company: Supplier, manufacturer, and vendor records.
-- A company can be a supplier (is_supplier=1), a manufacturer (is_manufacturer=1), or both.
-- default_contact FKs to contact.id (the primary contact for this company).
-- SUNumOfLNKs and SUNumOfPOs are denormalized counts kept in sync by database triggers
-- (dbo.trg_supplier_part_company_count, dbo.trg_PO_company_count — see SQL/triggers.sql).

IF OBJECT_ID('dbo.company', 'U') IS NOT NULL DROP TABLE company;

CREATE TABLE company (
  id                INT            PRIMARY KEY IDENTITY,
  name              VARCHAR(127)   NOT NULL CONSTRAINT UQ_company_name UNIQUE,
  SUWeb             VARCHAR(127),                    -- dead column; see FUTURE_GOALS.md (drop SUWeb/SUContact1)
  SUContact1        VARCHAR(127),                     -- dead column; see FUTURE_GOALS.md (drop SUWeb/SUContact1)
  SUNotes           VARCHAR(4000),
  date_modified     DATETIME       CONSTRAINT DF_company_date_modified DEFAULT GETDATE(),
  is_active         BIT            CONSTRAINT DF_company_is_active       DEFAULT 1,
  is_supplier       BIT            CONSTRAINT DF_company_is_supplier     DEFAULT 1,  -- Can sell parts to you (distributor or direct manufacturer).
  is_manufacturer   BIT            CONSTRAINT DF_company_is_manufacturer DEFAULT 0,  -- Makes parts; may also be a supplier. Used to populate mfg_part.mfg_id.
  SUNumOfLNKs       INT            CONSTRAINT DF_company_SUNumOfLNKs DEFAULT 0, -- Denormalized count of supplier_part rows for this company.
  SUNumOfPOs        INT            CONSTRAINT DF_company_SUNumOfPOs  DEFAULT 0, -- Denormalized count of PO rows for this company.
  SUSupplierCode    VARCHAR(12),
  default_contact        INT            NULL,              -- FK to contact.id. NULL = no contact assigned.
  primary_attachment_id  INT            NULL,              -- FK to company_attachment.supplier_attachment_id.
  bulk_order_delimiter   VARCHAR(10)    CONSTRAINT DF_company_bulk_order_delimiter DEFAULT 'comma' NOT NULL, -- 'comma' | 'tab' | 'newline'; PO bulk-order copy-to-clipboard (#80)
  bulk_order_pn_source   VARCHAR(10)    CONSTRAINT DF_company_bulk_order_pn_source DEFAULT 'internal' NOT NULL -- 'internal' | 'vendor'; which PN the bulk-order copy uses
);

ALTER TABLE dbo.company ADD CONSTRAINT FK_company_default_contact    FOREIGN KEY (default_contact)        REFERENCES dbo.contact (id);
ALTER TABLE dbo.company ADD CONSTRAINT FK_company_primary_attachment FOREIGN KEY (primary_attachment_id) REFERENCES dbo.company_attachment (supplier_attachment_id);
