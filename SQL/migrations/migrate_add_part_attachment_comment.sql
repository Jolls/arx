-- Adds a free-text comment column to part_attachment (#585). Category stays a
-- fixed/constrained dropdown; comment is for free-text notes.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (guarded, safe to re-run).

IF NOT EXISTS (
    SELECT 1 FROM sys.columns
    WHERE object_id = OBJECT_ID('dbo.part_attachment') AND name = 'comment'
)
    ALTER TABLE dbo.part_attachment ADD comment VARCHAR(500) NULL;
