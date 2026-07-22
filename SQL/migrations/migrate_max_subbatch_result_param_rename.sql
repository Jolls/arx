-- migrate_max_subbatch_result_param_rename.sql
-- Follow-up to migrate_769_traceability_renames.sql: that migration renamed the
-- max_subbatch_result named_query's own param from @test_id to @form_row_id (its stored
-- sql/params columns), but never touched the *usage sites* -- form_row.spec_nom and
-- result.spec_nom text that still call it as query:max_subbatch_result(@test_id=...).
-- Those now fail at runtime: "Must declare the scalar variable '@form_row_id'", because
-- parseQuerySpec() builds its param map from the stored spec_nom text, not the named_queries
-- row, and the text still supplies a param named test_id.
--
-- Plain string replace, no ID/token substitution involved -- safe to run repeatedly.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line -- nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

UPDATE dbo.form_row
   SET spec_nom = REPLACE(spec_nom, 'max_subbatch_result(@test_id=', 'max_subbatch_result(@form_row_id=')
 WHERE spec_nom LIKE '%max_subbatch_result(@test_id=%';
-- Postgres: identical UPDATE (spec_nom text is dialect-agnostic row data).

UPDATE dbo.result
   SET spec_nom = REPLACE(spec_nom, 'max_subbatch_result(@test_id=', 'max_subbatch_result(@form_row_id=')
 WHERE spec_nom LIKE '%max_subbatch_result(@test_id=%';
-- Postgres: identical UPDATE.
