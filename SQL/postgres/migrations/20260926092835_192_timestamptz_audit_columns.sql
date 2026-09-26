-- Audit/event timestamps become UTC timestamptz assigned by the database (#192,
-- architecture review 2026-09-24 item 6). Not breaking (no schema_version bump): an
-- older binary's time.Now() params are still accepted by timestamptz.
--
-- Existing values are reinterpreted by the clock that wrote them:
--   * DB-clock columns (GETDATE()/CURRENT_TIMESTAMP): read in this session's TimeZone
--     (the server's), so run it with the server's default TimeZone.
--   * desktop-clock columns (Go time.Now() params): read as America/Los_Angeles, the
--     zone the shop desktops ran in. If they ran elsewhere, replace every
--     'America/Los_Angeles' below with that zone BEFORE running.
-- form_record.record_date is user-typed and stays a zoneless TIMESTAMP.
--
-- Pinned to ArxDev: the guard below aborts on any other database. A human edits the guard
-- to run it elsewhere (never default to ArxProd). Idempotent; runs in one transaction:
--   psql -v ON_ERROR_STOP=1 -d ArxDev -f 20260926092835_192_timestamptz_audit_columns.sql

BEGIN;

DO $$ BEGIN
  IF lower(current_database()) <> 'arxdev' THEN
    RAISE EXCEPTION 'Pinned to ArxDev (connected to %); edit this guard to run elsewhere', current_database();
  END IF;
END $$;

DO $$
DECLARE c record;
BEGIN
  FOR c IN SELECT * FROM (VALUES
    ('app_config','updated_at',NULL),('build','created_at','America/Los_Angeles'),
    ('company','date_modified','America/Los_Angeles'),('contact','updated_at','America/Los_Angeles'),
    ('form_events','event_date',NULL),('form_record','created_at',NULL),('form_record','updated_at',NULL),
    ('form_row','created_at',NULL),('form_row','updated_at',NULL),('form_row_history','changed_at',NULL),
    ('inventory_transaction','created_at','America/Los_Angeles'),
    ('lot','created_at','America/Los_Angeles'),('named_queries','created_at','America/Los_Angeles'),
    ('named_queries','updated_at','America/Los_Angeles'),('part','last_rollup_at','America/Los_Angeles'),
    ('purchase_order','date_modified','America/Los_Angeles'),
    ('purchase_order_history','changed_at','America/Los_Angeles'),('record_events','event_date',NULL),
    ('result','updated_at',NULL),('schema_migrations','tstamp',NULL),('unit','created_at',NULL),
    ('users','created_at',NULL),('users','updated_at',NULL)
  ) AS v(tbl, col, legacy_zone)
  LOOP
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = current_schema() AND table_name = c.tbl AND column_name = c.col
                 AND data_type = 'timestamp without time zone') THEN
      IF c.legacy_zone IS NULL THEN  -- DB-clock column: stored in the server's session TimeZone
        EXECUTE format('ALTER TABLE %I ALTER COLUMN %I TYPE timestamptz', c.tbl, c.col);
      ELSE                          -- desktop time.Now() wall clock
        EXECUTE format('ALTER TABLE %I ALTER COLUMN %I TYPE timestamptz USING %I AT TIME ZONE %L',
                       c.tbl, c.col, c.col, c.legacy_zone);
      END IF;
    END IF;
    IF c.tbl = 'part' THEN
      EXECUTE format('ALTER TABLE %I ALTER COLUMN %I DROP DEFAULT', c.tbl, c.col);
    ELSE
      EXECUTE format('ALTER TABLE %I ALTER COLUMN %I SET DEFAULT now()', c.tbl, c.col);
    END IF;
  END LOOP;
END $$;

INSERT INTO schema_migrations (version_id, is_applied)
SELECT 20260926092835, TRUE
WHERE NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version_id = 20260926092835);

COMMIT;
