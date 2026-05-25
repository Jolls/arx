-- migrate_po_status.sql
-- Adds a status column to PO / PO_Test, backfills from is_active, and adds a
-- CHECK constraint.  Run against the PartsMaster database.
--
-- Status values: pending | placed | complete | cancelled | on_hold
-- Backfill rule: is_active=0 → 'complete', is_active=1 → 'pending'
-- After migration, status is authoritative; is_active is kept in sync by the app.

-- ============================================================
-- STEP 1: ADD COLUMN (nullable first so we can backfill)
-- ============================================================

ALTER TABLE dbo.PO      ADD status VARCHAR(20) NULL;
ALTER TABLE dbo.PO_Test ADD status VARCHAR(20) NULL;

-- ============================================================
-- STEP 2: BACKFILL
-- ============================================================

UPDATE dbo.PO      SET status = CASE WHEN is_active = 0 THEN 'complete' ELSE 'pending' END;
UPDATE dbo.PO_Test SET status = CASE WHEN is_active = 0 THEN 'complete' ELSE 'pending' END;

-- ============================================================
-- STEP 3: ADD NOT NULL + DEFAULT + CHECK CONSTRAINT
-- ============================================================

ALTER TABLE dbo.PO      ALTER COLUMN status VARCHAR(20) NOT NULL;
ALTER TABLE dbo.PO_Test ALTER COLUMN status VARCHAR(20) NOT NULL;

ALTER TABLE dbo.PO      ADD CONSTRAINT DF_PO_status      DEFAULT 'pending' FOR status;
ALTER TABLE dbo.PO_Test ADD CONSTRAINT DF_PO_Test_status DEFAULT 'pending' FOR status;

ALTER TABLE dbo.PO      ADD CONSTRAINT CK_PO_status      CHECK (status IN ('pending','placed','complete','cancelled','on_hold'));
ALTER TABLE dbo.PO_Test ADD CONSTRAINT CK_PO_Test_status CHECK (status IN ('pending','placed','complete','cancelled','on_hold'));
