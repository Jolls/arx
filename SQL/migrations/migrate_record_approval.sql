-- migrate_record_approval.sql
-- Test Record approval workflow (issues #249, #250). Adds the three-state record
-- lifecycle (WIP -> Complete -> Approved) and a per-user TR reviewer flag.
--
-- WIP      = is_locked 0
-- Complete = is_locked 1, is_approved 0   (any user may set/unlock)
-- Approved = is_locked 1, is_approved 1   (only a TR reviewer may set/unlock)
--
-- Run against BOTH ArxProd and ArxDev.
--
-- Idempotent: each step is guarded, so a partially-applied run can be re-executed.
-- Additive and rollback-safe: both columns are defaulted, and a 0.5.x binary never
-- references them. Existing locked records become 'Complete' (is_approved defaults 0).

-- ============================================================
-- STEP 1: PER-USER TR REVIEWER FLAG
-- ============================================================

IF COL_LENGTH('dbo.users', 'can_approve_records') IS NULL
    ALTER TABLE dbo.users ADD can_approve_records BIT NOT NULL
        CONSTRAINT DF_users_can_approve_records DEFAULT 0;

-- ============================================================
-- STEP 2: APPROVED FLAG ON test_record
-- ============================================================

IF COL_LENGTH('dbo.test_record', 'is_approved') IS NULL
    ALTER TABLE dbo.test_record ADD is_approved BIT NOT NULL
        CONSTRAINT DF_test_record_is_approved DEFAULT 0;

-- Backward-compatible (additive, defaulted) — does NOT bump app_config.schema_version.
-- The previous binary keeps working against the migrated DB.
