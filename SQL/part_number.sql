-- PN: Part Numbers — the core parts catalog.
-- category: descriptive label for what kind of part this is (ASM, BUY, DWG, DOC, FORM, MFG, RAW, SVC, TOOL).
--   Constrained by CK_PN_category — see CHECK constraint below.
-- has_bom: 1 if this part has a Bill of Materials. Drives BOM tab visibility. Decoupled from category.
-- release_status: U = Under Review, A = Active (Released), D = Deprecated (Obsolete).
-- PNFILLinks and PNPOLinks are denormalized counts maintained by DB triggers — do not update them in code.
-- PNPOLinks: trg_POL_part_count fires on POL INSERT/UPDATE/DELETE (see SQL/triggers.sql).
-- PNFILLinks: trg_FIL_part_count fires on FIL INSERT/UPDATE/DELETE, counts is_active=1 rows only.
-- user_field_1-10 are configurable user-defined fields.
-- price_id FKs to the price table. TODO: add FK constraint.
-- NOTE: The PN table was originally named PN_Test and renamed via sp_rename. The DEFAULT and UNIQUE
--       constraint names on the live DB still carry the PN_Test prefix (e.g. DF__PN_Test__PNActiv__*).
--       Run SQL/rename_constraints.sql to normalize them.

IF OBJECT_ID('dbo.PN', 'U') IS NOT NULL DROP TABLE PN;

CREATE TABLE PN (
  PNID              INT              PRIMARY KEY IDENTITY,
  part_number       VARCHAR(255)     NOT NULL CONSTRAINT UQ_PN_part_number UNIQUE,  -- live DB is nullable (pre-existing); NOT NULL is the intent.
  category          VARCHAR(10)      CONSTRAINT DF_PN_category         DEFAULT 'BUY'
                                     CONSTRAINT CK_PN_category         CHECK (category IN ('ASM', 'BUY', 'DWG', 'DOC', 'FORM', 'MFG', 'RAW', 'SVC', 'TOOL')),
  has_bom           BIT              CONSTRAINT DF_PN_has_bom          DEFAULT 0,
  revision          VARCHAR(10)      CONSTRAINT DF_PN_revision         DEFAULT '',   -- TODO: add NOT NULL (ALTER attempted but not applied to live).
  title             VARCHAR(255)     CONSTRAINT DF_PN_title            DEFAULT '',
  detail            VARCHAR(255)     CONSTRAINT DF_PN_detail           DEFAULT '',
  release_status    VARCHAR(255)     CONSTRAINT DF_PN_release_status   DEFAULT 'U',  -- U/A/D only. TODO: reduce to VARCHAR(1) or add CHECK constraint.
  PNReqBy           VARCHAR(50)      CONSTRAINT DF_PN_PNReqBy          DEFAULT '',
  PNNotes           VARCHAR(MAX)     CONSTRAINT DF_PN_PNNotes          DEFAULT '',
  user_field_1      VARCHAR(255)     CONSTRAINT DF_PN_user_field_1     DEFAULT '',
  user_field_2      VARCHAR(255)     CONSTRAINT DF_PN_user_field_2     DEFAULT '',
  user_field_3      VARCHAR(255)     CONSTRAINT DF_PN_user_field_3     DEFAULT '',
  user_field_4      VARCHAR(255)     CONSTRAINT DF_PN_user_field_4     DEFAULT '',
  user_field_5      VARCHAR(255)     CONSTRAINT DF_PN_user_field_5     DEFAULT '',
  user_field_6      VARCHAR(255)     CONSTRAINT DF_PN_user_field_6     DEFAULT '',
  user_field_7      VARCHAR(255)     CONSTRAINT DF_PN_user_field_7     DEFAULT '',
  user_field_8      VARCHAR(255)     CONSTRAINT DF_PN_user_field_8     DEFAULT '',
  user_field_9      VARCHAR(255)     CONSTRAINT DF_PN_user_field_9     DEFAULT '',
  user_field_10     VARCHAR(255)     CONSTRAINT DF_PN_user_field_10    DEFAULT '',
  PNDate            DATE             CONSTRAINT DF_PN_PNDate           DEFAULT GETDATE(),
  PNQty             DECIMAL(16,8)    CONSTRAINT DF_PN_PNQty            DEFAULT 0,
  PNLastRollupCost  DECIMAL(16,8)    NULL,                                            -- NULL = no rollup ever run.
  PNLastRollupAt    DATETIME         NULL,                                            -- NULL = no rollup ever run.
  PNFILLinks        INT              CONSTRAINT DF_PN_PNFILLinks       DEFAULT 0,    -- Denormalized count of FIL rows for this part.
  PNFILIDPrimary    INT              CONSTRAINT DF_PN_PNFILIDPrimary   DEFAULT 0,    -- FILID of the primary attachment.
  PNCurrentCost     DECIMAL(16,8)    CONSTRAINT DF_PN_PNCurrentCost    DEFAULT 0,
  active            BIT              CONSTRAINT DF_PN_active           DEFAULT 1,
  PNPOLinks         INT              CONSTRAINT DF_PN_PNPOLinks        DEFAULT 0,    -- Denormalized count of POL rows for this part.
  PNDateModified    DATE             CONSTRAINT DF_PN_PNDateModified   DEFAULT GETDATE(),
  price_id          INT              CONSTRAINT DF_PN_price_id         DEFAULT 0     -- FK to price table. TODO: add FK constraint.
);
