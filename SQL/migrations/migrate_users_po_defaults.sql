-- migrate_users_po_defaults.sql
-- Per-user PO defaults (issue #463). Adds two nullable columns to users so each
-- buyer can set their own default PO receiver/contact on the Profile page; when
-- NULL the app falls back to the global config/local.json PODefaults.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (guarded, safe to re-run).
-- Additive and rollback-safe: the columns are nullable and a pre-#463 binary
-- never references them.

IF COL_LENGTH('dbo.users', 'default_po_contact_id') IS NULL
    ALTER TABLE dbo.users ADD default_po_contact_id INT NULL;

IF COL_LENGTH('dbo.users', 'default_po_receiver_id') IS NULL
    ALTER TABLE dbo.users ADD default_po_receiver_id INT NULL;
