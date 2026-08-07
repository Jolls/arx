-- migrate_735a_null_fk_promotion.sql
-- #213 follow-up (#735 Group A): promote two already-NULL-based logical references to real FKs:
--   contact.company_id -> company.id
--   part.default_supplier_id -> company.id
-- Both columns are already nullable INT with no sentinel value, so this is pure DDL — no data
-- conversion needed (unlike #735 Group B/C, which rework a DEFAULT 0 sentinel first).
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line — nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each ADD CONSTRAINT guarded on existence; safe to re-run). Postgres equivalents
-- follow each block in comments (for the #625 migration).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- Orphan check (run first on ArxProd; must return zero rows):
--   SELECT c.id, c.company_id FROM dbo.contact c
--   LEFT JOIN dbo.company co ON co.id = c.company_id
--   WHERE c.company_id IS NOT NULL AND co.id IS NULL;
IF OBJECT_ID('dbo.FK_contact_company', 'F') IS NULL
    ALTER TABLE dbo.contact ADD CONSTRAINT FK_contact_company FOREIGN KEY (company_id) REFERENCES dbo.company (id);
-- Postgres: ALTER TABLE contact ADD CONSTRAINT FK_contact_company FOREIGN KEY (company_id) REFERENCES company (id);

-- Orphan check (run first on ArxProd; must return zero rows):
--   SELECT p.id, p.default_supplier_id FROM dbo.part p
--   LEFT JOIN dbo.company co ON co.id = p.default_supplier_id
--   WHERE p.default_supplier_id IS NOT NULL AND co.id IS NULL;
IF OBJECT_ID('dbo.FK_part_default_supplier', 'F') IS NULL
    ALTER TABLE dbo.part ADD CONSTRAINT FK_part_default_supplier FOREIGN KEY (default_supplier_id) REFERENCES dbo.company (id);
-- Postgres: ALTER TABLE part ADD CONSTRAINT FK_part_default_supplier FOREIGN KEY (default_supplier_id) REFERENCES company (id);
