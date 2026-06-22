-- drop_duplicate_part_attachment_fk.sql
-- Cleanup. part_attachment.part_id carries TWO foreign keys that both enforce
-- part_attachment.part_id -> part.id:
--   - FK_FIL_FILPNID_PN        (legacy, from the original FIL table)
--   - FK_part_attachment_part_id (added by the FILPNID VARCHAR->INT migration, #297)
-- The legacy one was never dropped. The DDL (SQL/FIL.sql) declares a single FK, so drop the
-- redundant legacy constraint to match. Functionally identical; removes a duplicate check.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent.

IF OBJECT_ID('FK_FIL_FILPNID_PN', 'F') IS NOT NULL
    ALTER TABLE dbo.part_attachment DROP CONSTRAINT FK_FIL_FILPNID_PN;
