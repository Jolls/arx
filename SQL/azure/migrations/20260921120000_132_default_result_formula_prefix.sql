-- 20260921120000_132_default_result_formula_prefix.sql
-- #132: a default_result is now evaluated as arithmetic only when it starts with "=".
-- Before, any default that parsed as math was evaluated, so {record.pn} baked to
-- "750-01347-01" became 750-1347-1 = -598. Literal defaults are now left as text.
--
-- This prepends "=" to existing defaults that are formulas: those containing a function call
-- "(", a cross-step token {id}, or a self token {min}/{max}/{nom}. Literal defaults (plain
-- text, numbers, {record.*}/{form.*}-only values) are left alone. Applies to the live
-- definition (form_row) and the per-record snapshot (result).
--
-- REVIEW FIRST: it runs as a preview (@apply = 0); confirm every row listed is a real formula,
-- then set @apply = 1. A formula that uses only literals and operators (e.g. "2*3") is not
-- matched — add "=" to it by hand.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line — nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (skips rows already starting with "="). Single batch, no GO.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- PREVIEW MODE (default): with @apply = 0 the script only lists the rows it would change and
-- changes nothing. Run it, review both result sets, then set @apply = 1 and run again.
DECLARE @apply BIT = 0;

-- Preview: rows that get the "=" prefix, with the value they would get.
SELECT 'form_row' AS tbl, id, form_id, parameter, default_result, '=' + default_result AS new_default_result
FROM dbo.form_row
WHERE default_result IS NOT NULL AND default_result NOT LIKE '=%'
  AND (default_result LIKE '%(%' OR default_result LIKE '%{[0-9]%'
       OR default_result LIKE '%{min}%' OR default_result LIKE '%{max}%' OR default_result LIKE '%{nom}%');

SELECT 'result' AS tbl, id, form_record_id, form_row_id, default_result, '=' + default_result AS new_default_result
FROM dbo.result
WHERE default_result IS NOT NULL AND default_result NOT LIKE '=%'
  AND (default_result LIKE '%(%' OR default_result LIKE '%{[0-9]%'
       OR default_result LIKE '%{min}%' OR default_result LIKE '%{max}%' OR default_result LIKE '%{nom}%');

IF @apply = 1
BEGIN
    UPDATE dbo.form_row
    SET default_result = '=' + default_result
    WHERE default_result IS NOT NULL AND default_result NOT LIKE '=%'
      AND (default_result LIKE '%(%' OR default_result LIKE '%{[0-9]%'
           OR default_result LIKE '%{min}%' OR default_result LIKE '%{max}%' OR default_result LIKE '%{nom}%');

    UPDATE dbo.result
    SET default_result = '=' + default_result
    WHERE default_result IS NOT NULL AND default_result NOT LIKE '=%'
      AND (default_result LIKE '%(%' OR default_result LIKE '%{[0-9]%'
           OR default_result LIKE '%{min}%' OR default_result LIKE '%{max}%' OR default_result LIKE '%{nom}%');

    IF NOT EXISTS (SELECT 1 FROM dbo.schema_migrations WHERE version_id = 20260921120000)
        INSERT INTO dbo.schema_migrations (version_id, is_applied) VALUES (20260921120000, 1);
END
