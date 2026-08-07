-- part: Part Numbers — the core parts catalog (renamed from PN in db-table-rename commit 6;
--   table renamed part_number → part in commit 8). The part_number COLUMN (the human-readable
--   PN string) keeps its name; only the table was renamed.
-- category: descriptive label for what kind of part this is (ASM, BUY, DWG, DOC, FORM, MFG, OPS, RAW, SVC, TOOL).
--   Constrained by CK_part_number_category — see CHECK constraint below.
--   OPS = an operation/labor line (e.g. Assembler, Test Engineer); current_cost holds the hourly rate,
--   added to an assembly's BOM with qty = hours so the rollup includes value-add (#465).
-- release_status: U = Under Review, A = Active (Released), D = Deprecated (Obsolete).
-- attachment_count and po_line_count are denormalized counts maintained by DB triggers — do not update them in code.
-- po_line_count: trg_POL_part_count fires on po_line INSERT/UPDATE/DELETE (see SQL/triggers.sql).
-- attachment_count: trg_FIL_part_count fires on part_attachment INSERT/UPDATE/DELETE, counts is_active=1 rows only.
-- user_field_1-10 are configurable user-defined fields.
-- price_id FKs to the price table (real FK).
-- default_supplier_id: the preferred supplier for cost rollup (#465). The rollup uses the cheapest
--   active price row from this supplier as the part's leaf cost (falls back to current_cost when NULL).
--   Auto-set to the first supplier a price is added for; NULL only when the part has no suppliers.
-- NOTE: Go struct fields still use the old PN-prefixed names (e.g. Part.PNID, .PNReqBy);
--       only the DB columns were renamed. See SQL/schema.md.
-- has_bom column dropped (#555) — BOM presence is computed on demand via EXISTS(bom) instead
-- of a denormalized flag. Drop migration: SQL/migrations/migrate_drop_has_bom.sql (#540).

IF OBJECT_ID('dbo.part', 'U') IS NOT NULL DROP TABLE part;

CREATE TABLE part (
  id                  INT              PRIMARY KEY IDENTITY,
  part_number         VARCHAR(255)     NOT NULL CONSTRAINT UQ_part_number_part_number UNIQUE,  -- live DB is nullable (pre-existing); NOT NULL is the intent.
  category            VARCHAR(10)      CONSTRAINT DF_part_number_category         DEFAULT 'BUY'
                                       CONSTRAINT CK_part_number_category         CHECK (category IN ('', 'ASM', 'BUY', 'DWG', 'DOC', 'FORM', 'MFG', 'OPS', 'RAW', 'SVC', 'TOOL')),  -- '' permitted for legacy/uncategorized rows (matches live).
  revision            VARCHAR(10)      CONSTRAINT DF_part_number_revision         DEFAULT '',   -- NOT NULL deferred; see #213.
  title               VARCHAR(255)     CONSTRAINT DF_part_number_title            DEFAULT '',
  detail              VARCHAR(255)     CONSTRAINT DF_part_number_detail           DEFAULT '',
  release_status      VARCHAR(255)     NOT NULL CONSTRAINT DF_part_number_release_status   DEFAULT 'U'
                                       CONSTRAINT CK_part_number_release_status CHECK (release_status IN ('U','A','D')),  -- NOT NULL + default 'U' (#542); CHECK added #540/#542; VARCHAR(255) narrowing deferred — see #213.
  requested_by        VARCHAR(50)      CONSTRAINT DF_part_number_requested_by     DEFAULT '',
  notes               VARCHAR(MAX)     CONSTRAINT DF_part_number_notes            DEFAULT '',
  user_field_1        VARCHAR(255)     CONSTRAINT DF_part_number_user_field_1     DEFAULT '',
  user_field_2        VARCHAR(255)     CONSTRAINT DF_part_number_user_field_2     DEFAULT '',
  user_field_3        VARCHAR(255)     CONSTRAINT DF_part_number_user_field_3     DEFAULT '',
  user_field_4        VARCHAR(255)     CONSTRAINT DF_part_number_user_field_4     DEFAULT '',
  user_field_5        VARCHAR(255)     CONSTRAINT DF_part_number_user_field_5     DEFAULT '',
  user_field_6        VARCHAR(255)     CONSTRAINT DF_part_number_user_field_6     DEFAULT '',
  user_field_7        VARCHAR(255)     CONSTRAINT DF_part_number_user_field_7     DEFAULT '',
  user_field_8        VARCHAR(255)     CONSTRAINT DF_part_number_user_field_8     DEFAULT '',
  user_field_9        VARCHAR(255)     CONSTRAINT DF_part_number_user_field_9     DEFAULT '',
  user_field_10       VARCHAR(255)     CONSTRAINT DF_part_number_user_field_10    DEFAULT '',
  created_date        DATE             CONSTRAINT DF_part_number_created_date     DEFAULT GETDATE(),
  last_rollup_cost    DECIMAL(16,8)    NULL,                                            -- NULL = no rollup ever run.
  last_rollup_at      DATETIME         NULL,                                            -- NULL = no rollup ever run.
  attachment_count    INT              CONSTRAINT DF_part_number_attachment_count DEFAULT 0,    -- Denormalized count of part_attachment rows for this part.
  primary_attachment_id INT            NULL,  -- part_attachment.id of the primary attachment.
  current_cost        DECIMAL(16,8)    CONSTRAINT DF_part_number_current_cost     DEFAULT 0,
  is_active           BIT              CONSTRAINT DF_part_number_is_active        DEFAULT 1,
  po_line_count       INT              CONSTRAINT DF_part_number_po_line_count    DEFAULT 0,    -- Denormalized count of po_line rows for this part.
  modified_date       DATE             CONSTRAINT DF_part_number_modified_date    DEFAULT GETDATE(),
  price_id            INT              NULL,                                             -- FK to price.id.
  default_supplier_id INT              NULL,                                             -- Preferred supplier for cost rollup (#465); FK to company.id.
  uom_id              INT              NULL,                                             -- FK to uom.uom_id. Base/inventory unit for this part (EA, mL, kg, …).
  stock_on_hand       DECIMAL(16,8)    NOT NULL CONSTRAINT DF_part_number_stock_on_hand DEFAULT 0, -- Cached inventory balance (issue #272); = SUM(inventory_transaction.qty). Maintained by the app, not a trigger. Do not edit directly.
  reorder_min         DECIMAL(16,8)    NULL,                                            -- Reorder point (issue #273): flag the part when stock_on_hand < reorder_min. NULL = no reorder point set (never flagged).
  is_lot_tracked      BIT              NOT NULL CONSTRAINT DF_part_number_is_lot_tracked DEFAULT 0, -- Lot/batch control (#676): when 1, goods receipt and build create a `lot` row for this part (see SQL/lot.sql).
  tracking_mode       VARCHAR(10)      NOT NULL CONSTRAINT DF_part_number_tracking_mode DEFAULT 'none'
                                       CONSTRAINT CK_part_number_tracking_mode CHECK (tracking_mode IN ('none', 'lot', 'serial', 'lot_serial'))
                                       -- Traceability epic (#736) slice 6 (#743): additive/inert — is_lot_tracked
                                       -- still drives reads until the slice 8 read-swap. Backfilled from
                                       -- is_lot_tracked (0->'none', 1->'lot'); 'serial'/'lot_serial' set per-part later.
);

ALTER TABLE dbo.part ADD CONSTRAINT FK_part_number_uom FOREIGN KEY (uom_id) REFERENCES dbo.uom (uom_id);
ALTER TABLE dbo.part ADD CONSTRAINT FK_part_default_supplier FOREIGN KEY (default_supplier_id) REFERENCES dbo.company (id);
ALTER TABLE dbo.part ADD CONSTRAINT FK_part_price FOREIGN KEY (price_id) REFERENCES dbo.price (id);
ALTER TABLE dbo.part ADD CONSTRAINT FK_part_primary_attachment FOREIGN KEY (primary_attachment_id) REFERENCES dbo.part_attachment (id);
