-- PO: Purchase Orders.
-- 'number' is the human-readable PO number shown on documents.
-- supplier_id and receiver_id both FK to company.id:
--   supplier_id  = who the PO is sent to.
--   receiver_id  = bill-to / ship-to address (may differ from supplier).
-- notes prints on the PO document. internal_notes is for internal use only.
-- PO numbers are auto-assigned via sequence: SELECT NEXT VALUE FOR dbo.PO_Number_Seq
-- TRIGGER: trg_PO_company_count fires after INSERT/UPDATE/DELETE and updates company.SUNumOfPOs.
--          Do not update SUNumOfPOs manually. See SQL/triggers.sql.

-- Sequence used to generate PO numbers. One-time creation — skip if already present.
IF NOT EXISTS (SELECT 1 FROM sys.sequences WHERE name = 'PO_Number_Seq' AND schema_id = SCHEMA_ID('dbo'))
    CREATE SEQUENCE dbo.PO_Number_Seq AS INT START WITH 1 INCREMENT BY 1 NO CYCLE NO CACHE;

IF OBJECT_ID('dbo.PO', 'U') IS NOT NULL DROP TABLE PO;

CREATE TABLE PO (
  -- Identity
  id                    INT            PRIMARY KEY IDENTITY,
  number                VARCHAR(32)    NOT NULL CONSTRAINT UQ_PO_number UNIQUE, -- Human-readable PO number. Note: an old auto-named duplicate unique constraint (UQ__PO__69B9A841B8557DE9) also existed and was dropped during migration.

  -- Supplier
  supplier_id           INT            NOT NULL,        -- FK to company.id.
  supplier_name         VARCHAR(127),                   -- Denormalized supplier name at time of order.
  supplier_contact      VARCHAR(127),
  supplier_address      VARCHAR(255),
  supplier_city         VARCHAR(64),
  supplier_state        VARCHAR(64),
  supplier_zipcode      VARCHAR(10),
  supplier_country      VARCHAR(127),
  supplier_phone_number VARCHAR(32),
  supplier_fax_number   VARCHAR(32),
  supplier_email        VARCHAR(127),

  -- Receiver / Ship-To
  receiver_id           INT,                            -- FK to company.id (bill & ship to). Nullable — not all POs have a separate ship-to.
  receiver_name         VARCHAR(255),
  receiver_contact      VARCHAR(127),
  receiver_address      VARCHAR(255),
  receiver_city         VARCHAR(64),
  receiver_state        VARCHAR(64),
  receiver_zipcode      VARCHAR(10),
  receiver_country      VARCHAR(127),
  receiver_phone        VARCHAR(32),
  receiver_fax          VARCHAR(32),
  receiver_email        VARCHAR(127),

  -- Order details
  orderer               VARCHAR(255),                   -- Employee who placed the order.
  account_id            VARCHAR(127),
  date_ordered          DATE,
  date_requested        DATE,
  date_closed           DATE,
  date_printed          DATE,                           -- Date the PO was printed. Editable in Go; will be set automatically when PDF print is implemented.
  date_modified         DATETIME       CONSTRAINT DF_PO_date_modified DEFAULT GETDATE(),

  -- Financials
  tax1                  DECIMAL(10,5),
  shipping_cost         DECIMAL(10,5),
  misc_cost             DECIMAL(10,5),
  total_cost            DECIMAL(16,8),                  -- PO total including tax, shipping, misc.

  -- Notes & status
  notes                 VARCHAR(MAX),                   -- Prints on PO document.
  internal_notes        VARCHAR(MAX)   CONSTRAINT DF_PO_internal_notes DEFAULT '', -- Internal-only notes, not printed on PO.
  is_active             BIT            CONSTRAINT DF_PO_is_active DEFAULT 1, -- 1 = open, 0 = closed. Derived from status — do not set directly.
  status                VARCHAR(20)    CONSTRAINT DF_PO_status DEFAULT 'draft' CONSTRAINT CK_PO_status CHECK (status IN ('draft','open','sent','partially_received','closed','cancelled')), -- Authoritative PO state (lifecycle #271). Transitions recorded in PO_status_history.
  approval_status       VARCHAR(20)    CONSTRAINT DF_PO_approval_status DEFAULT 'not_submitted' CONSTRAINT CK_PO_approval_status CHECK (approval_status IN ('not_submitted','pending','approved','rejected')) -- Approval gate (issue #267). Must be 'approved' before a PO can be sent or printed. Actions recorded in PO_approval_history.
);

ALTER TABLE dbo.PO ADD CONSTRAINT FK_PO_company  FOREIGN KEY (supplier_id) REFERENCES dbo.company (id);
ALTER TABLE dbo.PO ADD CONSTRAINT FK_PO_receiver FOREIGN KEY (receiver_id) REFERENCES dbo.company (id);

-- PO_history: append-only activity log for a PO (issues #271 + #267).
-- One unified timeline covering both kinds of event:
--   event_type='status'   — a lifecycle transition; from_status -> to_status
--                           (from_status NULL for the creation row).
--   event_type='approval' — an approval action in `action`
--                           (submitted | approved | rejected | reset), with an
--                           optional `note` (e.g. a rejection reason).
-- changed_by holds the app user's username (login handle), written by the Go handler
-- — consistent with record_events.username / test_definition_history.changed_by.
IF OBJECT_ID('dbo.PO_history', 'U') IS NOT NULL DROP TABLE dbo.PO_history;

CREATE TABLE PO_history (
  id          INT          PRIMARY KEY IDENTITY,
  po_id       INT          NOT NULL,                                          -- FK to PO.id.
  event_type  VARCHAR(20)  NOT NULL CONSTRAINT CK_PO_history_event CHECK (event_type IN ('status','approval')),
  from_status VARCHAR(20),                                                    -- status events: prior status (NULL on creation).
  to_status   VARCHAR(20),                                                    -- status events: new status.
  action      VARCHAR(20),                                                    -- approval events: submitted|approved|rejected|reset.
  note        VARCHAR(MAX),                                                   -- approval events: optional comment / rejection reason.
  changed_by  VARCHAR(128) NOT NULL CONSTRAINT DF_PO_history_by DEFAULT '',   -- App user username (login handle).
  changed_at  DATETIME     NOT NULL CONSTRAINT DF_PO_history_at DEFAULT GETDATE()
);

ALTER TABLE dbo.PO_history ADD CONSTRAINT FK_PO_history_PO FOREIGN KEY (po_id) REFERENCES dbo.PO (id);
CREATE INDEX IX_PO_history_po ON dbo.PO_history (po_id, changed_at);
