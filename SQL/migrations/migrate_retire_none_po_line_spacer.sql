-- Retire the legacy "None" sentinel part on po_line rows (#561).
--
-- Background: the old Excel workbook required every PO line to reference a part,
-- so blank spacer rows and comment lines were pointed at a sentinel part named
-- "None" (~4993 po_line rows). The Go app allows po_line.part_id to be NULL and
-- renders/saves description-only lines fine, so the sentinel is obsolete.
--
-- Goal: unhook these rows from the sentinel by setting part_id = NULL (KEEP the
-- rows — blank spacers stay in their POs; do NOT delete). Worked in passes,
-- starting with totally-blank rows, then reviewing what remains.
--
-- The trg_POL_part_count trigger recomputes part.po_line_count automatically on
-- UPDATE (unions inserted+deleted part_ids), so the "None" part's count self-
-- corrects — no manual count fixup needed.
--
-- Run against ArxDev FIRST, verify, THEN ArxProd. Idempotent: each pass filters
-- on part_id = <None id>, so re-running finds only rows not yet migrated.

SET NOCOUNT ON;

-- Resolve the sentinel part id by its part_number. If your live data names it
-- differently (title/detail rather than part_number), adjust this lookup.
DECLARE @noneId INT = (SELECT id FROM dbo.part WHERE part_number = 'None');
IF @noneId IS NULL
BEGIN
    RAISERROR('No part with part_number = ''None'' found — check the sentinel name before running.', 16, 1);
    RETURN;
END;

-- ─────────────────────────────────────────────────────────────────────────────
-- AUDIT (run this block on its own first; it only SELECTs)
-- Categorize every po_line still pointing at the sentinel so we know what each
-- pass will touch. A row is "totally blank" when it carries no description, no
-- qty/cost, no vendor PN, and no receipt activity.
-- ─────────────────────────────────────────────────────────────────────────────
SELECT
    bucket,
    COUNT(*) AS rows
FROM (
    SELECT CASE
        WHEN received_qty <> 0 OR date_received IS NOT NULL
             OR EXISTS (SELECT 1 FROM dbo.inventory_transaction it WHERE it.po_line_id = pol.id)
            THEN '4-has-receipts (REVIEW: spacer should not have receipts)'
        WHEN LTRIM(RTRIM(ISNULL(description, ''))) = ''
             AND qty = 0 AND unit_cost = 0
             AND ISNULL(vendor_part_number, '') = ''
            THEN '1-totally-blank (Pass 1 target)'
        WHEN LTRIM(RTRIM(ISNULL(description, ''))) <> ''
             AND qty = 0 AND unit_cost = 0
            THEN '2-description-only'
        ELSE '3-has-qty-or-cost'
    END AS bucket
    FROM dbo.po_line pol
    WHERE pol.part_id = @noneId
) x
GROUP BY bucket
ORDER BY bucket;

-- Eyeball the non-blank rows before deciding passes 2/3.
SELECT pol.id, po.number AS po_number, pol.line_number, pol.part_number_snapshot,
       pol.description, pol.qty, pol.unit_cost, pol.vendor_part_number,
       pol.received_qty, pol.date_received
FROM dbo.po_line pol
JOIN dbo.purchase_order po ON po.ID = pol.po_id
WHERE pol.part_id = @noneId
  AND NOT (LTRIM(RTRIM(ISNULL(pol.description,''))) = '' AND pol.qty = 0 AND pol.unit_cost = 0
           AND ISNULL(pol.vendor_part_number,'') = '')
ORDER BY pol.id;

-- ─────────────────────────────────────────────────────────────────────────────
-- PASS 1 — totally-blank rows → true empty spacers.
-- Clears the sentinel link and the 'None' snapshot so the printed row shows
-- nothing in the Part Number / Rev columns. Leaves the row (and its line_number)
-- in place. Excludes any row with receipt activity out of caution.
-- ─────────────────────────────────────────────────────────────────────────────
UPDATE dbo.po_line
SET    part_id = NULL,
       part_number_snapshot = NULL,
       revision_snapshot = NULL
WHERE  part_id = @noneId
  AND  LTRIM(RTRIM(ISNULL(description, ''))) = ''
  AND  qty = 0
  AND  unit_cost = 0
  AND  ISNULL(vendor_part_number, '') = ''
  AND  received_qty = 0
  AND  date_received IS NULL
  AND  NOT EXISTS (SELECT 1 FROM dbo.inventory_transaction it WHERE it.po_line_id = po_line.id);

PRINT CONCAT('Pass 1: nulled ', @@ROWCOUNT, ' totally-blank rows.');

-- Verify the sentinel's remaining line count (trigger-maintained):
SELECT id, part_number, po_line_count FROM dbo.part WHERE id = @noneId;

-- ─────────────────────────────────────────────────────────────────────────────
-- PASS 2 — description-only comment lines (run after reviewing the audit).
-- These carry real text; keeping the text but dropping the sentinel makes them
-- plain comment lines.
-- ─────────────────────────────────────────────────────────────────────────────
UPDATE dbo.po_line
SET    part_id = NULL, part_number_snapshot = NULL, revision_snapshot = NULL
WHERE  part_id = @noneId
  AND  LTRIM(RTRIM(ISNULL(description, ''))) <> ''
  AND  qty = 0 AND unit_cost = 0
  AND  ISNULL(vendor_part_number, '') = ''
  AND  received_qty = 0 AND date_received IS NULL;
PRINT CONCAT('Pass 2: nulled ', @@ROWCOUNT, ' description-only rows.');

-- ─────────────────────────────────────────────────────────────────────────────
-- PASS 3 — rows with qty/cost/vendor PN, no receipts. Eyeballed (#561): these are
-- real historical order lines for parts that had no catalog entry (or none yet)
-- at the time of order — not spacers. Unlink from the sentinel but KEEP qty,
-- unit_cost, vendor_part_number, and description as-is; that data is the trail
-- for a follow-up cleanup issue (matching these to real parts where possible).
-- Excludes any row with receipt activity out of caution, same as Pass 1.
-- ─────────────────────────────────────────────────────────────────────────────
UPDATE dbo.po_line
SET    part_id = NULL, part_number_snapshot = NULL, revision_snapshot = NULL
WHERE  part_id = @noneId
  AND  (qty <> 0 OR unit_cost <> 0 OR ISNULL(vendor_part_number, '') <> '')
  AND  received_qty = 0
  AND  date_received IS NULL
  AND  NOT EXISTS (SELECT 1 FROM dbo.inventory_transaction it WHERE it.po_line_id = po_line.id);
PRINT CONCAT('Pass 3: nulled ', @@ROWCOUNT, ' qty/cost/vendor-PN rows (no catalog part match).');

-- After Pass 3, these rows are easy to find again for a follow-up part-matching
-- pass (proposed as a separate GitHub issue) via:
-- SELECT pol.id, po.number AS po_number, pol.line_number, pol.description,
--        pol.qty, pol.unit_cost, pol.vendor_part_number
-- FROM dbo.po_line pol
-- JOIN dbo.purchase_order po ON po.ID = pol.po_id
-- WHERE pol.part_id IS NULL
--   AND (pol.qty <> 0 OR pol.unit_cost <> 0 OR ISNULL(pol.vendor_part_number, '') <> '')
-- ORDER BY pol.id;

-- ─────────────────────────────────────────────────────────────────────────────
-- RETIRE THE SENTINEL PART — run only after po_line_count reads 0 above and all
-- passes have been applied to both ArxDev and ArxProd. Deactivate rather than
-- delete (reversible, keeps history/audit trail intact).
-- ─────────────────────────────────────────────────────────────────────────────
-- UPDATE dbo.part SET is_active = 0 WHERE id = @noneId;

-- After all rows are migrated and po_line_count reads 0, the "None" part can be
-- deactivated (is_active = 0) or deleted in a separate step — left out here on
-- purpose so retiring the part is a deliberate, final action.
