-- cleanup_supplier_93_prices.sql
-- One-off data cleanup: deactivate all ACTIVE price rows for supplier 93 ("us" — a
-- legacy internal default-pricing scheme, not a real vendor).
--
-- Pricing rows only. Nothing is deleted: rows are set is_active = 0 (reversible),
-- history is preserved, and the company row and any sourcing links are left untouched.
--
-- RUN THIS BEFORE migrate_cost_rollup_phase2.sql so the Phase 2 preferred-supplier
-- auto-pin (STEP 3) never selects supplier 93 as a part's preferred supplier:
--   - parts whose only price was supplier 93 → end up with no active price (rollup
--     falls back to current_cost) and are NOT pinned.
--   - parts with 93 + one real supplier → leave exactly one active supplier, which
--     STEP 3 then auto-pins correctly.
--
-- Run against the target DB (ArxProd and/or ArxDev). Idempotent: re-running affects
-- 0 rows once the cleanup is done. Deactivating just removes the rows from the
-- filtered UQ_price_active_combo index — no unique-constraint conflict.

DECLARE @supplier_id INT = 93;

-- ── Preview: rows / parts currently affected (run this first to eyeball it) ──
SELECT COUNT(*)                AS active_price_rows,
       COUNT(DISTINCT part_id) AS affected_parts
FROM   dbo.price
WHERE  supplier_id = @supplier_id AND is_active = 1;

-- ── Deactivate ──────────────────────────────────────────────────────────────
UPDATE dbo.price
SET    is_active = 0
WHERE  supplier_id = @supplier_id AND is_active = 1;

PRINT CONCAT('Deactivated ', @@ROWCOUNT, ' price row(s) for supplier ', @supplier_id);

-- ── Verify: should return 0 ─────────────────────────────────────────────────
SELECT COUNT(*) AS remaining_active_rows
FROM   dbo.price
WHERE  supplier_id = @supplier_id AND is_active = 1;
