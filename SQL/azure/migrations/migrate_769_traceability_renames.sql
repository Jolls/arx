-- migrate_769_traceability_renames.sql
-- Traceability epic (#736) slice 1.1/1.2/1.3 (#769, closes #717): the pure column renames
-- the slice-1 rename wave never carved. No behavior change:
--     result.record_id                  -> form_record_id   (child FK)
--     record_events.test_record_id      -> form_record_id
--     form_record.serial_number_pn      -> subject_part_number
--     form_record.serial_number_pn_desc -> subject_pn_description
--     form_record.part_number_id        -> part_id          (assembly under test; §4.1)
-- and rewrites the one stored named_query (max_subbatch_result) whose SQL text references
-- result.record_id and the @test_id param (-> @form_row_id), then bumps schema_version 6 -> 7.
--
-- Deliberate asymmetry: form.part_number_id KEEPS its name (the form's own PN, §4.1/Q9);
-- only form_record's becomes part_id.
--
-- The result FK (FK_result_form_record) and the record_events inline FK follow the renamed
-- column/table automatically, so no constraint rename is needed.
--
-- NON-BACKWARDS-COMPATIBLE: a pre-#769 binary queries these columns by name. The Go change
-- (renamed-column literals) and this migration must ship together — an older binary breaks
-- after the rename, and this build breaks against un-renamed columns. schema_version 6 -> 7
-- gates the rollout via the mismatch banner. Run once every client is on a #769 build.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line — nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on the old column existing / new not existing; safe to
-- re-run). Postgres equivalents follow each block in comments (for the #625 migration).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. result.record_id -> form_record_id (slice 1.1).
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.result', 'record_id') IS NOT NULL AND COL_LENGTH('dbo.result', 'form_record_id') IS NULL
    EXEC sp_rename 'dbo.result.record_id', 'form_record_id', 'COLUMN';
-- Postgres: ALTER TABLE result RENAME COLUMN record_id TO form_record_id;

-- ------------------------------------------------------------------
-- 2. record_events.test_record_id -> form_record_id (slice 1.1).
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.record_events', 'test_record_id') IS NOT NULL AND COL_LENGTH('dbo.record_events', 'form_record_id') IS NULL
    EXEC sp_rename 'dbo.record_events.test_record_id', 'form_record_id', 'COLUMN';
-- Postgres: ALTER TABLE record_events RENAME COLUMN test_record_id TO form_record_id;

-- ------------------------------------------------------------------
-- 3. form_record subject-PN denormalized snapshots (slice 1.2, #717).
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.form_record', 'serial_number_pn') IS NOT NULL AND COL_LENGTH('dbo.form_record', 'subject_part_number') IS NULL
    EXEC sp_rename 'dbo.form_record.serial_number_pn', 'subject_part_number', 'COLUMN';
IF COL_LENGTH('dbo.form_record', 'serial_number_pn_desc') IS NOT NULL AND COL_LENGTH('dbo.form_record', 'subject_pn_description') IS NULL
    EXEC sp_rename 'dbo.form_record.serial_number_pn_desc', 'subject_pn_description', 'COLUMN';
-- Postgres:
--   ALTER TABLE form_record RENAME COLUMN serial_number_pn      TO subject_part_number;
--   ALTER TABLE form_record RENAME COLUMN serial_number_pn_desc TO subject_pn_description;

-- ------------------------------------------------------------------
-- 4. form_record.part_number_id -> part_id (slice 1.3). form.part_number_id is UNCHANGED.
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.form_record', 'part_number_id') IS NOT NULL AND COL_LENGTH('dbo.form_record', 'part_id') IS NULL
    EXEC sp_rename 'dbo.form_record.part_number_id', 'part_id', 'COLUMN';
-- Postgres: ALTER TABLE form_record RENAME COLUMN part_number_id TO part_id;

-- ------------------------------------------------------------------
-- 5. Rewrite the stored max_subbatch_result named_query. Its SQL text is row DATA (not
--    touched by the sp_renames above), so without this spec_nom auto-fill breaks after the
--    rename ("Invalid column name 'record_id'"). Idempotent (sets the row to its final text).
--    NOTE: any custom named_queries a site added that reference these columns must be updated
--    by hand — this covers only the one canonical seeded row.
-- ------------------------------------------------------------------
UPDATE dbo.named_queries
   SET sql = 'SELECT MAX(TRY_CAST(r.result AS INT)) FROM result r JOIN form_record tr ON r.form_record_id = tr.id WHERE r.form_row_id = @form_row_id AND tr.is_active = 1 AND CAST(tr.record_date AS DATE) <= CONVERT(DATE, @record_date, 101)',
       params = 'form_row_id, record_date'
 WHERE name = 'max_subbatch_result';
-- Postgres: identical UPDATE (named_queries is dialect-agnostic row data).

-- ------------------------------------------------------------------
-- 6. Bump schema_version 6 -> 7 (gates the rollout; guarded, only advances 6 -> 7).
-- ------------------------------------------------------------------
UPDATE dbo.app_config
   SET setting_value = '7'
 WHERE setting_key = 'schema_version'
   AND setting_value = '6';
