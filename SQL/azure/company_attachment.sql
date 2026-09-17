-- company_attachment: File and URL attachments linked to a company (supplier or manufacturer).
-- Each row is one attachment. supplier_id references company.id.
-- file_path holds either a LOCAL:... path or a full https:// URL.
-- sort_order controls display order within a company's attachment list.

IF OBJECT_ID('dbo.company_attachment', 'U') IS NOT NULL DROP TABLE dbo.company_attachment;
CREATE TABLE dbo.company_attachment (
    supplier_attachment_id  INT            PRIMARY KEY IDENTITY,
    supplier_id             INT            NOT NULL,
    file_path               NVARCHAR(1024) NOT NULL,
    notes                   NVARCHAR(512),
    sort_order              INT,
    is_active               BIT            NOT NULL CONSTRAINT DF_company_attachment_is_active DEFAULT 1
);
