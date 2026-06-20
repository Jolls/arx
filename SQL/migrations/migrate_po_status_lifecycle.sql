-- migrate_po_status_lifecycle.sql
-- PO Status Lifecycle (issue #271). Moves PO.status from the old value set to the
-- new lifecycle and adds the PO_status_history transition log.
--
-- Old values: pending | placed | complete | cancelled | on_hold
-- New values: draft | open | sent | partially_received | closed | cancelled
-- Mapping:    pending->draft, placed->sent, on_hold->open, complete->closed,
--             cancelled->cancelled (unchanged)
--
-- Run against BOTH the production database AND ArxDev. Additive and rollback-safe:
-- the old 0.5.x binary keeps working (its reads use explicit column lists; it never
-- references PO_status_history).

-- ============================================================
-- STEP 1: SWAP THE STATUS VALUES
-- ============================================================

ALTER TABLE dbo.PO DROP CONSTRAINT CK_PO_status;

UPDATE dbo.PO SET status = 'draft'  WHERE status = 'pending';
UPDATE dbo.PO SET status = 'sent'   WHERE status = 'placed';
UPDATE dbo.PO SET status = 'open'   WHERE status = 'on_hold';
UPDATE dbo.PO SET status = 'closed' WHERE status = 'complete';
-- 'cancelled' is unchanged.

ALTER TABLE dbo.PO ADD CONSTRAINT CK_PO_status
    CHECK (status IN ('draft','open','sent','partially_received','closed','cancelled'));

-- New default for freshly inserted rows.
ALTER TABLE dbo.PO DROP CONSTRAINT DF_PO_status;
ALTER TABLE dbo.PO ADD CONSTRAINT DF_PO_status DEFAULT 'draft' FOR status;

-- Keep is_active in sync (closed/cancelled are inactive).
UPDATE dbo.PO SET is_active =
    CASE WHEN status IN ('draft','open','sent','partially_received') THEN 1 ELSE 0 END;

-- ============================================================
-- STEP 2: CREATE THE TRANSITION LOG
-- ============================================================

IF OBJECT_ID('dbo.PO_status_history', 'U') IS NOT NULL DROP TABLE dbo.PO_status_history;

CREATE TABLE dbo.PO_status_history (
  id          INT          PRIMARY KEY IDENTITY,
  po_id       INT          NOT NULL,
  from_status VARCHAR(20),
  to_status   VARCHAR(20)  NOT NULL,
  changed_by  VARCHAR(128) NOT NULL CONSTRAINT DF_PO_status_history_by DEFAULT '',
  changed_at  DATETIME     NOT NULL CONSTRAINT DF_PO_status_history_at DEFAULT GETDATE()
);

ALTER TABLE dbo.PO_status_history ADD CONSTRAINT FK_PO_status_history_PO
    FOREIGN KEY (po_id) REFERENCES dbo.PO (id);
CREATE INDEX IX_PO_status_history_po ON dbo.PO_status_history (po_id, changed_at);

-- ============================================================
-- STEP 3 (optional): SEED A BASELINE HISTORY ROW PER EXISTING PO
-- Gives pre-existing POs one entry reflecting their current status.
-- ============================================================

INSERT INTO dbo.PO_status_history (po_id, from_status, to_status, changed_by, changed_at)
SELECT id, NULL, status, 'migration', ISNULL(date_modified, GETDATE()) FROM dbo.PO;
