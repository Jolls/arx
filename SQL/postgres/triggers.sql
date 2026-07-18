-- Postgres port of SQL/triggers.sql (issue #625, #670).
-- Maintains the denormalized counts on company and part, plus the
-- form_row audit-history snapshot.
--   company.SUNumOfLNKs    — supplier_part rows for a supplier
--   company.SUNumOfPOs     — purchase_order rows for a supplier
--   part.attachment_count  — active (is_active) part_attachment rows for a part
--   part.po_line_count     — po_line rows for a part
--   form_row_history       — snapshot of form_row rows on UPDATE
--
-- Like the SQL Server set, the count triggers recompute a full COUNT(*) from live
-- data (not increment/decrement), so any drift is self-correcting on the next
-- write to an affected row. Those columns are display-only; they are never used
-- in WHERE clauses or business logic.
--
-- Postgres uses statement-level triggers with transition tables (REFERENCING
-- NEW TABLE / OLD TABLE) — the closest match to SQL Server's inserted/deleted
-- pseudo-tables: they fire once per statement over the whole affected set. A
-- transition table is only available for the operation that produces it (NEW
-- TABLE on INSERT/UPDATE, OLD TABLE on UPDATE/DELETE), so each count needs a
-- separate INSERT / UPDATE / DELETE trigger sharing one function; the function
-- branches on TG_OP and only touches the transition table valid for that branch.
--
-- Mixed-case count columns (SUNumOfLNKs, SUNumOfPOs) are written unquoted, so
-- Postgres folds them to lowercase — matching how the table DDL defines them.
--
-- Human-run reference DDL, like the rest of SQL/postgres. Run last, after all
-- table DDL exists. Re-runnable: CREATE OR REPLACE FUNCTION + DROP TRIGGER IF
-- EXISTS. The seed script assumes these already exist and lets them fire on its
-- INSERTs (bare table names in the Postgres ArxDev database).

-- supplier_part → company.SUNumOfLNKs
CREATE OR REPLACE FUNCTION trg_supplier_part_company_count() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE company s
        SET SUNumOfLNKs = (SELECT COUNT(*) FROM supplier_part sp WHERE sp.supplier_id = s.id)
        WHERE s.id IN (SELECT supplier_id FROM newtab WHERE supplier_id IS NOT NULL);
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE company s
        SET SUNumOfLNKs = (SELECT COUNT(*) FROM supplier_part sp WHERE sp.supplier_id = s.id)
        WHERE s.id IN (SELECT supplier_id FROM oldtab WHERE supplier_id IS NOT NULL);
    ELSE  -- UPDATE: a moved row changes the count of both the old and new supplier.
        UPDATE company s
        SET SUNumOfLNKs = (SELECT COUNT(*) FROM supplier_part sp WHERE sp.supplier_id = s.id)
        WHERE s.id IN (
            SELECT supplier_id FROM newtab WHERE supplier_id IS NOT NULL
            UNION
            SELECT supplier_id FROM oldtab WHERE supplier_id IS NOT NULL);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_supplier_part_company_count_ins ON supplier_part;
CREATE TRIGGER trg_supplier_part_company_count_ins
    AFTER INSERT ON supplier_part
    REFERENCING NEW TABLE AS newtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_supplier_part_company_count();

DROP TRIGGER IF EXISTS trg_supplier_part_company_count_upd ON supplier_part;
CREATE TRIGGER trg_supplier_part_company_count_upd
    AFTER UPDATE ON supplier_part
    REFERENCING NEW TABLE AS newtab OLD TABLE AS oldtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_supplier_part_company_count();

DROP TRIGGER IF EXISTS trg_supplier_part_company_count_del ON supplier_part;
CREATE TRIGGER trg_supplier_part_company_count_del
    AFTER DELETE ON supplier_part
    REFERENCING OLD TABLE AS oldtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_supplier_part_company_count();

-- purchase_order → company.SUNumOfPOs
CREATE OR REPLACE FUNCTION trg_PO_company_count() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE company s
        SET SUNumOfPOs = (SELECT COUNT(*) FROM purchase_order p WHERE p.supplier_id = s.id)
        WHERE s.id IN (SELECT supplier_id FROM newtab WHERE supplier_id IS NOT NULL);
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE company s
        SET SUNumOfPOs = (SELECT COUNT(*) FROM purchase_order p WHERE p.supplier_id = s.id)
        WHERE s.id IN (SELECT supplier_id FROM oldtab WHERE supplier_id IS NOT NULL);
    ELSE  -- UPDATE
        UPDATE company s
        SET SUNumOfPOs = (SELECT COUNT(*) FROM purchase_order p WHERE p.supplier_id = s.id)
        WHERE s.id IN (
            SELECT supplier_id FROM newtab WHERE supplier_id IS NOT NULL
            UNION
            SELECT supplier_id FROM oldtab WHERE supplier_id IS NOT NULL);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_PO_company_count_ins ON purchase_order;
CREATE TRIGGER trg_PO_company_count_ins
    AFTER INSERT ON purchase_order
    REFERENCING NEW TABLE AS newtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_PO_company_count();

DROP TRIGGER IF EXISTS trg_PO_company_count_upd ON purchase_order;
CREATE TRIGGER trg_PO_company_count_upd
    AFTER UPDATE ON purchase_order
    REFERENCING NEW TABLE AS newtab OLD TABLE AS oldtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_PO_company_count();

DROP TRIGGER IF EXISTS trg_PO_company_count_del ON purchase_order;
CREATE TRIGGER trg_PO_company_count_del
    AFTER DELETE ON purchase_order
    REFERENCING OLD TABLE AS oldtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_PO_company_count();

-- part_attachment → part.attachment_count (counts only active rows)
CREATE OR REPLACE FUNCTION trg_FIL_part_count() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE part p
        SET attachment_count = (SELECT COUNT(*) FROM part_attachment f WHERE f.part_id = p.id AND f.is_active)
        WHERE p.id IN (SELECT part_id FROM newtab WHERE part_id IS NOT NULL);
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE part p
        SET attachment_count = (SELECT COUNT(*) FROM part_attachment f WHERE f.part_id = p.id AND f.is_active)
        WHERE p.id IN (SELECT part_id FROM oldtab WHERE part_id IS NOT NULL);
    ELSE  -- UPDATE
        UPDATE part p
        SET attachment_count = (SELECT COUNT(*) FROM part_attachment f WHERE f.part_id = p.id AND f.is_active)
        WHERE p.id IN (
            SELECT part_id FROM newtab WHERE part_id IS NOT NULL
            UNION
            SELECT part_id FROM oldtab WHERE part_id IS NOT NULL);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_FIL_part_count_ins ON part_attachment;
CREATE TRIGGER trg_FIL_part_count_ins
    AFTER INSERT ON part_attachment
    REFERENCING NEW TABLE AS newtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_FIL_part_count();

DROP TRIGGER IF EXISTS trg_FIL_part_count_upd ON part_attachment;
CREATE TRIGGER trg_FIL_part_count_upd
    AFTER UPDATE ON part_attachment
    REFERENCING NEW TABLE AS newtab OLD TABLE AS oldtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_FIL_part_count();

DROP TRIGGER IF EXISTS trg_FIL_part_count_del ON part_attachment;
CREATE TRIGGER trg_FIL_part_count_del
    AFTER DELETE ON part_attachment
    REFERENCING OLD TABLE AS oldtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_FIL_part_count();

-- po_line → part.po_line_count
CREATE OR REPLACE FUNCTION trg_POL_part_count() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE part p
        SET po_line_count = (SELECT COUNT(*) FROM po_line pol WHERE pol.part_id = p.id)
        WHERE p.id IN (SELECT part_id FROM newtab WHERE part_id IS NOT NULL);
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE part p
        SET po_line_count = (SELECT COUNT(*) FROM po_line pol WHERE pol.part_id = p.id)
        WHERE p.id IN (SELECT part_id FROM oldtab WHERE part_id IS NOT NULL);
    ELSE  -- UPDATE
        UPDATE part p
        SET po_line_count = (SELECT COUNT(*) FROM po_line pol WHERE pol.part_id = p.id)
        WHERE p.id IN (
            SELECT part_id FROM newtab WHERE part_id IS NOT NULL
            UNION
            SELECT part_id FROM oldtab WHERE part_id IS NOT NULL);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_POL_part_count_ins ON po_line;
CREATE TRIGGER trg_POL_part_count_ins
    AFTER INSERT ON po_line
    REFERENCING NEW TABLE AS newtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_POL_part_count();

DROP TRIGGER IF EXISTS trg_POL_part_count_upd ON po_line;
CREATE TRIGGER trg_POL_part_count_upd
    AFTER UPDATE ON po_line
    REFERENCING NEW TABLE AS newtab OLD TABLE AS oldtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_POL_part_count();

DROP TRIGGER IF EXISTS trg_POL_part_count_del ON po_line;
CREATE TRIGGER trg_POL_part_count_del
    AFTER DELETE ON po_line
    REFERENCING OLD TABLE AS oldtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_POL_part_count();

-- form_row → form_row_history
-- Snapshot pre-update values into form_row_history on every UPDATE, using
-- the OLD TABLE transition table (set-based; handles bulk updates correctly).
-- changed_by comes from the session GUC arx.username set by the app (dialect
-- SetAuditUser); it is empty when unset, matching the SQL Server CONTEXT_INFO path.
CREATE OR REPLACE FUNCTION trg_form_row_history() RETURNS trigger AS $$
BEGIN
    INSERT INTO form_row_history
      (form_row_id, changed_at, changed_by,
       type, parameter, specification, spec_units,
       spec_min, spec_max, spec_nom, default_result,
       hide_formula, pf_type,
       instrument_types, format, comment, category, sheet_name)
    SELECT
      id, CURRENT_TIMESTAMP,
      COALESCE(current_setting('arx.username', true), ''),
      type, parameter, specification, spec_units,
      spec_min, spec_max, spec_nom, default_result,
      hide_formula, pf_type,
      instrument_types, format, comment, category, sheet_name
    FROM oldtab;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_form_row_history ON form_row;
CREATE TRIGGER trg_form_row_history
    AFTER UPDATE ON form_row
    REFERENCING OLD TABLE AS oldtab
    FOR EACH STATEMENT EXECUTE FUNCTION trg_form_row_history();

-- One-time recalibration: corrects any counts that drifted before triggers
-- existed (or were seeded with explicit values). Safe to re-run at any time.
UPDATE company s
SET SUNumOfLNKs = (SELECT COUNT(*) FROM supplier_part sp WHERE sp.supplier_id = s.id),
    SUNumOfPOs  = (SELECT COUNT(*) FROM purchase_order p  WHERE p.supplier_id  = s.id);

UPDATE part p
SET attachment_count = (SELECT COUNT(*) FROM part_attachment f WHERE f.part_id = p.id AND f.is_active),
    po_line_count    = (SELECT COUNT(*) FROM po_line pol WHERE pol.part_id = p.id);
