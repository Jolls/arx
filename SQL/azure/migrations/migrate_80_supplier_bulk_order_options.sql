-- migrate_80_supplier_bulk_order_options.sql
-- Issue #80: add company.bulk_order_delimiter and company.bulk_order_pn_source, used by the
-- PO "Copy for Ordering" clipboard feature to format part number + qty pairs for pasting into
-- a supplier's ordering system (e.g. McMaster-Carr's "part_number,qty" bulk order form).
--
-- BACKWARD-COMPATIBLE — both columns are NOT NULL with a DEFAULT, so an INSERT from an old
-- binary (which doesn't set them) still succeeds.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line — nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on its old/new state; safe to re-run). Postgres equivalent
-- follows in comments.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

IF COL_LENGTH('dbo.company', 'bulk_order_delimiter') IS NULL
    ALTER TABLE dbo.company ADD bulk_order_delimiter VARCHAR(10) NOT NULL CONSTRAINT DF_company_bulk_order_delimiter DEFAULT 'comma';
-- Postgres: ALTER TABLE company ADD COLUMN IF NOT EXISTS bulk_order_delimiter VARCHAR(10) NOT NULL DEFAULT 'comma';

IF COL_LENGTH('dbo.company', 'bulk_order_pn_source') IS NULL
    ALTER TABLE dbo.company ADD bulk_order_pn_source VARCHAR(10) NOT NULL CONSTRAINT DF_company_bulk_order_pn_source DEFAULT 'internal';
-- Postgres: ALTER TABLE company ADD COLUMN IF NOT EXISTS bulk_order_pn_source VARCHAR(10) NOT NULL DEFAULT 'internal';
