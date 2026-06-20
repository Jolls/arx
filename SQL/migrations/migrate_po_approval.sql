-- migrate_po_approval.sql
-- PO Approval Workflow (issue #267). Adds an approval gate to POs, a per-user
-- approver flag, and consolidates PO history into a single PO_history log.
--
-- Builds on migrate_po_status_lifecycle.sql (#271), which created PO_status_history.
-- That table is superseded by the unified PO_history table below and is dropped here
-- (it carries no production data worth keeping). Run against BOTH ArxProd and ArxDev.
--
-- Idempotent: safe to re-run (each step is guarded), so a partially-applied run can
-- simply be executed again. Additive and rollback-safe for the new columns: they are
-- defaulted, and a 0.5.x binary never references them. (The PO_status_history drop is
-- not reversible, but the old binary's status-history writes are not relied upon.)

-- ============================================================
-- STEP 1: PER-USER APPROVER FLAG
-- ============================================================

IF COL_LENGTH('dbo.users', 'can_approve_po') IS NULL
    ALTER TABLE dbo.users ADD can_approve_po BIT NOT NULL CONSTRAINT DF_users_can_approve_po DEFAULT 0;

-- ============================================================
-- STEP 2: APPROVAL STATUS ON PO
-- Add the column + default, backfill existing rows, then add the CHECK.
-- (Separate statements avoid the WITH VALUES grammar restriction and guarantee
-- existing POs get 'not_submitted' rather than NULL.)
-- ============================================================

IF COL_LENGTH('dbo.PO', 'approval_status') IS NULL
    ALTER TABLE dbo.PO ADD approval_status VARCHAR(20) CONSTRAINT DF_PO_approval_status DEFAULT 'not_submitted';

-- Backfill existing rows (added column is NULL until set). Step 4 below grandfathers
-- these as approved; this just ensures a valid non-NULL starting value.
UPDATE dbo.PO SET approval_status = 'not_submitted' WHERE approval_status IS NULL;

IF OBJECT_ID('dbo.CK_PO_approval_status', 'C') IS NULL
    ALTER TABLE dbo.PO ADD CONSTRAINT CK_PO_approval_status
        CHECK (approval_status IN ('not_submitted','pending','approved','rejected'));

-- ============================================================
-- STEP 3: UNIFIED PO HISTORY (replaces PO_status_history)
-- ============================================================

IF OBJECT_ID('dbo.PO_status_history', 'U') IS NOT NULL DROP TABLE dbo.PO_status_history;
IF OBJECT_ID('dbo.PO_history',        'U') IS NOT NULL DROP TABLE dbo.PO_history;

CREATE TABLE dbo.PO_history (
  id          INT          PRIMARY KEY IDENTITY,
  po_id       INT          NOT NULL,
  event_type  VARCHAR(20)  NOT NULL CONSTRAINT CK_PO_history_event CHECK (event_type IN ('status','approval')),
  from_status VARCHAR(20),   -- status events: prior status (NULL on creation)
  to_status   VARCHAR(20),   -- status events: new status
  action      VARCHAR(20),   -- approval events: submitted|approved|rejected|reset
  note        VARCHAR(MAX),  -- approval events: optional comment / rejection reason
  changed_by  VARCHAR(128) NOT NULL CONSTRAINT DF_PO_history_by DEFAULT '',
  changed_at  DATETIME     NOT NULL CONSTRAINT DF_PO_history_at DEFAULT GETDATE()
);

ALTER TABLE dbo.PO_history ADD CONSTRAINT FK_PO_history_PO FOREIGN KEY (po_id) REFERENCES dbo.PO (id);
CREATE INDEX IX_PO_history_po ON dbo.PO_history (po_id, changed_at);

-- ============================================================
-- STEP 4: GRANDFATHER EXISTING POs AS APPROVED (one-time)
-- These POs predate the approval workflow, so mark them approved and record a
-- matching history entry (timestamp + actor) instead of leaving a blank trail.
--   - Edit @actor if you would rather attribute this to a person than "migration".
--   - The timestamp uses each PO's order date (falling back to last-modified, then now).
--   - To exclude drafts/cancelled POs, add e.g. AND status IN ('sent','partially_received','closed')
--     to both the INSERT...SELECT and the UPDATE WHERE clauses.
-- Comment out this whole step if you would rather review existing POs individually.
-- Re-running is safe: only POs still 'not_submitted' are affected.
-- ============================================================

DECLARE @actor VARCHAR(128) = 'migration';

INSERT INTO dbo.PO_history (po_id, event_type, action, note, changed_by, changed_at)
SELECT id, 'approval', 'approved', 'Bulk-approved: predates approval workflow (#267)',
       @actor, COALESCE(CAST(date_ordered AS DATETIME), date_modified, GETDATE())
FROM dbo.PO
WHERE approval_status = 'not_submitted';

UPDATE dbo.PO SET approval_status = 'approved' WHERE approval_status = 'not_submitted';
