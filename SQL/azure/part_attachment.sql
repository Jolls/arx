-- part_attachment: File and URL attachments linked to a part.
-- Each row is one attachment. part_id links to part.id.
-- file_name holds either a file path (UNC/local) or a full URL.
-- sort_order controls display sort order within a part's attachment list.
-- supplier_part_id / mfg_part_id optionally scope an attachment to one of the part's
-- vendor links (#56); both NULL = a plain part-level attachment.
-- Deletions are soft-delete only: SET is_active=0. Never hard-delete part_attachment rows.
-- TRIGGER: trg_FIL_part_count fires after INSERT/UPDATE/DELETE and updates part.attachment_count (active rows only).
--          Do not update attachment_count manually. See SQL/triggers.sql.
--
-- Renamed from FIL in db-table-rename commit 3. Run SQL/migrations/migrate_rename_part_attachment.sql.
-- Go struct fields intentionally retain old names (FILID, FILFileName, etc.) — reads are positional.
--
-- Prior migrations (historical reference):
--   #313 — FILNotes → category rename
--   #297 — FILPNID VARCHAR → INT FK

IF OBJECT_ID('dbo.part_attachment', 'U') IS NOT NULL DROP TABLE dbo.part_attachment;

CREATE TABLE part_attachment (
  id             INT           PRIMARY KEY IDENTITY,
  part_id        INT NOT NULL  REFERENCES dbo.part (id),  -- FK to part.id.
  file_name      VARCHAR(1000),  -- File path or URL.
  category       VARCHAR(500),   -- Attachment category (e.g. Datasheet, Drawing). Options managed via app_config 'attachment_categories'. (Live length 500, inherited from the former FILNotes column.)
  part_revision  VARCHAR(10),    -- Part revision this file is associated with.
  sort_order     INT            CONSTRAINT DF_part_attachment_sort_order DEFAULT 1,  -- Display sort order.
  is_active      BIT NOT NULL  CONSTRAINT DF_part_attachment_is_active  DEFAULT 1,
  comment        VARCHAR(500),   -- Free-text note about this attachment (#585).

  -- Vendor scope (#56). At most one may be set; both NULL = part-level attachment.
  -- ON DELETE SET NULL: supplier_part rows are hard-deleted, so a scoped attachment
  -- reverts to part-level rather than blocking the delete.
  supplier_part_id INT  CONSTRAINT FK_part_attachment_supplier_part
                        REFERENCES dbo.supplier_part (id) ON DELETE SET NULL,
  mfg_part_id      INT  CONSTRAINT FK_part_attachment_mfg_part
                        REFERENCES dbo.mfg_part (id) ON DELETE SET NULL,

  CONSTRAINT CK_part_attachment_vendor_scope
    CHECK (supplier_part_id IS NULL OR mfg_part_id IS NULL)
);
