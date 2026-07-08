-- Issue #636: review-only queries to find historical po_line rows (part_id IS NULL)
-- that can likely be matched to an existing catalog part now.
-- These are SELECT-only for manual review. Run against ArxProd yourself; do not
-- generate/run any UPDATE until the matches below are eyeballed and confirmed.

-- 1. Baseline: all unlinked po_line rows with real data (from the issue).  412 results in ArxProd as of 2024-06-05.  These are the rows that need to be matched to a real part.
SELECT pol.id, po.number AS po_number, pol.line_number, pol.description,
       pol.qty, pol.unit_cost, pol.vendor_part_number
FROM dbo.po_line pol
JOIN dbo.purchase_order po ON po.ID = pol.po_id
WHERE pol.part_id IS NULL
  AND (pol.qty <> 0 OR pol.unit_cost <> 0 OR ISNULL(pol.vendor_part_number, '') <> '')
ORDER BY pol.id;

-- 2. Candidate matches by vendor_part_number == supplier_part.supplier_pn (exact, case-insensitive),
--    scoped to the SAME supplier the PO was placed with (supplier_part.supplier_id = purchase_order.supplier_id).
--    Highest-confidence group: the supplier's own PN on the PO line matches a known sourcing link
--    from that same supplier. Flags ambiguous rows (matched to more than one distinct part) for review.
SELECT pol.id, po.number AS po_number, pol.line_number, pol.description,
       pol.qty, pol.unit_cost, pol.vendor_part_number,
       sp.part_id AS candidate_part_id, p.part_number AS candidate_part_number,
       COUNT(*) OVER (PARTITION BY pol.id) AS match_count
FROM dbo.po_line pol
JOIN dbo.purchase_order po ON po.ID = pol.po_id
JOIN dbo.supplier_part sp ON sp.supplier_id = po.supplier_id
                          AND LTRIM(RTRIM(sp.supplier_pn)) = LTRIM(RTRIM(pol.vendor_part_number))
JOIN dbo.part p ON p.id = sp.part_id
WHERE pol.part_id IS NULL
  AND ISNULL(pol.vendor_part_number, '') <> ''
ORDER BY match_count DESC, pol.id;

-- 2a. Apply: the vendor-PN match, restricted to the unambiguous rows (match_count = 1 in query 2).
--     Sets ONLY part_id — part_number_snapshot/revision_snapshot/vendor_part_number are left as-is.
--     Review the SELECT below before running the UPDATE beneath it.
WITH matches AS (
  SELECT pol.id AS pol_id,
         sp.part_id AS candidate_part_id,
         COUNT(*) OVER (PARTITION BY pol.id) AS match_count
  FROM dbo.po_line pol
  JOIN dbo.purchase_order po ON po.ID = pol.po_id
  JOIN dbo.supplier_part sp ON sp.supplier_id = po.supplier_id
                            AND LTRIM(RTRIM(sp.supplier_pn)) = LTRIM(RTRIM(pol.vendor_part_number))
  JOIN dbo.part p ON p.id = sp.part_id
  WHERE pol.part_id IS NULL
    AND ISNULL(pol.vendor_part_number, '') <> ''
)
SELECT pol_id, candidate_part_id
FROM matches
WHERE match_count = 1
ORDER BY pol_id;

-- Applies the match above:
WITH matches AS (
  SELECT pol.id AS pol_id,
         sp.part_id AS candidate_part_id,
         COUNT(*) OVER (PARTITION BY pol.id) AS match_count
  FROM dbo.po_line pol
  JOIN dbo.purchase_order po ON po.ID = pol.po_id
  JOIN dbo.supplier_part sp ON sp.supplier_id = po.supplier_id
                            AND LTRIM(RTRIM(sp.supplier_pn)) = LTRIM(RTRIM(pol.vendor_part_number))
  JOIN dbo.part p ON p.id = sp.part_id
  WHERE pol.part_id IS NULL
    AND ISNULL(pol.vendor_part_number, '') <> ''
)
UPDATE pol
SET pol.part_id = m.candidate_part_id
FROM dbo.po_line pol
JOIN matches m ON m.pol_id = pol.id AND m.match_count = 1;

-- 3. Candidate matches by description == part.title or part.part_number (exact, case-insensitive).
--    Lower-confidence group: free-text description happens to equal a part's title/PN exactly.
--    Still needs eyeballing before applying — description text is not a stable key.
--    NOTE: returned 0 rows in ArxProd — part.title is Arx's internal naming, not the supplier's
--    verbatim product description, so exact matches are unlikely. See 3-alt below for a looser pass.
SELECT pol.id, po.number AS po_number, pol.line_number, pol.description,
       pol.qty, pol.unit_cost, pol.vendor_part_number,
       p.id AS candidate_part_id, p.part_number AS candidate_part_number, p.title AS candidate_title
FROM dbo.po_line pol
JOIN dbo.purchase_order po ON po.ID = pol.po_id
JOIN dbo.part p
  ON LTRIM(RTRIM(p.title)) = LTRIM(RTRIM(pol.description))
  OR LTRIM(RTRIM(p.part_number)) = LTRIM(RTRIM(pol.description))
WHERE pol.part_id IS NULL
  AND ISNULL(pol.description, '') <> ''
ORDER BY pol.id;

-- 3-alt. Looser pass: part.part_number appears literally inside the PO line description
--        (catches cases where the internal PN was written into a free-text description/comment
--        line). Still needs manual review — a short/common part_number could substring-match
--        unrelated text.
SELECT pol.id, po.number AS po_number, pol.line_number, pol.description,
       pol.qty, pol.unit_cost, pol.vendor_part_number,
       p.id AS candidate_part_id, p.part_number AS candidate_part_number, p.title AS candidate_title
FROM dbo.po_line pol
JOIN dbo.purchase_order po ON po.ID = pol.po_id
JOIN dbo.part p
  ON pol.description LIKE '%' + p.part_number + '%'
WHERE pol.part_id IS NULL
  AND ISNULL(pol.description, '') <> ''
  AND LEN(p.part_number) >= 6
ORDER BY pol.id;

-- 3-alt-a. Apply: the substring match above, restricted to unambiguous rows (one candidate per po_line).
--          Sets ONLY part_id — part_number_snapshot/revision_snapshot/description are left as-is.
--          Review the SELECT below before running the UPDATE beneath it.
WITH matches AS (
  SELECT pol.id AS pol_id,
         p.id AS candidate_part_id,
         COUNT(*) OVER (PARTITION BY pol.id) AS match_count
  FROM dbo.po_line pol
  JOIN dbo.purchase_order po ON po.ID = pol.po_id
  JOIN dbo.part p
    ON pol.description LIKE '%' + p.part_number + '%'
  WHERE pol.part_id IS NULL
    AND ISNULL(pol.description, '') <> ''
    AND LEN(p.part_number) >= 6
)
SELECT pol_id, candidate_part_id
FROM matches
WHERE match_count = 1
ORDER BY pol_id;

-- Applies the match above:
WITH matches AS (
  SELECT pol.id AS pol_id,
         p.id AS candidate_part_id,
         COUNT(*) OVER (PARTITION BY pol.id) AS match_count
  FROM dbo.po_line pol
  JOIN dbo.purchase_order po ON po.ID = pol.po_id
  JOIN dbo.part p
    ON pol.description LIKE '%' + p.part_number + '%'
  WHERE pol.part_id IS NULL
    AND ISNULL(pol.description, '') <> ''
    AND LEN(p.part_number) >= 6
)
UPDATE pol
SET pol.part_id = m.candidate_part_id
FROM dbo.po_line pol
JOIN matches m ON m.pol_id = pol.id AND m.match_count = 1;

-- 3a. Apply: the description match, restricted to unambiguous rows (one candidate per po_line).
--     Sets ONLY part_id — part_number_snapshot/revision_snapshot/description are left as-is.
--     Review the SELECT below before running the UPDATE beneath it.
WITH matches AS (
  SELECT pol.id AS pol_id,
         p.id AS candidate_part_id,
         COUNT(*) OVER (PARTITION BY pol.id) AS match_count
  FROM dbo.po_line pol
  JOIN dbo.purchase_order po ON po.ID = pol.po_id
  JOIN dbo.part p
    ON LTRIM(RTRIM(p.title)) = LTRIM(RTRIM(pol.description))
    OR LTRIM(RTRIM(p.part_number)) = LTRIM(RTRIM(pol.description))
  WHERE pol.part_id IS NULL
    AND ISNULL(pol.description, '') <> ''
)
SELECT pol_id, candidate_part_id
FROM matches
WHERE match_count = 1
ORDER BY pol_id;

-- Once the above looks right, apply it:
-- WITH matches AS (
--   SELECT pol.id AS pol_id,
--          p.id AS candidate_part_id,
--          COUNT(*) OVER (PARTITION BY pol.id) AS match_count
--   FROM dbo.po_line pol
--   JOIN dbo.purchase_order po ON po.ID = pol.po_id
--   JOIN dbo.part p
--     ON LTRIM(RTRIM(p.title)) = LTRIM(RTRIM(pol.description))
--     OR LTRIM(RTRIM(p.part_number)) = LTRIM(RTRIM(pol.description))
--   WHERE pol.part_id IS NULL
--     AND ISNULL(pol.description, '') <> ''
-- )
-- UPDATE pol
-- SET pol.part_id = m.candidate_part_id
-- FROM dbo.po_line pol
-- JOIN matches m ON m.pol_id = pol.id AND m.match_count = 1;

-- 4. Extract an Arx-format part number (###-#####-##, e.g. 655-01211-00) embedded in the
--    description text, then match it exactly against an existing part.part_number.
--    Different from 3-alt: pulls a PN-shaped token out of the free text first (via PATINDEX),
--    rather than checking every known part_number for substring containment.
SELECT pol.id, po.number AS po_number, pol.line_number, pol.description,
       pol.qty, pol.unit_cost, pol.vendor_part_number,
       extracted.candidate_pn,
       p.id AS candidate_part_id, p.part_number AS candidate_part_number
FROM dbo.po_line pol
JOIN dbo.purchase_order po ON po.ID = pol.po_id
CROSS APPLY (
  SELECT SUBSTRING(pol.description,
                    PATINDEX('%[0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9]-[0-9][0-9]%', pol.description),
                    12) AS candidate_pn
) extracted
JOIN dbo.part p ON p.part_number = extracted.candidate_pn
WHERE pol.part_id IS NULL
  AND ISNULL(pol.description, '') <> ''
  AND PATINDEX('%[0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9]-[0-9][0-9]%', pol.description) > 0
ORDER BY pol.id;
