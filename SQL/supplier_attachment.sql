-- supplier_attachment: File and URL attachments linked to a supplier.
-- Each row is one attachment. supplier_id references supplier.id.
-- file_path holds either a LOCAL:... path or a full https:// URL.
-- sort_order controls display order within a supplier's attachment list.

IF OBJECT_ID('dbo.supplier_attachment', 'U') IS NOT NULL DROP TABLE dbo.supplier_attachment;
CREATE TABLE dbo.supplier_attachment (
    supplier_attachment_id  INT            PRIMARY KEY IDENTITY,
    supplier_id             INT            NOT NULL,
    file_path               NVARCHAR(1024) NOT NULL,
    notes                   NVARCHAR(512),
    sort_order              INT,
    is_active               BIT            NOT NULL CONSTRAINT DF_supplier_attachment_is_active DEFAULT 1
);

-- Test variant
IF OBJECT_ID('dbo.supplier_attachment_Test', 'U') IS NOT NULL DROP TABLE dbo.supplier_attachment_Test;
CREATE TABLE dbo.supplier_attachment_Test (
    supplier_attachment_id  INT            PRIMARY KEY IDENTITY,
    supplier_id             INT            NOT NULL,
    file_path               NVARCHAR(1024) NOT NULL,
    notes                   NVARCHAR(512),
    sort_order              INT,
    is_active               BIT            NOT NULL DEFAULT 1  -- Test table: auto-named constraint is acceptable.
);
