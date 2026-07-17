-- migrate_drop_contact_user_account_link.sql
-- Drop contact.user_account_link (issue #534, tracked in #540). The "User Account"
-- field was removed from the contact form and detail page (#534); the app no longer
-- reads or writes the column after removing the SELECT scan in arx_go/contacts.go and
-- the Contact.UserAccountLink struct field.
--
-- NON-BACKWARDS-COMPATIBLE: a pre-#534/#540 binary still SELECTs user_account_link by
-- name in fetchContact. Only run once every client is on a build that includes this
-- change (or later) — an older binary breaks after this column is gone.
--
-- EVALUATE DATA FIRST (run this against the target DB and preserve anything you need
-- before dropping — the column is not recoverable afterward):
--     SELECT id, display_name, user_account_link
--     FROM dbo.contact
--     WHERE user_account_link IS NOT NULL AND user_account_link <> '';
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (guarded, safe to re-run).
--
-- One of four v4 migrations (#540); run migrate_schema_v4.sql last to bump schema_version.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

IF COL_LENGTH('dbo.contact', 'user_account_link') IS NOT NULL
    ALTER TABLE dbo.contact DROP COLUMN user_account_link;
