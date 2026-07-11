-- migrate_reorder_min.sql
-- Reorder Points (issue #273 INV-2). Adds a per-part reorder minimum; the app
-- flags a part when stock_on_hand < reorder_min.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (guarded, safe to re-run).
-- Additive and rollback-safe: one nullable column, no data change.

IF COL_LENGTH('dbo.part', 'reorder_min') IS NULL
    ALTER TABLE dbo.part ADD reorder_min DECIMAL(16,8) NULL;
