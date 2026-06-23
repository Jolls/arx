-- migrate_po_rfq.sql
-- Request for Quotation (issue #270). An RFQ is modelled as a purchase_order in a
-- new 'rfq' status, grouped with its sibling quotes via rfq_group_id, with a
-- per-line quoted lead time on po_line. Awarding duplicates the winning quote into
-- a new PO and closes out the RFQ group (application logic; no schema impact here).
--
-- Additive and rollback-safe: both new columns are nullable and the old 0.5.x binary
-- never references them. Extending the status CHECK only widens the allowed set, so
-- existing rows stay valid. Run against BOTH ArxProd and ArxDev.
--
-- Idempotent: safe to re-run (each step is guarded).

-- ============================================================
-- STEP 1: ALLOW 'rfq' IN THE STATUS CHECK CONSTRAINT
-- ============================================================

IF OBJECT_ID('dbo.CK_purchase_order_status', 'C') IS NOT NULL
    ALTER TABLE dbo.purchase_order DROP CONSTRAINT CK_purchase_order_status;

ALTER TABLE dbo.purchase_order ADD CONSTRAINT CK_purchase_order_status
    CHECK (status IN ('rfq','draft','open','sent','partially_received','closed','cancelled'));

-- ============================================================
-- STEP 2: RFQ GROUPING COLUMN ON purchase_order
-- Sibling quotes (one per supplier) share rfq_group_id. The originating RFQ
-- anchors the group to its own id; clones inherit it. NULL for ordinary POs.
-- ============================================================

IF COL_LENGTH('dbo.purchase_order', 'rfq_group_id') IS NULL
    ALTER TABLE dbo.purchase_order ADD rfq_group_id INT NULL;

-- ============================================================
-- STEP 3: QUOTED LEAD TIME PER LINE ON po_line
-- Captured per supplier response on the RFQ comparison grid. NULL = not quoted.
-- ============================================================

IF COL_LENGTH('dbo.po_line', 'lead_time_days') IS NULL
    ALTER TABLE dbo.po_line ADD lead_time_days INT NULL;
