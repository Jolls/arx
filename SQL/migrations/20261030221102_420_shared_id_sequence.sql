-- One id space for lot, unit and build (#420, part of #364). Until now each table had its own
-- identity column, so lot 42, unit 42 and build 42 could all exist and a bare number (a scan, a
-- label) was ambiguous. This puts the three behind one sequence, trace_id_seq, and renumbers
-- the existing rows into it.
--
-- What it does, in one transaction:
--   1. Numbers every lot, unit and build in created_at order (ties: lot, unit, build, then old
--      id) starting just above the current highest id of the three, so a new id never equals
--      any old id and the rewrite below can't collide with itself.
--   2. Rewrites every column that holds one of those ids: unit.lot_id/build_id,
--      build.output_lot_id, inventory_transaction.lot_id/build_id, form_record.lot_id/build_id/
--      unit_id, genealogy.parent_/child_ lot/unit ids. The FKs are dropped for the rewrite and
--      re-added afterwards under their original names (which also re-validates every row).
--   3. Rewrites text that embeds an id: lot.lot_description "Build #<id>", the matching
--      inventory_transaction.note ("Build #<id>" or "Build #<id> - <note>", only on rows whose
--      build_id is that build), and an auto-generated lot.lot_number (one equal to the lot's old
--      id as text). Any other lot_number is user-entered and is left alone. Free-text notes
--      (build.note, lot.notes) are not touched.
--   4. Moves the three id columns from IDENTITY to DEFAULT nextval('trace_id_seq') and leaves
--      the sequence at the highest assigned id.
--   5. Raises if an id still appears in more than one table, or any reference is orphaned
--      (checked before the rewrite too, because form_record.lot_id/build_id carry no FK).
--
-- Breaking: bumps app_config.schema_version 12 -> 13 (an older binary mints per-table ids).
-- Needs GRANT USAGE ON SEQUENCE trace_id_seq for the app login (SCHEMA.md#database-privileges).
-- Run it when nobody is using the app: it rewrites ids across many tables, and takes an
-- ACCESS EXCLUSIVE lock on all of them until it commits.
--
-- Idempotent: if lot.id already defaults to trace_id_seq the renumber is skipped. Applied by the
-- migrate runner (arx_go/cmd/migrate, #91), one transaction per file.

-- +goose Up
-- +goose StatementBegin

CREATE SEQUENCE IF NOT EXISTS trace_id_seq AS integer START WITH 1 INCREMENT BY 1 NO CYCLE;

-- Raises, naming the offending row ids, when any lot/unit/build reference points at nothing.
CREATE FUNCTION pg_temp.trace_check_refs(phase text) RETURNS void AS $fn$
DECLARE
  r   record;
  bad text;
BEGIN
  FOR r IN SELECT * FROM (VALUES
      ('unit',                  'lot_id',         'lot'),
      ('unit',                  'build_id',       'build'),
      ('build',                 'output_lot_id',  'lot'),
      ('inventory_transaction', 'lot_id',         'lot'),
      ('inventory_transaction', 'build_id',       'build'),
      ('form_record',           'lot_id',         'lot'),
      ('form_record',           'build_id',       'build'),
      ('form_record',           'unit_id',        'unit'),
      ('genealogy',             'parent_lot_id',  'lot'),
      ('genealogy',             'parent_unit_id', 'unit'),
      ('genealogy',             'child_lot_id',   'lot'),
      ('genealogy',             'child_unit_id',  'unit')
    ) v(child, col, parent)
  LOOP
    EXECUTE format(
      'SELECT string_agg(o.id::text, '', '') FROM (SELECT c.id FROM %1$I c LEFT JOIN %3$I p ON p.id = c.%2$I WHERE c.%2$I IS NOT NULL AND p.id IS NULL ORDER BY c.id LIMIT 20) o',
      r.child, r.col, r.parent) INTO bad;
    IF bad IS NOT NULL THEN
      RAISE EXCEPTION 'orphaned %.% rows (ids: %) %; fix them before renumbering', r.child, r.col, bad, phase;
    END IF;
  END LOOP;
END
$fn$ LANGUAGE plpgsql;

DO $mig$
DECLARE
  v_base  integer;
  v_total integer;
  v_dups  text;
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_schema = current_schema() AND table_name = 'lot' AND column_name = 'id'
               AND column_default LIKE 'nextval(''trace_id_seq''%') THEN
    RAISE NOTICE 'lot/unit/build already share trace_id_seq; skipping renumber';
    RETURN;
  END IF;

  -- Block writers (and readers) until commit, so no row can be added that the id map below
  -- doesn't cover; a blocked insert then proceeds and draws from trace_id_seq.
  LOCK TABLE lot, unit, build, inventory_transaction, form_record, genealogy IN ACCESS EXCLUSIVE MODE;

  PERFORM pg_temp.trace_check_refs('before the rewrite');

  -- Number every row. Old ids are all <= v_base, new ids all > v_base.
  SELECT GREATEST(COALESCE((SELECT max(id) FROM lot), 0),
                  COALESCE((SELECT max(id) FROM unit), 0),
                  COALESCE((SELECT max(id) FROM build), 0))
    INTO v_base;

  CREATE TEMP TABLE trace_map ON COMMIT DROP AS
  SELECT kind, old_id, v_base + (row_number() OVER (ORDER BY created_at, rank, old_id))::integer AS new_id
  FROM (SELECT 'lot'::text AS kind, 1 AS rank, id AS old_id, created_at FROM lot
        UNION ALL SELECT 'unit',  2, id, created_at FROM unit
        UNION ALL SELECT 'build', 3, id, created_at FROM build) s;
  CREATE UNIQUE INDEX ON trace_map (kind, old_id);
  SELECT count(*) INTO v_total FROM trace_map;

  -- Text that embeds an id (rewritten while the old ids are still in place).
  UPDATE lot l SET lot_number = m.new_id::text
    FROM trace_map m
    WHERE m.kind = 'lot' AND m.old_id = l.id AND l.lot_number = l.id::text;
  UPDATE lot l SET lot_description = 'Build #' || m.new_id
    FROM trace_map m
    WHERE m.kind = 'build' AND l.lot_description = 'Build #' || m.old_id;
  UPDATE inventory_transaction t
    SET note = 'Build #' || m.new_id || substr(t.note, length('Build #' || m.old_id) + 1)
    FROM trace_map m
    WHERE m.kind = 'build' AND t.build_id = m.old_id
      AND (t.note = 'Build #' || m.old_id OR starts_with(t.note, 'Build #' || m.old_id || ' '));

  ALTER TABLE unit                  DROP CONSTRAINT IF EXISTS FK_unit_lot;
  ALTER TABLE unit                  DROP CONSTRAINT IF EXISTS FK_unit_build;
  ALTER TABLE build                 DROP CONSTRAINT IF EXISTS FK_build_output_lot;
  ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS FK_inv_txn_lot;
  ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS FK_inv_txn_build;
  ALTER TABLE form_record           DROP CONSTRAINT IF EXISTS FK_form_record_unit;
  ALTER TABLE genealogy             DROP CONSTRAINT IF EXISTS FK_gen_parent_lot;
  ALTER TABLE genealogy             DROP CONSTRAINT IF EXISTS FK_gen_parent_unit;
  ALTER TABLE genealogy             DROP CONSTRAINT IF EXISTS FK_gen_child_lot;
  ALTER TABLE genealogy             DROP CONSTRAINT IF EXISTS FK_gen_child_unit;

  UPDATE unit u SET lot_id = m.new_id FROM trace_map m WHERE m.kind = 'lot' AND u.lot_id = m.old_id;
  UPDATE unit u SET build_id = m.new_id FROM trace_map m WHERE m.kind = 'build' AND u.build_id = m.old_id;
  UPDATE build b SET output_lot_id = m.new_id FROM trace_map m WHERE m.kind = 'lot' AND b.output_lot_id = m.old_id;
  UPDATE inventory_transaction t SET lot_id = m.new_id FROM trace_map m WHERE m.kind = 'lot' AND t.lot_id = m.old_id;
  UPDATE inventory_transaction t SET build_id = m.new_id FROM trace_map m WHERE m.kind = 'build' AND t.build_id = m.old_id;
  UPDATE form_record f SET lot_id = m.new_id FROM trace_map m WHERE m.kind = 'lot' AND f.lot_id = m.old_id;
  UPDATE form_record f SET build_id = m.new_id FROM trace_map m WHERE m.kind = 'build' AND f.build_id = m.old_id;
  UPDATE form_record f SET unit_id = m.new_id FROM trace_map m WHERE m.kind = 'unit' AND f.unit_id = m.old_id;
  UPDATE genealogy g SET parent_lot_id = m.new_id FROM trace_map m WHERE m.kind = 'lot' AND g.parent_lot_id = m.old_id;
  UPDATE genealogy g SET child_lot_id = m.new_id FROM trace_map m WHERE m.kind = 'lot' AND g.child_lot_id = m.old_id;
  UPDATE genealogy g SET parent_unit_id = m.new_id FROM trace_map m WHERE m.kind = 'unit' AND g.parent_unit_id = m.old_id;
  UPDATE genealogy g SET child_unit_id = m.new_id FROM trace_map m WHERE m.kind = 'unit' AND g.child_unit_id = m.old_id;

  UPDATE lot   x SET id = m.new_id FROM trace_map m WHERE m.kind = 'lot'   AND x.id = m.old_id;
  UPDATE unit  x SET id = m.new_id FROM trace_map m WHERE m.kind = 'unit'  AND x.id = m.old_id;
  UPDATE build x SET id = m.new_id FROM trace_map m WHERE m.kind = 'build' AND x.id = m.old_id;

  ALTER TABLE lot   ALTER COLUMN id DROP IDENTITY IF EXISTS;
  ALTER TABLE unit  ALTER COLUMN id DROP IDENTITY IF EXISTS;
  ALTER TABLE build ALTER COLUMN id DROP IDENTITY IF EXISTS;
  ALTER TABLE lot   ALTER COLUMN id SET DEFAULT nextval('trace_id_seq');
  ALTER TABLE unit  ALTER COLUMN id SET DEFAULT nextval('trace_id_seq');
  ALTER TABLE build ALTER COLUMN id SET DEFAULT nextval('trace_id_seq');
  PERFORM setval('trace_id_seq', GREATEST(v_base + v_total, 1), v_base + v_total >= 1);

  ALTER TABLE unit                  ADD CONSTRAINT FK_unit_lot          FOREIGN KEY (lot_id)          REFERENCES lot (id);
  ALTER TABLE unit                  ADD CONSTRAINT FK_unit_build        FOREIGN KEY (build_id)        REFERENCES build (id);
  ALTER TABLE build                 ADD CONSTRAINT FK_build_output_lot  FOREIGN KEY (output_lot_id)   REFERENCES lot (id);
  ALTER TABLE inventory_transaction ADD CONSTRAINT FK_inv_txn_lot       FOREIGN KEY (lot_id)          REFERENCES lot (id);
  ALTER TABLE inventory_transaction ADD CONSTRAINT FK_inv_txn_build     FOREIGN KEY (build_id)        REFERENCES build (id);
  ALTER TABLE form_record           ADD CONSTRAINT FK_form_record_unit  FOREIGN KEY (unit_id)         REFERENCES unit (id);
  ALTER TABLE genealogy             ADD CONSTRAINT FK_gen_parent_lot    FOREIGN KEY (parent_lot_id)   REFERENCES lot (id);
  ALTER TABLE genealogy             ADD CONSTRAINT FK_gen_parent_unit   FOREIGN KEY (parent_unit_id)  REFERENCES unit (id);
  ALTER TABLE genealogy             ADD CONSTRAINT FK_gen_child_lot     FOREIGN KEY (child_lot_id)    REFERENCES lot (id);
  ALTER TABLE genealogy             ADD CONSTRAINT FK_gen_child_unit    FOREIGN KEY (child_unit_id)   REFERENCES unit (id);

  PERFORM pg_temp.trace_check_refs('after the rewrite');

  SELECT string_agg(d.id::text, ', ') INTO v_dups FROM (
    SELECT id FROM (SELECT id FROM lot UNION ALL SELECT id FROM unit UNION ALL SELECT id FROM build) a
    GROUP BY id HAVING count(*) > 1 ORDER BY id LIMIT 20) d;
  IF v_dups IS NOT NULL THEN
    RAISE EXCEPTION 'ids shared by more than one of lot/unit/build after renumber: %', v_dups;
  END IF;

  RAISE NOTICE 'renumbered % lot/unit/build rows into trace_id_seq (ids % to %)', v_total, v_base + 1, v_base + v_total;
END
$mig$;

DROP FUNCTION pg_temp.trace_check_refs(text);

UPDATE app_config SET setting_value = '13', updated_at = now() WHERE setting_key = 'schema_version' AND setting_value = '12';

-- +goose StatementEnd
