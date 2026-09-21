-- seed_test_data.sql (Postgres) — Wipe ArxDev row data and load a fixed, synthetic
-- reference dataset. Postgres port of SQL/seed_test_data.sql (#829) — same shape, same
-- pinned IDs, translated per SQL/postgres/README.md's rules. This script assumes the
-- ArxDev SCHEMA (tables/constraints/triggers/po_number_seq) already exists — created
-- once from the SQL/postgres/*.sql DDL files — and only owns row data: DELETE
-- everything, INSERT a documented set of records at pinned IDs.
--
-- Human-run, like the rest of SQL/postgres/*.sql — not auto-applied.
-- Re-run any time to reset ArxDev to a known state. Safe to run repeatedly.
--
-- ID ranges (see also SQL/schema.md "Reference test data"):
--   1001-1099  company                 6001-6099  form
--   2001-2099  contact                 6101-6199  form_row
--   3001-3099  part                    6201-6299  form_events
--   3901-3999  bom                     7001-7099  form_record
--   4001-4099  supplier_part           7101-7199  result
--   4101-4199  mfg_part                7201-7299  record_events
--   4201-4299  price                   7301-7399  record_event_results
--   5001-5099  purchase_order          8001-8099  users
--   5501-5599  po_line                 8101-8199  part_attachment
--   5801-5899  purchase_order_history  1-17       uom (natural identity)
--   5901-5999  inventory_transaction   8201-8299  build
--   8301-8399  lot                     8401-8499  genealogy
--   8501-8599  unit
--   (identity) app_config, named_queries
--
-- part_attachment (8101-8199) is seeded with URL-only attachments (no real files needed) —
-- one with a comment, one without. company_attachment is NOT seeded (would require real
-- files/URLs on companies too) and is cleared and left empty, along with logs and release_notes.
-- form_row_history gets one row written by trg_form_row_history (the seed
-- updates step 6103 after insert precisely to exercise the definition-history timeline).
--
-- Login (ArxDev only): admin/admin (PO + record approver), tester/tester (no approvals).

BEGIN;

    -- ============================================================
    -- 1. DELETE existing rows, children-first (schema/constraints/triggers stay).
    -- ============================================================
    -- company rows must go before both contact (FK_company_default_contact) and
    -- company_attachment (FK_company_primary_attachment) — company points AT both,
    -- so deleting either first fails on a re-run once those columns are set.
    -- contact.company_id -> company.id (FK_contact_company, #735) makes company <-> contact
    -- a circular reference (company.default_contact -> contact.id already existed the other
    -- way) — null out contact.company_id first to break the cycle before either table's
    -- rows are deleted.
    UPDATE contact SET company_id = NULL;
    -- part.default_supplier_id -> company.id, part.price_id -> price.id, and
    -- part.primary_attachment_id -> part_attachment.id (#735) are all real FKs now, but
    -- company/price/part_attachment are deleted well before part below — null out part's
    -- side of all three first so those earlier deletes don't fail on a dangling reference.
    UPDATE part SET default_supplier_id = NULL, price_id = NULL, primary_attachment_id = NULL;
    DELETE FROM part_attachment;
    DELETE FROM supplier_part;
    DELETE FROM mfg_part;
    DELETE FROM bom;
    DELETE FROM price;
    -- form_record.lot_id/build_id (#677) reference lot + build, so the test-record group
    -- (children first) must be cleared before build/lot below.
    DELETE FROM record_event_results;
    DELETE FROM record_events;
    DELETE FROM result;
    DELETE FROM form_record;
    DELETE FROM inventory_transaction;
    DELETE FROM genealogy;                          -- edges first (FKs to lot + unit)
    DELETE FROM unit;                               -- unit FKs part + lot + build, so before all three
    DELETE FROM build;                              -- build.output_lot_id FK to lot, so before lot
    DELETE FROM lot;                                -- lot FKs po_line + part, so before both
    DELETE FROM po_line;
    DELETE FROM purchase_order_history;
    DELETE FROM purchase_order;
    DELETE FROM company;
    DELETE FROM company_attachment;
    DELETE FROM contact;
    DELETE FROM form_events;
    DELETE FROM form_row_history;
    DELETE FROM form_row;
    DELETE FROM form;                               -- form.part_number_id FKs part, so before part
    DELETE FROM part;
    DELETE FROM named_queries;
    DELETE FROM app_config;
    DELETE FROM uom;
    DELETE FROM release_notes;
    DELETE FROM logs;
    DELETE FROM users;

    -- ============================================================
    -- 2. Reference data (no fixed-ID scheme — natural identity)
    -- ============================================================
    INSERT INTO uom (uom_id, abbreviation, display_name, unit_type) VALUES
        (1,  'EA',    'Each',        'count'),
        (2,  'PC',    'Piece',       'count'),
        (3,  'mL',    'Milliliter',  'volume'),
        (4,  'L',     'Liter',       'volume'),
        (5,  'oz',    'Fluid Ounce', 'volume'),
        (6,  'mm',    'Millimeter',  'length'),
        (7,  'm',     'Meter',       'length'),
        (8,  'IN',    'Inch',        'length'),
        (9,  'FT',    'Foot',        'length'),
        (10, 'g',     'Gram',        'mass'),
        (11, 'kg',    'Kilogram',    'mass'),
        (12, 'lb',    'Pound',       'mass'),
        (13, 'PK',    'Pack',        'package'),
        (14, 'REEL',  'Reel',        'package'),
        (15, 'BOX',   'Box',         'package'),
        (16, 'BTL',   'Bottle',      'package'),
        (17, 'SPOOL', 'Spool',       'package');

    -- updated_at is pinned to a fixed sentinel (not CURRENT_TIMESTAMP) so an integration
    -- test can assert these rows go untouched by unrelated code paths — see
    -- TestIntegration_UpdatedAtSentinel in integration_test.go.
    -- company_logo is intentionally NOT seeded here (it's a large base64 data URI that would
    -- swamp this file's diff) — run SQL/postgres/seed_company_logo.sql separately, after this script.
    INSERT INTO app_config (setting_key, setting_value, updated_at) VALUES
        ('schema_version', '10', '2020-01-01T00:00:00'),
        ('attachment_categories', 'Vendor Link,Drawing,CAD,Datasheet,Vendor Document,Fabrication,Schematic,Quote,BOM,SOP,Certificate,Photo,PDF Preview,Thumbnail', '2020-01-01T00:00:00');
    -- named_queries drive spec_nom auto-fill (query:name(@param=…) tokens). This is app
    -- config, not throwaway test data — the canonical set lives in SQL/named_queries.sql;
    -- keep the two in sync. Postgres translation (#830) of the T-SQL query text: TRY_CAST →
    -- the regex-guard CAST pattern, `+` string concat → `||`, `TOP N` → trailing `LIMIT N`,
    -- `is_active = 1` → `is_active = TRUE`. Identity-assigned (looked up by unique `name`).
    INSERT INTO named_queries (name, description, sql, params, result_type, created_at, updated_at) VALUES
        ('fil_category_for_pn', 'Comments of active Attachments for a given part number',
         'SELECT comment FROM part_attachment WHERE part_id = (SELECT id FROM part WHERE part_number = @pn) AND is_active = TRUE',
         'pn', 'list', CURRENT_TIMESTAMP, '2020-01-01T00:00:00'),
        ('parts_matching', 'Part numbers matching a LIKE pattern (caller supplies wildcards)',
         'SELECT part_number FROM part WHERE part_number LIKE @pattern AND is_active = TRUE ORDER BY part_number DESC',
         'pattern', 'list', CURRENT_TIMESTAMP, '2020-01-01T00:00:00'),
        ('pos_for_pn', 'PO numbers where a line item part number prefix matches (active = not soft-deleted)',
         'SELECT purchase_order.number FROM po_line LEFT JOIN purchase_order ON po_line.po_id = purchase_order.id WHERE po_line.part_number_snapshot LIKE @pn || ''%'' ORDER BY po_line.po_id DESC',
         'pn', 'list', CURRENT_TIMESTAMP, '2020-01-01T00:00:00'),
        ('bom_pn_by_item', 'Part number at a specific BOM item position for a given parent assembly PN',
         'SELECT part_number, description FROM bom JOIN part ON bom.component_part_id = part.id WHERE bom.parent_part_id = (SELECT id FROM part WHERE part_number = @pn) AND bom.line_number = @item',
         'pn, item', 'list', CURRENT_TIMESTAMP, '2020-01-01T00:00:00'),
        ('pn_primary_attachment', 'Primary attachment for any part number via part.primary_attachment_id; falls back to lowest sort_order if no primary set.',
         'SELECT part_attachment.file_name, COALESCE(part_attachment.category, part_attachment.file_name) FROM part_attachment JOIN part ON part_attachment.part_id = part.id WHERE part.part_number = @pn AND part_attachment.is_active = TRUE ORDER BY CASE WHEN part.primary_attachment_id IS NOT NULL AND part_attachment.id = part.primary_attachment_id THEN 0 ELSE 1 END, part_attachment.sort_order ASC LIMIT 1',
         'pn', 'single', CURRENT_TIMESTAMP, '2020-01-01T00:00:00'),
        ('form_primary_attachment', 'Primary attachment for the form''s own part number via part.primary_attachment_id; falls back to lowest sort_order if no primary set.',
         'SELECT part_attachment.file_name, COALESCE(part_attachment.category, part_attachment.file_name) FROM part_attachment JOIN part ON part_attachment.part_id = part.id WHERE part.id = @pnid AND part_attachment.is_active = TRUE ORDER BY CASE WHEN part.primary_attachment_id IS NOT NULL AND part_attachment.id = part.primary_attachment_id THEN 0 ELSE 1 END, part_attachment.sort_order ASC LIMIT 1',
         'pnid', 'single', CURRENT_TIMESTAMP, '2020-01-01T00:00:00'),
        ('recent_serial_numbers_for_form', 'Most recent 20 serial numbers tested against a given form (active records only, newest first). Use a literal form_id to reference a different form than the current one.',
         'SELECT serial_number FROM form_record WHERE form_id = @form_id AND is_active = TRUE ORDER BY CASE WHEN serial_number ~ ''^[0-9]+$'' THEN CAST(serial_number AS INTEGER) END DESC NULLS LAST, record_date DESC LIMIT 20',
         'form_id', 'multi', CURRENT_TIMESTAMP, '2020-01-01T00:00:00'),
        ('max_subbatch_result', 'Highest integer result for a test step among active records on or before the given date. Prevents later batches from inflating the max when editing historical records.',
         'SELECT MAX(CASE WHEN r.result ~ ''^[0-9]+$'' THEN CAST(r.result AS INTEGER) END) FROM result r JOIN form_record tr ON r.form_record_id = tr.id WHERE r.form_row_id = @form_row_id AND tr.is_active = TRUE AND CAST(tr.record_date AS DATE) <= CAST(@record_date AS DATE)',
         'form_row_id, record_date', 'single', CURRENT_TIMESTAMP, '2020-01-01T00:00:00'),
        ('vendor_pns_for_pn', 'Vendor part numbers and line item description from PO lines whose part number contains the search term (wildcard both sides).',
         'SELECT vendor_part_number, description FROM po_line WHERE part_number_snapshot LIKE ''%'' || @pn || ''%'' ORDER BY po_id DESC',
         'pn', 'list', CURRENT_TIMESTAMP, '2020-01-01T00:00:00');

    -- ============================================================
    -- 3. Users
    -- ============================================================
    -- Real bcrypt hashes so login works in test mode (ArxDev only — never reuse in prod):
    --   admin / admin   (can approve POs and test records; is_admin — may manage users, #750)
    --   tester / tester (no approval rights, not an admin)
    -- admin carries per-user PO defaults (receiver 1003 Global Distribution + its
    -- contact 2005 Pat Dock); tester has none (NULL = new POs start blank) — issue #463.
    -- admin also carries a per-user accent theme ('teal'); tester has none (NULL = default "blue") — issue #537.
    -- Both users are seeded in 'America/Los_Angeles' — the migration's backfill value (#847).
    INSERT INTO users (id, username, display_name, password_hash, is_active, can_approve_po, can_approve_records, is_admin, default_po_contact_id, default_po_receiver_id, accent_color, timezone, updated_at) VALUES
        (8001, 'admin',  'Admin User',  '$2a$10$0mUf8G9KxL6jGVWpUJZnaO40arOpPIqW09xqPyiyqnrBCR/VxffQe', TRUE, TRUE, TRUE, TRUE, 2005, 1003, 'teal', 'America/Los_Angeles', '2020-01-01T00:00:00'),
        (8002, 'tester', 'Test User',   '$2a$10$B29vUdQg85rwb53HIcltUuhdIb17PrSSVF32tJNJ/TQ1JyHvVYer2', TRUE, FALSE, FALSE, FALSE, NULL, NULL, NULL, 'America/Los_Angeles', '2020-01-01T00:00:00');

    -- ============================================================
    -- 4. Companies (suppliers / manufacturers)
    -- ============================================================
    INSERT INTO company (id, name, SUNotes, is_active, is_supplier, is_manufacturer, SUSupplierCode) VALUES
        (1001, 'Acme Fasteners',         'Reference test supplier - screws, fasteners.', TRUE, TRUE, FALSE, 'ACME'),
        (1002, 'Precision Machining Co', 'Reference test supplier + manufacturer.',       TRUE, TRUE, TRUE, 'PMC'),
        (1003, 'Global Distribution',    'Reference test receiver / ship-to.',            TRUE, TRUE, FALSE, 'GDIST'),
        (1004, 'Contoso Manufacturing',  'Reference test manufacturer-only company.',     TRUE, FALSE, TRUE, 'CONTOSO');

    -- ============================================================
    -- 5. Contacts
    -- ============================================================
    INSERT INTO contact (id, display_name, email, phone_1, is_active, company_id, updated_at) VALUES
        (2001, 'John Doe',    'john.doe@acmefasteners.test',       '555-0101', TRUE, 1001, '2020-01-01T00:00:00'),
        (2002, 'Jane Smith',  'jane.smith@acmefasteners.test',     '555-0102', TRUE, 1001, '2020-01-01T00:00:00'),
        (2003, 'Bob Lee',     'bob.lee@precisionmachining.test',   '555-0201', TRUE, 1002, '2020-01-01T00:00:00'),
        (2004, 'Sam Retired', 'sam.retired@acmefasteners.test',    '555-0103', FALSE, 1001, '2020-01-01T00:00:00'), -- soft-deleted contact
        (2005, 'Pat Dock',    'receiving@globaldistribution.test', '555-0301', TRUE, 1003, '2020-01-01T00:00:00'); -- receiver company contact

    -- Fully populated contact so the detail card renders every field (address, website, notes, ...).
    UPDATE contact SET
        address = '100 Fastener Blvd', city = 'Dayton', state = 'OH', zipcode = '45400', country = 'USA',
        phone_2 = '555-0104', fax = '555-0105', website = 'https://acmefasteners.test',
        notes   = 'Primary purchasing contact at Acme.'
    WHERE id = 2001;

    UPDATE company SET default_contact = 2001 WHERE id = 1001;
    UPDATE company SET default_contact = 2003 WHERE id = 1002;

    -- Bulk-order copy-to-clipboard settings (#80): exercise both delimiter/PN-source paths.
    UPDATE company SET bulk_order_delimiter = 'comma',   bulk_order_pn_source = 'internal' WHERE id = 1001;
    UPDATE company SET bulk_order_delimiter = 'newline', bulk_order_pn_source = 'vendor'   WHERE id = 1002;
    UPDATE company SET default_contact = 2005 WHERE id = 1003;

    -- ============================================================
    -- 6. Parts
    -- ============================================================
    INSERT INTO part (id, part_number, category, revision, description, release_status, is_active, uom_id, current_cost, default_supplier_id, last_rollup_cost, last_rollup_at) VALUES
        (3001, 'RAW-1001', 'RAW', 'A', 'Aluminum Stock 6061',        'A', TRUE, 6,  2.50,   1001, NULL, NULL),
        (3002, 'BUY-1001', 'BUY', 'A', 'M3x8 SHCS',                  'A', TRUE, 1,  0.05,   1002, NULL, NULL),
        (3003, 'BUY-1002', 'BUY', 'A', 'O-Ring 2-014',               'A', TRUE, 1,  0.12,   1001, NULL, NULL),
        (3004, 'MFG-1001', 'MFG', 'B', 'Drone Frame Housing',        'A', TRUE, 1,  0,      NULL, NULL, NULL),
        (3005, 'ASM-1001', 'ASM', 'A', 'Skyrunner Standard Drone',   'A', TRUE, 1,  0,      1002, 4.30, CURRENT_TIMESTAMP),
        (3006, 'OPS-1001', 'OPS', '',  'Assembler Labor',            'A', TRUE, 1,  35.00,  NULL, NULL, NULL),
        (3007, 'RAW-1002', 'RAW', 'A', 'Stainless Steel Bar Stock',  'A', TRUE, 6,  4.10,   1001, NULL, NULL),
        (3008, 'BUY-1003', 'BUY', '-', 'Prototype Bracket',          'U', TRUE, 1,  0,      NULL, NULL, NULL), -- Under Review
        (3009, 'BUY-1004', 'BUY', 'A', 'Obsolete Retaining Clip',    'D', FALSE, 1,  0.08,   NULL, NULL, NULL), -- Deprecated + inactive
        -- FORM-category parts: FormsList and the new-form part picker filter on category='FORM',
        -- so test form 6001 must hang off one. 3011 is a spare with no form attached (new-form target).
        (3010, 'FORM-1001','FORM', 'A', 'Drone Frame Housing Test Form','A', TRUE, 1,  0,      NULL, NULL, NULL),
        (3011, 'FORM-1002','FORM', 'A', 'Spare Test Form Part',       'A', TRUE, 1,  0,      NULL, NULL, NULL),
        -- Sub-assembly nested inside 3005's BOM (#579): exercises the BOM expand/collapse
        -- toggle and the "rollup" cost-source badge, neither of which any other seeded
        -- assembly-of-assemblies line reaches. last_rollup_cost matches the sum of its own
        -- BOM lines below (2*2.50 + 1*4.10 = 9.10) as if the rollup engine had just run.
        (3012, 'ASM-1002', 'ASM', 'A', 'Flight Controller Sub-Assembly', 'A', TRUE, 1,  0,      NULL, 9.10,  CURRENT_TIMESTAMP),
        -- Top-level lot-tracked assembly (#737): consumes sub-assembly 3012 AND raw 3007
        -- directly, so its build writes a two-parent genealogy edge into a two-level-deep
        -- chain (8301→8302→8306, plus 8303→8306) — a PRE-state tree for the future
        -- traceability view (epic #736 slice 9) and completes the
        -- receipt→incoming-inspection→build→build→final-test flow (§0 of the plan).
        (3013, 'ASM-1003', 'ASM', 'A', 'Skyrunner Deluxe Drone',     'A', TRUE, 1,  0,      NULL, NULL, NULL);

    -- Fully populated part so the detail card and edit round-trip show detail/notes/user fields.
    UPDATE part SET
        detail = '18-8 stainless, black oxide', notes = 'Reference part with all detail fields set.',
        requested_by = 'Admin User', user_field_1 = 'RoHS', user_field_2 = 'Bin A-12'
    WHERE id = 3002;

    -- ============================================================
    -- 6b. Part attachments — URL attachments only (no real files needed).
    -- 8101 carries a comment (#585); 8102 has none, to exercise both list states.
    -- ============================================================
    -- hash (#71) = SHA-256 of the URL string, matching computeAttachmentHash's
    -- rule for http(s) links; keeps the seed pre-backfilled rather than
    -- exercising the NULL/not-yet-backfilled path.
    INSERT INTO part_attachment (id, part_id, file_name, category, part_revision, sort_order, comment, hash) VALUES
        (8101, 3002, 'https://example.com/datasheets/m3x8-shcs.pdf', 'Datasheet', 'A', 1, 'Confirmed torque spec with vendor 2026-06-01', 'f5a466c6da42bbf7a1414f0bf831c7d749ea3870c98e54fba4272ef325c42214'),
        (8102, 3004, 'https://example.com/drawings/widget-housing.pdf', 'Drawing', 'B', 1, NULL, 'f1dbb7a5fe4577841cabc0ec7a3a995a10b73033ef9bffb422d3888f828322a7');

    -- ============================================================
    -- 7. BOM (3005 Skyrunner Standard Drone = 3002 + 3003 + OPS labor 3006 + sub-assembly 3012)
    -- ============================================================
    -- Line 3 is an OPS labor line (#465): component 3006's current_cost is an hourly rate
    -- and qty is hours, so the cost rollup includes value-add, not just material.
    -- Line 4 nests sub-assembly 3012 (#579): its own BOM (3001 + 3007) makes 3005's item 4
    -- expandable, and its cost source badges as "Rollup" using 3012.last_rollup_cost.
    -- Line 3908 (#466): 3012 also directly consumes 3002 (the same screw 3005 uses at line
    -- 3901), so the qty-break build-cost calculation must consolidate the screw's demand
    -- across both occurrences before picking a price tier — see price rows 4204/4205 below.
    INSERT INTO bom (id, parent_part_id, component_part_id, line_number, qty) VALUES
        (3901, 3005, 3002, 1, 2),
        (3902, 3005, 3003, 2, 4),
        (3903, 3005, 3006, 3, 0.5), -- 0.5 hr assembler labor
        (3904, 3010, 3004, 1, 1),   -- FORM part's BOM lists the testable unit → NewRecord PN picker
        (3905, 3012, 3001, 1, 2),   -- sub-assembly 3012's own BOM: 2x Aluminum Stock
        (3906, 3012, 3007, 2, 1),   -- + 1x Stainless Steel Bar Stock
        (3907, 3005, 3012, 4, 1),   -- 3005 nests 3012 as a sub-assembly component
        (3908, 3012, 3002, 3, 3),   -- 3012 also directly uses 3x screw 3002 (shared leaf, #466)
        (3909, 3010, 3007, 2, 1),   -- FORM part's BOM also covers 3007 (#737), so it can carry an
                                    -- Incoming Inspection record — see form_record 7012 below
        (3910, 3013, 3012, 1, 1),   -- 3013's own BOM: 1x sub-assembly 3012 (#737)
        (3911, 3013, 3007, 2, 1),   -- + 1x raw stainless bar stock, direct (not via 3012)
        (3912, 3010, 3013, 3, 1),   -- FORM part's BOM also covers 3013, for its Final Test record
        (3913, 3010, 3012, 4, 1);   -- FORM part's BOM also covers 3012 (#867/#868) — its own BOM (3905/3906)
                                    -- + tracking_mode='lot' (not lot_serial) makes it a NewRecord-selectable,
                                    -- buildable whole-lot/batch part for testing the qty-at-test-time field

    -- ============================================================
    -- 8. supplier_part / mfg_part / price
    -- ============================================================
    INSERT INTO mfg_part (id, part_id, mfg_id, mfg_part_number, description, is_active) VALUES
        (4101, 3002, 1004, 'CX-4471',          'M3x8 socket head cap screw', TRUE),
        (4102, 3002, 1004, 'CX-9999-OBSOLETE', 'Superseded MPN (soft-deleted)', FALSE); -- inactive; filtered unique index allows re-add

    -- supplier_part 4002 buys part 3002 (base unit EA) by the REEL (uom_id 14), exercising
    -- purchase-unit ≠ base-unit conversion plus the min_increment / lead_time fields.
    INSERT INTO supplier_part (id, supplier_id, part_id, mfg_part_id, uom_id, supplier_pn, supplier_desc, min_increment, lead_time, preference) VALUES
        (4001, 1001, 3001, NULL, NULL, 'ACME-AL6061',  'Aluminum 6061 bar stock', NULL,  NULL,        1),
        (4002, 1002, 3002, 4101, 14,   'PMC-M3X8',     'M3x8 SHCS (reel of 1000)', 1000, '2-3 weeks', 1),
        (4003, 1001, 3003, NULL, NULL, 'ACME-OR2014',  'O-Ring 2-014',            NULL,  NULL,        1);

    -- Vendor-scoped attachments (#56) — seeded here, after supplier_part/mfg_part, because
    -- they FK to those rows. Part 3002 ends up with all three scopes: 8101 part-level
    -- (section 6b), 8103 scoped to supplier link 4002, 8104 scoped to MPN 4101.
    -- hash (#71) = SHA-256 of the URL string, same rule as the 8101/8102 rows above.
    INSERT INTO part_attachment (id, part_id, file_name, category, part_revision, sort_order, comment, supplier_part_id, mfg_part_id, hash) VALUES
        (8103, 3002, 'https://example.com/coa/pmc-m3x8-lot7.pdf',  'COA',       'A', 2, 'Supplier-specific CoA', 4002, NULL, 'c275cf5daeb916b19bf60be1d1a5073a2aac5a90c45b0cfc0404c3eeb9e15d19'),
        (8104, 3002, 'https://example.com/datasheets/cx-4471.pdf', 'Datasheet', 'A', 3, NULL,                    NULL, 4101, '11c78752e097923a15efc83fe1961b581bbd83471ad7aafb0c21a1f2eda03572');

    -- 4204/4205 (#466): additional active qty-break tiers on 3002, alongside 4203, so the
    -- build-cost calculation has 3 tiers (1 / 100 / 1000) to select between. See bom line
    -- 3908 above for the shared-leaf scenario these tiers are meant to exercise.
    INSERT INTO price (id, part_id, supplier_id, price_ea, price_pack, pack_size, is_active, effective_date) VALUES
        (4201, 3001, 1001, 2.50, 25.00, 10,   TRUE, '2026-01-15'),
        (4202, 3002, 1002, 0.06, 6.00,  100,  FALSE, '2025-06-01'), -- superseded price (history)
        (4203, 3002, 1002, 0.05, 5.00,  100,  TRUE, '2026-02-01'), -- active price
        (4204, 3002, 1002, 0.10, 0.10,  1,    TRUE, '2026-02-01'), -- qty-break tier: singles
        (4205, 3002, 1002, 0.03, 30.00, 1000, TRUE, '2026-02-01'); -- qty-break tier: 1000-pack

    -- ============================================================
    -- 9. Purchase orders — one per lifecycle state, plus a resolved and an in-flight RFQ group
    -- ============================================================
    -- Existing RFQ group (base 5006) is seeded already-resolved: two quotes (5006 declined,
    -- 5007 awarded) plus the awarded PO 5008. The base-5010 group below is seeded in-flight
    -- (both quotes still status='rfq') to exercise the "Show RFQs" toggle and comparison grid.
    INSERT INTO purchase_order (id, number, supplier_id, supplier_name, receiver_id, receiver_name, date_ordered, date_closed, tax1, shipping_cost, total_cost, status, approval_status, is_active, rfq_group_id, notes) VALUES
        (5001, '5001',  1001, 'Acme Fasteners',         1003, 'Global Distribution', NULL,         NULL,         NULL, NULL, 25.00,  'draft',              'not_submitted', TRUE, NULL, 'Reference draft PO'),
        (5002, '5002',  1002, 'Precision Machining Co', 1003, 'Global Distribution', '2026-05-01', NULL,         4.80, 8.00, 72.80,  'open',               'approved',      TRUE, NULL, 'Reference approved/open PO'),
        (5003, '5003',  1001, 'Acme Fasteners',         1003, 'Global Distribution', '2026-04-01', NULL,         NULL, NULL, 205.00, 'partially_received', 'approved',      TRUE, NULL, 'Reference partially-received PO'),
        (5004, '5004',  1001, 'Acme Fasteners',         1003, 'Global Distribution', '2026-01-10', '2026-01-20', NULL, NULL, 25.00,  'closed',             'approved',      FALSE, NULL, 'Reference closed PO'),
        (5005, '5005',  1002, 'Precision Machining Co', 1003, 'Global Distribution', '2026-02-01', NULL,         NULL, NULL, NULL,   'cancelled',          'not_submitted', FALSE, NULL, 'Reference cancelled PO'),
        (5006, '5006R1',1001, 'Acme Fasteners',         1003, 'Global Distribution', NULL,         NULL,         NULL, NULL, NULL,   'cancelled',          'not_submitted', FALSE, 5006, 'Reference RFQ quote - declined'),
        (5007, '5006R2',1002, 'Precision Machining Co', 1003, 'Global Distribution', NULL,         NULL,         NULL, NULL, NULL,   'closed',             'not_submitted', FALSE, 5006, 'Reference RFQ quote - awarded'),
        (5008, '5006',  1002, 'Precision Machining Co', 1003, 'Global Distribution', '2026-06-01', NULL,         NULL, NULL, 24.00,  'draft',              'not_submitted', TRUE, NULL, 'Reference PO - result of awarding RFQ group 5006'),
        (5009, '5009',  1002, 'Precision Machining Co', 1003, 'Global Distribution', NULL,         NULL,         NULL, NULL, 60.00,  'draft',              'pending',       TRUE, NULL, 'Reference PO pending approval'),
        (5010, '5010R1',1001, 'Acme Fasteners',         1003, 'Global Distribution', NULL,         NULL,         NULL, NULL, 27.50,  'rfq',                'not_submitted', TRUE, 5010, 'Reference in-flight RFQ quote - Acme'),
        (5011, '5010R2',1002, 'Precision Machining Co', 1003, 'Global Distribution', NULL,         NULL,         NULL, NULL, 24.00,  'rfq',                'not_submitted', TRUE, 5010, 'Reference in-flight RFQ quote - Precision');

    -- Flesh out the denormalized supplier/receiver snapshot blocks on the printable POs
    -- (5002 approved/open, 5003 partially received) so PO detail/print render full
    -- vendor and ship-to address blocks instead of blanks.
    UPDATE purchase_order SET
        supplier_contact = 'Bob Lee',   supplier_contact_id = 2003, supplier_address = '200 Machining Way', supplier_city = 'Springfield',
        supplier_state   = 'OH',        supplier_zipcode = '45501',             supplier_country = 'USA',
        supplier_phone_number = '555-0201', supplier_email = 'bob.lee@precisionmachining.test',
        receiver_contact = 'Pat Dock',  receiver_contact_id = 2005, receiver_address = '9 Warehouse Rd',    receiver_city = 'Columbus',
        receiver_state   = 'OH',        receiver_zipcode = '43004',             receiver_country = 'USA',
        receiver_phone   = '555-0301',  receiver_email   = 'receiving@globaldistribution.test',
        orderer = 'Admin User', account_id = 'ACCT-100', date_requested = '2026-05-15',
        internal_notes = 'Internal-only note: rush job for line 2.', misc_cost = 5.00
    WHERE id = 5002;
    UPDATE purchase_order SET
        supplier_contact = 'John Doe',  supplier_contact_id = 2001, supplier_address = '100 Fastener Blvd', supplier_city = 'Dayton',
        supplier_state   = 'OH',        supplier_zipcode = '45400',             supplier_country = 'USA',
        supplier_phone_number = '555-0101', supplier_email = 'john.doe@acmefasteners.test',
        receiver_contact = 'Pat Dock',  receiver_contact_id = 2005, receiver_address = '9 Warehouse Rd',    receiver_city = 'Columbus',
        receiver_state   = 'OH',        receiver_zipcode = '43004',             receiver_country = 'USA',
        orderer = 'Admin User', account_id = 'ACCT-100'
    WHERE id = 5003;

    -- lead_time_days is captured per line on the RFQ comparison grid (#270); set on the
    -- in-flight quote lines so the grid renders differing price + lead time per supplier.
    INSERT INTO po_line (id, po_id, part_number_snapshot, revision_snapshot, part_id, line_number, description, qty, unit_cost, vendor_part_number, lead_time_days, received_qty, date_received) VALUES
        (5501, 5001, 'RAW-1001', 'A', 3001, 1, 'Aluminum Stock 6061',       10,  2.50,  'ACME-AL6061', NULL, 0,  NULL),
        (5502, 5002, 'RAW-1001', 'A', 3001, 1, 'Aluminum Stock 6061',       20,  2.50,  'ACME-AL6061', NULL, 0,  NULL),
        (5503, 5002, 'BUY-1001', 'A', 3002, 2, 'M3x8 SHCS',                 200, 0.05,  'PMC-M3X8',    NULL, 0,  NULL),
        (5504, 5003, 'RAW-1002', 'A', 3007, 1, 'Stainless Steel Bar Stock', 50,  4.10,  'ACME-SS304',  NULL, 20, '2026-05-15'),
        (5505, 5004, 'RAW-1001', 'A', 3001, 1, 'Aluminum Stock 6061',       10,  2.50,  'ACME-AL6061', NULL, 10, '2026-01-18'),
        (5506, 5008, 'BUY-1001', 'A', 3002, 1, 'M3x8 SHCS',                 500, 0.048, 'PMC-M3X8',    NULL, 0,  NULL),
        (5511, 5010, 'BUY-1001', 'A', 3002, 1, 'M3x8 SHCS',                 500, 0.055, 'ACME-M3X8',   14,   0,  NULL),
        (5512, 5011, 'BUY-1001', 'A', 3002, 1, 'M3x8 SHCS',                 500, 0.048, 'PMC-M3X8',    21,   0,  NULL);

    -- 5805 (PO 5009's submission) is dated well before "today" so the reference pending PO
    -- shows a non-trivial age on the Reports dashboard's POs Pending Approval card (issue
    -- #658, RPT-7).
    INSERT INTO purchase_order_history (id, po_id, event_type, from_status, to_status, action, note, changed_by, changed_at) VALUES
        (5801, 5002, 'status',   NULL,     'draft', NULL,         NULL,                     'admin', '2026-04-28'),
        (5802, 5002, 'approval', NULL,     NULL,    'submitted',  NULL,                     'tester','2026-04-29'),
        (5803, 5002, 'approval', NULL,     NULL,    'approved',   'Looks good',             'admin', '2026-04-30'),
        (5804, 5002, 'status',   'draft',  'open',  NULL,         NULL,                     'admin', '2026-05-01'),
        (5805, 5009, 'approval', NULL,     NULL,    'submitted',  NULL,                     'tester','2026-06-20');

    -- ============================================================
    -- 10. Inventory transactions (drives part.stock_on_hand for 3007)
    -- ============================================================
    INSERT INTO inventory_transaction (id, part_id, txn_type, qty, txn_date, username, reference, note, po_line_id) VALUES
        (5901, 3007, 'receipt',    20, '2026-05-15', 'admin',  '5003', 'Receipt against PO 5003',        5504),
        (5902, 3007, 'issue',      -5, '2026-05-20', 'tester', NULL,   'Pulled for build',                NULL),
        (5903, 3007, 'adjustment', -1, '2026-05-21', 'admin',  NULL,   'Cycle count correction',          NULL),
        (5904, 3007, 'count',       2, '2026-05-31', 'admin',  NULL,   'Physical count - found 2 extra',  NULL);

    UPDATE part
    SET stock_on_hand = (SELECT COALESCE(SUM(qty), 0) FROM inventory_transaction WHERE part_id = 3007)
    WHERE id = 3007;

    -- Reorder point (issue #273): 3007's on-hand is 16, so reorder_min = 25 leaves it
    -- below-min (flagged on the parts list/detail + Below Reorder Point dashboard card).
    UPDATE part SET reorder_min = 25 WHERE id = 3007;

    -- ============================================================
    -- 10b. Build history (#675): one past build of assembly 3005.
    -- ============================================================
    -- The matching component 'issue' / output 'receipt' ledger rows are intentionally
    -- NOT seeded here — the inventory seed above is calibrated so only 3007 carries a
    -- balance (drives the reorder-point fixtures), and posting build ledger rows would
    -- perturb those. This row just exercises the build table itself in test mode.
    -- 8202 is the manufactured build that produced lot-tracked sub-assembly 3012's lot
    -- 8302 (linked below in 10c, once the lot exists) — exercises form_record.build_id +
    -- lot_id together (#677). Its ledger rows are likewise not seeded, per the note above.
    -- 8203 (#737): builds top-level assembly 3013 out of sub-assembly 3012's lot 8302 and
    -- raw 3007's lot 8303, five days after 8202 — the second tier of the multi-level chain.
    INSERT INTO build (id, part_id, output_lot_id, qty, build_date, username, note) VALUES
        (8201, 3005, NULL, 1, '2026-05-25', 'tester', 'Built 1x assembly 3005 from BOM'),
        (8202, 3012, NULL, 1, '2026-05-25', 'tester', 'Built 1x sub-assembly 3012 (lot 8302)'),
        (8203, 3013, NULL, 1, '2026-05-30', 'tester', 'Built 1x deluxe assembly 3013 (lot 8306)');

    -- ============================================================
    -- 10c. Lot control (#676): lot-tracked parts + a one-level genealogy chain.
    -- ============================================================
    -- 3007 (RAW-1002) is a lot-tracked purchased raw; 3012 (ASM-1002 sub-assembly) and 3013
    -- (ASM-1003 top-level assembly, #737) are lot-tracked manufactured parts that consume
    -- 3007 (and, for 3013, 3012 too). 3005 is intentionally left un-tracked so the #675
    -- build test (which builds 3005 via the app's build-create POST) is unaffected — lot
    -- machinery only engages when the OUTPUT part is lot-tracked, and 3005's own BOM
    -- components (3002/3003) are deliberately left un-tracked as well so that existing
    -- app-driven build integration tests don't need an extra lot pick per component.
    UPDATE part SET is_lot_tracked = TRUE WHERE id IN (3007, 3012, 3013);

    -- tracking_mode backfill (#743): mirrors is_lot_tracked (FALSE->'none', TRUE->'lot') until
    -- the slice 8 read-swap; is_lot_tracked keeps driving reads until then.
    UPDATE part SET tracking_mode = 'lot' WHERE id IN (3007, 3012, 3013);

    -- Serial coverage for slice 8 (#745): 3013 → lot_serial (its unit 8501 carries BOTH a lot
    -- and a build), 3005 → serial (its unit 8503 is build-only). TracksLots('serial') is false,
    -- so 3005 stays un-lot-tracked and the #675 app build test is unaffected; is_lot_tracked
    -- stays in sync (3013 already TRUE; 3005 stays FALSE — serial does not imply lot control).
    UPDATE part SET tracking_mode = 'lot_serial' WHERE id = 3013;
    UPDATE part SET tracking_mode = 'serial'     WHERE id = 3005;

    -- 8301: purchased lot of 3007, received against po_line 5504 (PO 5003); lot_number
    --       defaults to the lot's own id (#687), vendor_lot_number is the supplier's
    --       own batch ID, lot_description records the PO as provenance.
    -- 8302: manufactured lot of sub-assembly 3012 (po_line_id NULL).
    -- 8303 (#737): manually-adjusted lot of 3007 — neither po_line_id nor an owning build,
    --       the third lot origin (found/uncounted stock folded in via cycle count) that
    --       today is inferred, not stored; a PRE-state row for the future lot.source enum
    --       (traceability epic #736 slice 7) to backfill as 'adjust'. Consumed below into
    --       8306, so it's not an orphan node in the genealogy graph.
    -- 8306 (#737): manufactured lot of top-level assembly 3013 (po_line_id NULL) — the
    --       second tier of the multi-level chain, output of build 8203.
    -- source (#744): purchase (po_line set) / build (build output) / adjust (manual/cycle-count).
    -- notes (#872): 8302 carries a multi-line note so the truncation/tooltip treatment in the
    -- lot lists and genealogy tables is exercised without hand-editing rows.
    INSERT INTO lot (id, part_id, lot_number, lot_description, vendor_lot_number, source, po_line_id, created_at, is_active, notes) VALUES
        (8301, 3007, '8301', 'PO 5003',                          'SS304-LOT-0088', 'purchase', 5504, '2026-05-15T00:00:00', TRUE, 'Vendor CoA on file.'),
        (8302, 3012, '8302', 'Build #8202',                      NULL,             'build',    NULL, '2026-05-25T00:00:00', TRUE, '[jolls 2026-05-25] Built from bar stock lot 8301.' || CHR(13) || CHR(10) || CHR(13) || CHR(10) || '[jolls 2026-05-26] Two housings reworked for finish; both re-inspected and passed.'),
        (8303, 3007, '8303', 'Cycle count - unlabeled found lot', NULL,            'adjust',   NULL, '2026-05-22T00:00:00', TRUE, NULL),
        (8306, 3013, '8306', 'Build #8203',                       NULL,            'build',    NULL, '2026-05-30T00:00:00', TRUE, NULL);

    -- 8401: 3012's lot 8302 consumed 1 unit of 3007's lot 8301 (bom line 3906, qty 1) — a
    --       one-level chain so a recursive query from 8302 resolves back to vendor lot 8301.
    -- 8402/8403 (#737): 3013's lot 8306 consumed 1 unit each of 8302 (bom line 3910) and
    --       8303 (bom line 3911) — two parents into one child (branching), and a second
    --       level on top of 8401 (8301→8302→8306), so a recursive trace from 8306 resolves
    --       back through BOTH raw lots (8301 via 8302, and 8303 directly). These three are
    --       lot->lot edges (unit columns NULL); the unit-endpoint edges 8404/8405 are added
    --       after the unit rows below (their FKs reference unit, which does not exist yet).
    INSERT INTO genealogy (id, parent_lot_id, child_lot_id, qty_consumed) VALUES
        (8401, 8301, 8302, 1),
        (8402, 8302, 8306, 1),
        (8403, 8303, 8306, 1);

    -- Stamp the seed receipt (5901, part 3007 against po_line 5504) with the lot it
    -- created (8301), exercising inventory_transaction.lot_id (#676).
    UPDATE inventory_transaction SET lot_id = 8301 WHERE id = 5901;

    -- Link build 8202/8203's output to their lots (deferred until the lots exist, mirroring
    -- the build handler: insert build → create lot → set output_lot_id). Gives each lot a
    -- real originating build so its lot_description shows "Build #NNNN" and the matching
    -- form_record can point at both.
    UPDATE build SET output_lot_id = 8302 WHERE id = 8202;
    UPDATE build SET output_lot_id = 8306 WHERE id = 8203;

    -- ============================================================
    -- 10d. Units (#740): Tier-3 serialized instances — additive/inert until slice 8.
    -- ============================================================
    -- Four PRE-state rows. 8501-8503 formerly covered every branch of CK_unit_provenance
    -- (dropped by #799 — a unit must carry a lot_id OR a build_id); that CHECK is gone, and
    -- 8504 now proves the branch it forbade (BOTH NULL) is legal:
    --   8501: final-tested serial of top assembly 3013 — BOTH lot 8306 and build 8203 set
    --         (the keystone case: a build-sourced serialized unit that also lives in a lot).
    --   8502: serial of raw 3007 received inside purchased lot 8301 — lot only, build NULL.
    --   8503: serial of assembly 3005 from non-lot-tracked build 8201 — build only, lot NULL.
    --   8504 (#799): manually back-filled serial of part 3005 — lot_id AND build_id both
    --         NULL, source = 'manual'. Proves a provenance-less unit is legal and that
    --         PartBuild's TestedCount completeness numerator ignores manual units.
    -- serial_number is a string, UNIQUE per part_id (UQ_unit_serial). source (#799) defaults
    -- to 'test' — every unit here except 8504 was minted from a test record.
    INSERT INTO unit (id, part_id, lot_id, build_id, serial_number, created_at, is_active, source) VALUES
        (8501, 3013, 8306, 8203, 'SN-3013-001', '2026-05-30T00:00:00', TRUE, 'test'),
        (8502, 3007, 8301, NULL, 'SN-3007-A1',  '2026-05-15T00:00:00', TRUE, 'test'),
        (8503, 3005, NULL, 8201, 'SN-3005-001', '2026-05-25T00:00:00', TRUE, 'test'),
        (8504, 3005, NULL, NULL, 'SN-3005-MANUAL-1', '2026-07-20T00:00:00', TRUE, 'manual');

    -- 8404/8405 (#746): unit-endpoint genealogy edges — inserted here (not with the lot->lot
    -- edges above) because their FKs reference unit, which is created just above. They give
    -- final-tested serial 8501 (a unit of top assembly 3013) a MIXED as-built ancestry: parent
    -- UNIT 8503 (serialized sub-assembly of 3005) AND parent raw LOT 8301, so the per-serial
    -- traceability walk (slice 9) resolves both a unit parent and a lot parent. Exercises the
    -- widened genealogy's unit columns and the exactly-one-parent/exactly-one-child CHECK.
    INSERT INTO genealogy (id, parent_lot_id, parent_unit_id, child_lot_id, child_unit_id, qty_consumed) VALUES
        (8404, NULL, 8503, NULL, 8501, 1),
        (8405, 8301, NULL, NULL, 8501, 2);

    -- ============================================================
    -- 11. Test records — form, form_row, form_record, result,
    --     record_events, record_event_results
    -- ============================================================
    -- Form hangs off FORM-category part 3010 (FormsList filters pn.category='FORM').
    -- Steps 6105-6108 were "added after the records below were created" — they're in the
    -- form's test_order but not the records' snapshots, so new records pick them up while
    -- existing ones don't (realistic form evolution, and keeps the frozen-row model honest).
    -- form_type (#744) = the kind of quality document (orthogonal to record_types); this seed form is a test.
    INSERT INTO form (id, part_number_id, test_order, is_locked, is_active, form_type, record_types, instrument_types, revision) VALUES
        (6001, 3010, '6101,6102,6103,6104,6105,6106,6107,6108', TRUE, TRUE, 'test', 'New Release,Re-Test', 'ModelA,ModelB', 1);

    -- 6104 is an archived (retired) step: hidden from new records and the live definition view
    -- (revealed by the "Show archived" toggle), but still rendered on historical records that
    -- recorded a result for it — exercises stepVisibleOnRecord / the #487 frozen-row model.
    -- 6105-6108 exercise the non-default step features: pf_type filled/comment, format,
    -- List:/query: spec_nom pickers, {id} cross-step tokens, default_result (including the
    -- min/max/abs/mod/round/floor/ceil/sqrt/pow functions from #95), and hide_formula.
    -- updated_at pinned to a fixed sentinel (see the app_config seed comment above) so
    -- TestIntegration_UpdatedAtSentinel can assert these rows go untouched.
    -- granularity (#744): all existing form lines are per-unit tests ('unit'); the DEFAULT also covers this.
    INSERT INTO form_row (id, form_id, revision, type, parameter, specification, spec_units, spec_min, spec_nom, spec_max, pf_type, instrument_types, archived, format, default_result, hide_formula, updated_at, granularity) VALUES
        (6101, 6001, 1, 1, 'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    NULL,            FALSE, NULL,   NULL,  NULL, '2020-01-01T00:00:00', 'unit'),
        (6102, 6001, 1, 0, 'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 'ModelA,ModelB', FALSE, NULL,   NULL,  NULL, '2020-01-01T00:00:00', 'unit'),
        (6103, 6001, 1, 0, 'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '250', 'range', 'ModelA,ModelB', FALSE, NULL,   NULL,  NULL, '2020-01-01T00:00:00', 'unit'),
        (6104, 6001, 1, 0, 'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 'ModelA,ModelB', TRUE, NULL,   NULL,  NULL, '2020-01-01T00:00:00', 'unit'), -- archived/retired step
        (6105, 6001, 1, 0, 'Firmware Version',      'Record installed version', NULL, NULL, NULL, NULL, 'filled', NULL, FALSE, NULL,   'v2.1',NULL, '2020-01-01T00:00:00', 'unit'),
        (6106, 6001, 1, 0, 'Visual Inspection',     'No scratches or dents',    NULL, NULL, 'List:Pass;Fail', NULL, 'filled', NULL, FALSE, NULL, NULL, NULL, '2020-01-01T00:00:00', 'unit'),
        (6107, 6001, 1, 0, 'Previous Serial Number','Prior unit tested on this form', NULL, NULL, 'query:recent_serial_numbers_for_form(@form_id={form.id})', NULL, 'comment', NULL, FALSE, NULL, NULL, NULL, '2020-01-01T00:00:00', 'unit'),
        (6108, 6001, 1, 0, 'Retest Voltage Check',  'Re-measure output ({6102} at first test)', 'V', '4.75', NULL, '5.25', 'range', NULL, FALSE, '0.00', '=min(max({6102},4.75),5.25)', '{record.type}!=Re-Test', '2020-01-01T00:00:00', 'unit'); -- only shown on Re-Test records; default_result clamps the prior reading to this step's spec range

    -- Exercise the definition-history timeline: tighten 6103's limit 250 → 200.
    -- trg_form_row_history snapshots the old row into form_row_history.
    UPDATE form_row SET spec_max = '200' WHERE id = 6103;

    -- updated_at pinned to a fixed sentinel (see the app_config seed comment above) so
    -- TestIntegration_UpdatedAtSentinel can assert these rows go untouched.
    -- 7001's created_at is pinned well outside staleWIPThresholdDays (14 days) so it
    -- surfaces on the Reports dashboard's Stale WIP Records card (issue #658, RPT-7);
    -- the rest just mirror record_date, since the app sets created_at at record-creation
    -- time and none of them need to look stale (all are locked except 7001).
    -- lot_id / build_id (#677) trace a serialized unit to the lot/build that produced it.
    -- 7001-7009 predate lot control (both NULL — the legacy/unlinked case). 7010 links a
    -- lot-tracked part (3012) to both its lot (8302) and build (8202); 7011 links a NON-
    -- lot-tracked assembly (3005) to its build (8201) only — the build_id-without-lot_id
    -- case that build_id exists to cover. Both are WIP with recent (non-stale) dates.
    INSERT INTO form_record (id, form_id, part_id, record_date, serial_number, subject_part_number, subject_pn_description, test_order, record_type, instrument_type, is_locked, is_approved, is_active, form_revision, lot_id, build_id, unit_id, updated_at, created_at, notes) VALUES
        (7001, 6001, 3004, '2026-06-01', '7001', 'MFG-1001', 'Drone Frame Housing', '6101,6102,6103,6104', 'New Release', 'ModelA', FALSE, FALSE, TRUE, 1, NULL, NULL, NULL, '2020-01-01T00:00:00', '2026-06-01T00:00:00', NULL), -- WIP, stale
        (7002, 6001, 3004, '2026-06-02', '7002', 'MFG-1001', 'Drone Frame Housing', '6101,6102,6103,6104', 'Re-Test',     'ModelA', TRUE, FALSE, TRUE, 1, NULL, NULL, NULL, '2020-01-01T00:00:00', '2026-06-02T00:00:00', NULL), -- Complete
        (7003, 6001, 3004, '2026-06-03', '7003', 'MFG-1001', 'Drone Frame Housing', '6101,6102,6103,6104', 'New Release', 'ModelB', TRUE, TRUE, TRUE, 1, NULL, NULL, NULL, '2020-01-01T00:00:00', '2026-06-03T00:00:00', NULL), -- Approved (locked twice — see events)
        (7004, 6001, 3004, '2026-06-04', '7004', 'MFG-1001', 'Drone Frame Housing', '6101,6102,6103,6104', 'New Release', 'ModelA', FALSE, FALSE, FALSE, 1, NULL, NULL, NULL, '2020-01-01T00:00:00', '2026-06-04T00:00:00', NULL), -- soft-deleted
        (7005, 6001, 3004, '2026-06-05', '7005', 'MFG-1001', 'Drone Frame Housing', '6101,6102,6103,6104', 'Re-Test',     'ModelA', TRUE, FALSE, TRUE, 1, NULL, NULL, NULL, '2020-01-01T00:00:00', '2026-06-05T00:00:00', NULL), -- Complete, pre-#251 style (backfillable)
        -- 7006-7009 span May and July so the Reports > Yield Summary "Group by month" view (#244) has more than one month to show.
        (7006, 6001, 3004, '2026-05-15', '7006', 'MFG-1001', 'Drone Frame Housing', '6101,6102,6103,6104', 'New Release', 'ModelA', TRUE, FALSE, TRUE, 1, NULL, NULL, NULL, '2020-01-01T00:00:00', '2026-05-15T00:00:00', NULL), -- Complete, all pass
        (7007, 6001, 3004, '2026-05-20', '7007', 'MFG-1001', 'Drone Frame Housing', '6101,6102,6103,6104', 'New Release', 'ModelB', TRUE, TRUE, TRUE, 1, NULL, NULL, NULL, '2020-01-01T00:00:00', '2026-05-20T00:00:00', NULL), -- Approved, has a FAIL
        (7008, 6001, 3004, '2026-07-01', '7008', 'MFG-1001', 'Drone Frame Housing', '6101,6102,6103,6104', 'New Release', 'ModelA', TRUE, FALSE, TRUE, 1, NULL, NULL, NULL, '2020-01-01T00:00:00', '2026-07-01T00:00:00', NULL), -- Complete, all pass
        (7009, 6001, 3004, '2026-07-05', '7009', 'MFG-1001', 'Drone Frame Housing', '6101,6102,6103,6104', 'New Release', 'ModelA', TRUE, FALSE, TRUE, 1, NULL, NULL, NULL, '2020-01-01T00:00:00', '2026-07-05T00:00:00', NULL), -- Complete, all pass
        (7010, 6001, 3012, '2026-07-10', '7010', 'ASM-1002', 'Sub-Assembly',  '6101,6102,6103,6104', 'New Release', 'ModelA', FALSE, FALSE, TRUE, 1, 8302, 8202, NULL, '2020-01-01T00:00:00', '2026-07-10T00:00:00', 'Session run on bench 2; DMM swapped mid-session after a fuse blew (#870 seed note).'), -- WIP, lot-tracked part → lot 8302 + build 8202 (#677)
        (7011, 6001, 3005, '2026-07-11', '7011', 'ASM-1001', 'Skyrunner Standard Drone','6101,6102,6103,6104', 'New Release', 'ModelA', FALSE, FALSE, TRUE, 1, NULL, 8201, 8503, '2020-01-01T00:00:00', '2026-07-11T00:00:00', NULL), -- WIP, non-lot-tracked assembly → build 8201 only (#677); unit 8503 under test (#742, Q8 — denormalized lot/build equal the unit's)
        (7012, 6001, 3007, '2026-05-16', '7012', 'RAW-1002', 'Stainless Steel Bar Stock', '6101,6102,6103,6104', 'New Release', 'ModelA', FALSE, FALSE, TRUE, 1, 8301, NULL, 8502, '2020-01-01T00:00:00', '2026-05-16T00:00:00', NULL), -- WIP, Incoming Inspection: lot 8301 → build_id NULL (#737 PRE-state row; reuses form 6001 purely to cover the receipt-not-yet-consumed case, not a realistic electrical test on bar stock); unit 8502 under test (#742, Q8)
        (7013, 6001, 3013, '2026-05-31', 'SN-3013-001', 'ASM-1003', 'Skyrunner Deluxe Drone', '6101,6102,6103,6104', 'New Release', 'ModelA', FALSE, FALSE, TRUE, 1, 8306, 8203, 8501, '2020-01-01T00:00:00', '2026-05-31T00:00:00', NULL), -- WIP, Final Test on the top-level assembly: lot 8306 + build 8203 (#737) — completes the receipt(7012)→build(8202)→build(8203)→final-test flow (plan §0); unit 8501 under test (#742, Q8)
        -- Retest of unit 8501 (#745): a SECOND form_record pointing at the SAME unit — the
        -- retest-as-same-unit case. serial_number matches unit 8501's ('SN-3013-001') and
        -- unit_id is reused; lot_id/build_id are NULL (the slice-8 write-shape: provenance is
        -- read THROUGH the unit, Q8), so this row also exercises loadRecordTrace's read-through.
        (7014, 6001, 3013, '2026-06-15', 'SN-3013-001', 'ASM-1003', 'Skyrunner Deluxe Drone', '6101,6102,6103,6104,6108', 'Re-Test', 'ModelA', FALSE, FALSE, TRUE, 1, NULL, NULL, 8501, '2020-01-01T00:00:00', '2026-06-15T00:00:00', NULL);

    -- One materialized row per step per record, headings included (type=1) — matching what
    -- materializeRecordSteps produces for post-#487 records. Grouped by record.
    -- updated_at pinned to a fixed sentinel (see the app_config seed comment above) so
    -- TestIntegration_UpdatedAtSentinel can assert these rows go untouched.
    INSERT INTO result (id, form_record_id, form_row_id, pass_fail, result, parameter, specification, spec_units, spec_min, spec_nom, spec_max, pf_type, type, updated_at) VALUES
        (7101, 7001, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7102, 7001, 6102, NULL, NULL,   'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7103, 7001, 6103, NULL, NULL,   'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'),
        (7104, 7001, 6104, NULL, NULL,   'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'),
        (7105, 7002, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7106, 7002, 6102, TRUE, '5.01', 'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7107, 7002, 6103, TRUE, '150',  'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'),
        (7108, 7002, 6104, TRUE, '250',  'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'),
        (7109, 7003, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7110, 7003, 6102, TRUE, '4.98', 'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7111, 7003, 6103, TRUE, '180',  'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'),
        (7112, 7003, 6104, TRUE, '310',  'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'), -- corrected 300→310 on re-lock; see events
        (7113, 7005, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7114, 7005, 6102, TRUE, '5.10', 'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7115, 7005, 6103, FALSE, '210',  'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'), -- FAIL row
        (7116, 7005, 6104, TRUE, '150',  'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'),
        (7117, 7006, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7118, 7006, 6102, TRUE, '5.02', 'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7119, 7006, 6103, TRUE, '160',  'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'),
        (7120, 7006, 6104, TRUE, '200',  'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'),
        (7121, 7007, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7122, 7007, 6102, TRUE, '4.90', 'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7123, 7007, 6103, FALSE, '220',  'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'), -- FAIL row
        (7124, 7007, 6104, TRUE, '180',  'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'),
        (7125, 7008, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7126, 7008, 6102, TRUE, '4.99', 'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7127, 7008, 6103, TRUE, '170',  'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'),
        (7128, 7008, 6104, TRUE, '220',  'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'),
        (7129, 7009, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7130, 7009, 6102, TRUE, '5.05', 'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7131, 7009, 6103, TRUE, '190',  'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'),
        (7132, 7009, 6104, TRUE, '240',  'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'),
        -- 7010 / 7011 (#677) are WIP with empty results, mirroring 7001's unfilled snapshot rows.
        (7133, 7010, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7134, 7010, 6102, NULL, NULL,   'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7135, 7010, 6103, NULL, NULL,   'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'),
        (7136, 7010, 6104, NULL, NULL,   'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'),
        (7137, 7011, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7138, 7011, 6102, NULL, NULL,   'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7139, 7011, 6103, NULL, NULL,   'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'),
        (7140, 7011, 6104, NULL, NULL,   'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'),
        -- 7012 / 7013 (#737) are WIP with empty results, same unfilled pattern as 7010/7011.
        (7141, 7012, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7142, 7012, 6102, NULL, NULL,   'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7143, 7012, 6103, NULL, NULL,   'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'),
        (7144, 7012, 6104, NULL, NULL,   'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'),
        (7145, 7013, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7146, 7013, 6102, NULL, NULL,   'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7147, 7013, 6103, NULL, NULL,   'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'),
        (7148, 7013, 6104, NULL, NULL,   'Insulation Resistance', '>100 Mohm',   'Mohm','100', NULL, NULL,  'range', 0, '2020-01-01T00:00:00'),
        -- 7014 (#745) is the retest of unit 8501 — WIP with empty results; includes the
        -- Re-Test-only step 6108 and skips archived 6104, matching a fresh Re-Test snapshot.
        (7149, 7014, 6101, NULL, NULL,   'Electrical Tests',      NULL,          NULL, NULL,  NULL,  NULL,  NULL,    1, '2020-01-01T00:00:00'),
        (7150, 7014, 6102, NULL, NULL,   'Output Voltage',        '5V +/-0.25V', 'V',  '4.75','5.00','5.25','range', 0, '2020-01-01T00:00:00'),
        (7151, 7014, 6103, NULL, NULL,   'Current Draw',          '<=200mA',     'mA', NULL,  NULL,  '200', 'range', 0, '2020-01-01T00:00:00'),
        (7152, 7014, 6108, NULL, NULL,   'Retest Voltage Check',  'Re-measure output', 'V', '4.75', NULL, '5.25','range', 0, '2020-01-01T00:00:00');

    -- A 'completed' event + per-result snapshot is captured on every lock (#251).
    -- 7003 was locked twice (result 6104 corrected 300→310 in between) so the record detail
    -- view has a real snapshot-to-snapshot diff. 7005's completed event deliberately has NO
    -- snapshot rows — a pre-#251 record that makes the backfill bulk action appear.
    INSERT INTO record_events (id, form_record_id, event_type, username, event_date, comments) VALUES
        (7201, 7003, 'completed', 'admin', '2026-06-03', 'Initial completion'),
        (7202, 7002, 'completed', 'tester','2026-06-02', 'Marked complete'),
        (7203, 7005, 'completed', 'tester','2026-06-05', 'Completed before per-lock snapshots existed'),
        (7204, 7003, 'unlocked',  'admin', '2026-06-08', 'Correcting insulation resistance reading'),
        (7205, 7003, 'completed', 'admin', '2026-06-09', 'Approved after correction'),
        (7206, 7006, 'completed', 'tester','2026-05-15', 'Marked complete'),
        (7207, 7007, 'completed', 'tester','2026-05-20', 'Marked complete'),
        (7208, 7008, 'completed', 'tester','2026-07-01', 'Marked complete'),
        (7209, 7009, 'completed', 'tester','2026-07-05', 'Marked complete');

    INSERT INTO record_event_results (id, event_id, form_row_id, parameter, specification, spec_units, result, pass_fail) VALUES
        (7301, 7201, 6102, 'Output Voltage',        '5V +/-0.25V', 'V',   '4.98', TRUE),
        (7302, 7201, 6103, 'Current Draw',          '<=200mA',     'mA',  '180',  TRUE),
        (7303, 7201, 6104, 'Insulation Resistance', '>100 Mohm',   'Mohm','300',  TRUE),
        (7304, 7202, 6102, 'Output Voltage',        '5V +/-0.25V', 'V',   '5.01', TRUE),
        (7305, 7202, 6103, 'Current Draw',          '<=200mA',     'mA',  '150',  TRUE),
        (7306, 7202, 6104, 'Insulation Resistance', '>100 Mohm',   'Mohm','250',  TRUE),
        (7307, 7205, 6102, 'Output Voltage',        '5V +/-0.25V', 'V',   '4.98', TRUE),
        (7308, 7205, 6103, 'Current Draw',          '<=200mA',     'mA',  '180',  TRUE),
        (7309, 7205, 6104, 'Insulation Resistance', '>100 Mohm',   'Mohm','310',  TRUE); -- changed vs event 7201 → diff shows it

    -- form_events: audit trail for the form's release (revision 1 = released once).
    INSERT INTO form_events (id, form_id, event_type, username, event_date, comments) VALUES
        (6201, 6001, 'locked', 'admin', '2026-05-28', 'Released revision 1');

    -- ============================================================
    -- 12. Reseed identities above the fixed-ID ranges so ad hoc rows
    --     created during manual testing / integration tests never
    --     collide with the reference dataset.
    -- ============================================================
    SELECT setval(pg_get_serial_sequence('company', 'id'), 1099);
    SELECT setval(pg_get_serial_sequence('contact', 'id'), 2099);
    SELECT setval(pg_get_serial_sequence('part', 'id'), 3099);
    SELECT setval(pg_get_serial_sequence('bom', 'id'), 3999);
    SELECT setval(pg_get_serial_sequence('supplier_part', 'id'), 4099);
    SELECT setval(pg_get_serial_sequence('mfg_part', 'id'), 4199);
    SELECT setval(pg_get_serial_sequence('price', 'id'), 4299);
    SELECT setval(pg_get_serial_sequence('purchase_order', 'id'), 5099);
    SELECT setval(pg_get_serial_sequence('po_line', 'id'), 5599);
    SELECT setval(pg_get_serial_sequence('purchase_order_history', 'id'), 5899);
    SELECT setval(pg_get_serial_sequence('inventory_transaction', 'id'), 5999);
    SELECT setval(pg_get_serial_sequence('build', 'id'), 8299);
    SELECT setval(pg_get_serial_sequence('form', 'id'), 6099);
    SELECT setval(pg_get_serial_sequence('form_events', 'id'), 6299);
    SELECT setval(pg_get_serial_sequence('form_row', 'id'), 6199);
    SELECT setval(pg_get_serial_sequence('form_record', 'id'), 7099);
    SELECT setval(pg_get_serial_sequence('result', 'id'), 7199);
    SELECT setval(pg_get_serial_sequence('record_events', 'id'), 7299);
    SELECT setval(pg_get_serial_sequence('record_event_results', 'id'), 7399);
    SELECT setval(pg_get_serial_sequence('users', 'id'), 8099);
    SELECT setval(pg_get_serial_sequence('part_attachment', 'id'), 8199);
    SELECT setval(pg_get_serial_sequence('named_queries', 'id'), 99);
    -- lot / genealogy / unit are not reseeded here — the SQL Server original's DBCC
    -- CHECKIDENT list doesn't cover them either (added after the reseed block was last
    -- updated); mirrored as-is rather than fixed here, since fixing it is out of #829's scope.

    -- po_number_seq: restart well above the highest fixed PO base number (5010).
    -- The sequence itself is created unconditionally in SQL/postgres/purchase_order.sql.
    ALTER SEQUENCE po_number_seq RESTART WITH 9001;

COMMIT;
