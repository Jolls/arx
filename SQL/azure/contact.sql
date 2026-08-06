-- contact: Contacts associated with suppliers.
-- company_id links to company.id — each contact belongs to one company.

IF OBJECT_ID('dbo.contact', 'U') IS NOT NULL DROP TABLE dbo.contact;

CREATE TABLE contact (
  id                  INT            PRIMARY KEY IDENTITY,
  display_name        VARCHAR(127)   NOT NULL,
  address             VARCHAR(255),
  city                VARCHAR(64),
  zipcode             VARCHAR(10),
  state               VARCHAR(64),
  country             VARCHAR(127),
  phone_1             VARCHAR(32),
  phone_2             VARCHAR(32),
  fax                 VARCHAR(32),
  website             VARCHAR(500),                  -- URL for supplier/contact website.
  email               VARCHAR(127),
  is_active           BIT            DEFAULT 1,
  company_id          INT,                           -- FK to company.id; FK constraint deferred — see #213.
  updated_at          DATETIME       CONSTRAINT DF_contact_updated_at DEFAULT GETDATE(),
  notes               VARCHAR(4000)
);
