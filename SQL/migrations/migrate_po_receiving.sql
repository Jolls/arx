-- migrate_po_receiving.sql
-- PO Receiving / Goods Receipt (issue #269, Inventory Stage 2). Records receipt of
-- PO line items (partial or full) against po_line, and posts each receipt into the
-- inventory_transaction ledger (txn_type='receipt') so part.stock_on_hand rises.
-- The ledger and stock_on_hand already exist from Inventory Stage 1 (#272/#274,
-- migrate_inventory_core.sql) — this migration only adds the two po_line columns.
--
-- Additive and rollback-safe: received_qty is defaulted, date_received is nullable,
-- and the old 0.5.x binary never references either. Run against BOTH ArxProd and ArxDev.
--
-- Idempotent: safe to re-run (each step is guarded).

-- ============================================================
-- STEP 1: CUMULATIVE RECEIVED QTY PER LINE
-- ============================================================

IF COL_LENGTH('dbo.po_line', 'received_qty') IS NULL
    ALTER TABLE dbo.po_line ADD received_qty DECIMAL(11,2) NOT NULL
        CONSTRAINT DF_po_line_received_qty DEFAULT 0;

-- ============================================================
-- STEP 2: DATE OF MOST RECENT RECEIPT ON THE LINE
-- ============================================================

IF COL_LENGTH('dbo.po_line', 'date_received') IS NULL
    ALTER TABLE dbo.po_line ADD date_received DATE NULL;
