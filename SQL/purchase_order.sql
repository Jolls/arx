-- purchase_order: Purchase Orders (renamed from PO in db-table-rename commit 4).
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

IF OBJECT_ID('dbo.purchase_order', 'U') IS NOT NULL DROP TABLE purchase_order;

CREATE TABLE purchase_order (
  -- Identity
  id                    INT            PRIMARY KEY IDENTITY,
  number                VARCHAR(32)    NOT NULL CONSTRAINT UQ_purchase_order_number UNIQUE, -- Human-readable PO number.

  -- Supplier
  supplier_id           INT            NOT NULL,        -- FK to company.id.
  supplier_name         VARCHAR(127),                   -- Denormalized supplier name at time of order.
  supplier_contact      VARCHAR(127),
  supplier_contact_id   INT,                            -- FK to contact.id. NULL for legacy POs or when no contact chosen; name snapshot above is authoritative for print (#597).
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
  receiver_contact_id   INT,                            -- FK to contact.id. NULL for legacy POs or when no contact chosen; name snapshot above is authoritative for print (#597).
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
  date_modified         DATETIME       CONSTRAINT DF_purchase_order_date_modified DEFAULT GETDATE(),

  -- Financials
  tax1                  DECIMAL(10,5),
  shipping_cost         DECIMAL(10,5),
  misc_cost             DECIMAL(10,5),
  total_cost            DECIMAL(16,8),                  -- PO total including tax, shipping, misc.

  -- Notes & status
  notes                 VARCHAR(MAX),                   -- Prints on PO document.
  internal_notes        VARCHAR(MAX)   CONSTRAINT DF_purchase_order_internal_notes DEFAULT '', -- Internal-only notes, not printed on PO.
  is_active             BIT            CONSTRAINT DF_purchase_order_is_active DEFAULT 1, -- 1 = open, 0 = closed. Derived from status — do not set directly.
  status                VARCHAR(20)    CONSTRAINT DF_purchase_order_status DEFAULT 'draft' CONSTRAINT CK_purchase_order_status CHECK (status IN ('rfq','draft','open','sent','partially_received','closed','cancelled')), -- Authoritative PO state (lifecycle #271; 'rfq' added #270). Transitions recorded in purchase_order_history.
  approval_status       VARCHAR(20)    CONSTRAINT DF_purchase_order_approval_status DEFAULT 'not_submitted' CONSTRAINT CK_purchase_order_approval_status CHECK (approval_status IN ('not_submitted','pending','approved','rejected')), -- Approval gate (issue #267). Must be 'approved' before a PO can be sent or printed. Actions recorded in purchase_order_history.
  rfq_group_id          INT                                                              -- RFQ grouping (issue #270). Sibling quotes share this; anchored to the originating RFQ's own id. NULL for ordinary POs.
);

ALTER TABLE dbo.purchase_order ADD CONSTRAINT FK_purchase_order_company  FOREIGN KEY (supplier_id) REFERENCES dbo.company (id);
ALTER TABLE dbo.purchase_order ADD CONSTRAINT FK_purchase_order_receiver FOREIGN KEY (receiver_id) REFERENCES dbo.company (id);
ALTER TABLE dbo.purchase_order ADD CONSTRAINT FK_purchase_order_supplier_contact FOREIGN KEY (supplier_contact_id) REFERENCES dbo.contact (id);
ALTER TABLE dbo.purchase_order ADD CONSTRAINT FK_purchase_order_receiver_contact FOREIGN KEY (receiver_contact_id) REFERENCES dbo.contact (id);

-- purchase_order_history: append-only activity log for a PO (issues #271 + #267).
-- (Renamed from PO_history in db-table-rename commit 4.)
-- One unified timeline covering both kinds of event:
--   event_type='status'   — a lifecycle transition; from_status -> to_status
--                           (from_status NULL for the creation row).
--   event_type='approval' — an approval action in `action`
--                           (submitted | approved | rejected | reset), with an
--                           optional `note` (e.g. a rejection reason).
-- changed_by holds the app user's username (login handle), written by the Go handler
-- — consistent with record_events.username / test_definition_history.changed_by.
IF OBJECT_ID('dbo.purchase_order_history', 'U') IS NOT NULL DROP TABLE dbo.purchase_order_history;

CREATE TABLE purchase_order_history (
  id          INT          PRIMARY KEY IDENTITY,
  po_id       INT          NOT NULL,                                          -- FK to purchase_order.id.
  event_type  VARCHAR(20)  NOT NULL CONSTRAINT CK_purchase_order_history_event CHECK (event_type IN ('status','approval')),
  from_status VARCHAR(20),                                                    -- status events: prior status (NULL on creation).
  to_status   VARCHAR(20),                                                    -- status events: new status.
  action      VARCHAR(20),                                                    -- approval events: submitted|approved|rejected|reset.
  note        VARCHAR(MAX),                                                   -- approval events: optional comment / rejection reason.
  changed_by  VARCHAR(128) NOT NULL CONSTRAINT DF_purchase_order_history_by DEFAULT '',   -- App user username (login handle).
  changed_at  DATETIME     NOT NULL CONSTRAINT DF_purchase_order_history_at DEFAULT GETDATE()
);

ALTER TABLE dbo.purchase_order_history ADD CONSTRAINT FK_purchase_order_history_po FOREIGN KEY (po_id) REFERENCES dbo.purchase_order (id);
CREATE INDEX IX_purchase_order_history_po ON dbo.purchase_order_history (po_id, changed_at);
