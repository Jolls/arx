-- CN: Contacts associated with suppliers.
-- CNSUID links to supplier.id — each contact belongs to one supplier.
-- CNUserAccountLink holds the associated Windows/network account name for internal users.

IF OBJECT_ID('dbo.CN', 'U') IS NOT NULL DROP TABLE CN;

CREATE TABLE CN (
  CNID                INT            PRIMARY KEY IDENTITY,
  CNName              VARCHAR(127)   NOT NULL,
  CNAddress           VARCHAR(255),
  CNCity              VARCHAR(64),
  CNZipcode           VARCHAR(10),
  CNState             VARCHAR(64),
  CNCountry           VARCHAR(127),
  CNPhone1            VARCHAR(32),
  CNPhone2            VARCHAR(32),
  CNFAX               VARCHAR(32),
  CNWeb               VARCHAR(500),                  -- URL for supplier/contact website.
  CNEmail             VARCHAR(127),
  CNActive            BIT            DEFAULT 1,
  CNSUID              INT,                           -- FK to supplier.id. TODO: add FK constraint.
  CNUserAccountLink   VARCHAR(127),                  -- Associated Windows/network account for internal users.
  CNDateModified      DATETIME       CONSTRAINT DF_CN_CNDateModified DEFAULT GETDATE(),
  CNNotes             VARCHAR(4000)
);
