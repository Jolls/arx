# #194 — Part/attachment categories: real tables instead of JSON in app_config

Depends on (applied first): `docs/plans/dead-column-cleanup.md` (creates `SQL/postgres/migrations/` plus its self-registration lint), and #192 timestamptz. `main` is Postgres-only: do not touch `SQL/azure/`.

## Open questions (plan assumes the recommendation)
1. **FK on `part_attachment.category`?** Today it's free text: "Custom..." in the `category_select` partial (`partials.html:59`) lets users type anything; code hardcodes "Photo", "Datasheet", "PDF Preview", "Thumbnail"; seed has custom 'COA' (row 8103); integration tests post `itest-71`, `integration-test-category`. (A) Full FK; (B, recommended) table only, no FK, Custom... stays; (C) FK + Custom... inserts a row.
2. **`''` part category** → NULL (recommended). Migrate `''` to NULL, keep `DEFAULT 'BUY'`, write NULL when form sends empty.
3. **Part-numbering settings:** out of scope (single-value JSON config, no FK target).
4. **Stale cache across multiple exes:** keep per-exe cache, no cross-exe refresh; FK stops bad writes.
5. **Renaming a category code:** no rename in editor (delete+add); in-use code gets the "still in use" error. No `ON UPDATE CASCADE`.

## Decisions
- New table `part_category`, keyed on `code`. `part.category` gets an FK to it. The fixed `CK_part_number_category` CHECK is dropped. An empty `''` category becomes NULL (Q2).
- New table `attachment_category`, keyed on `display_name`. It is the source of the attachment Category dropdown. **No FK** on `part_attachment.category`, and "Custom..." stays (Q1 = B).
- The app_config keys `part_categories` and `attachment_categories` are removed. The tables are the only source Go reads and writes.
- Part categories stay cached in `runtimeState.partCategories`. Attachment categories stay a per-request read, as today, now from the table.
- Removing a category that parts still use is rejected before any write, with a Settings error banner. The FK (default NO ACTION) catches the race case.
- Natural key (`code`, `display_name`) instead of an `id` PK is a deliberate break from the SQL/schema.md convention; note this in schema.md.
- Breaking change: bump `app_config.schema_version` and `ExpectedSchemaVersion` by +1 from whatever value is on the branch when you implement.
- Booleans: `is_purchased`, `is_bom_visible`, `is_orders_visible`, `is_pricing_visible`, `is_mfg_parts_visible`, `is_suppliers_visible`, `is_inventory_visible`.

## 1. Schema

### `SQL/postgres/part_category.sql` (new)
```sql
DROP TABLE IF EXISTS part_category CASCADE;
CREATE TABLE part_category (
  code                 VARCHAR(10)  NOT NULL PRIMARY KEY,   -- = part.category (FK_part_category)
  label                VARCHAR(255) NOT NULL DEFAULT '',
  is_purchased         BOOLEAN      NOT NULL DEFAULT FALSE,
  is_bom_visible       BOOLEAN      NOT NULL DEFAULT FALSE,
  is_orders_visible    BOOLEAN      NOT NULL DEFAULT FALSE,
  is_pricing_visible   BOOLEAN      NOT NULL DEFAULT FALSE,
  is_mfg_parts_visible BOOLEAN      NOT NULL DEFAULT FALSE,
  is_suppliers_visible BOOLEAN      NOT NULL DEFAULT FALSE,
  is_inventory_visible BOOLEAN      NOT NULL DEFAULT FALSE,
  sort_order           INTEGER      NOT NULL DEFAULT 0,
  created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ  NOT NULL DEFAULT now()
);
```
Header comment: edited through Settings → Part Categories; rows in use by `part` can't be deleted.

### `SQL/postgres/attachment_category.sql` (new)
```sql
DROP TABLE IF EXISTS attachment_category CASCADE;
CREATE TABLE attachment_category (
  display_name VARCHAR(500) NOT NULL PRIMARY KEY,  -- option text; part_attachment.category stays free text (no FK)
  sort_order   INTEGER      NOT NULL DEFAULT 0,
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
```

### `SQL/postgres/part.sql`
- Line 11–12: `category VARCHAR(10) DEFAULT 'BUY'`. Remove the `CONSTRAINT CK_part_number_category CHECK (...)`; comment `-- FK to part_category.code; NULL = uncategorized (#194)`.
- After the other FKs: `ALTER TABLE part ADD CONSTRAINT FK_part_category FOREIGN KEY (category) REFERENCES part_category (code);`

### `SQL/postgres/part_attachment.sql`
- Line 14 comment: `-- Free-text category; dropdown options from attachment_category (#194).`

### `SQL/postgres/app_config.sql`
- Delete line 14 (the `attachment_categories` INSERT). Update the `schema_version` value to the new version.

### `SQL/postgres/README.md`
- In "Suggested run order", add `part_category` before `part` and `attachment_category` before `part_attachment`.

### `SQL/postgres/seed_test_data.sql`
- DELETE block: add `DELETE FROM part_category;` right after `DELETE FROM part;` (line 79), and `DELETE FROM attachment_category;` after `DELETE FROM part_attachment;` (line 53).
- app_config INSERT (lines 114–116): remove the `attachment_categories` row and set `schema_version` to the new version.
- Before the `INSERT INTO part` (line 205), mirroring `models.DefaultCategories()`:
```sql
INSERT INTO part_category (code, label, is_purchased, is_bom_visible, is_orders_visible, is_pricing_visible, is_mfg_parts_visible, is_suppliers_visible, is_inventory_visible, sort_order, created_at, updated_at) VALUES
  ('ASM','Assembly',FALSE,TRUE,TRUE,TRUE,TRUE,TRUE,TRUE,0,'2020-01-01T00:00:00Z','2020-01-01T00:00:00Z'),
  ('BUY','Purchased',TRUE,FALSE,TRUE,TRUE,TRUE,TRUE,TRUE,1,...),
  ('DWG','Drawing',FALSE,FALSE,FALSE,FALSE,FALSE,FALSE,FALSE,2,...),
  ('DOC','Document',FALSE,FALSE,FALSE,FALSE,FALSE,FALSE,FALSE,3,...),
  ('FORM','Test Form',FALSE,TRUE,FALSE,FALSE,FALSE,FALSE,FALSE,4,...),
  ('MFG','Manufactured',FALSE,TRUE,TRUE,TRUE,TRUE,TRUE,TRUE,5,...),
  ('OPS','Operation / Labor',FALSE,FALSE,FALSE,FALSE,FALSE,FALSE,FALSE,6,...),
  ('RAW','Raw Material',TRUE,FALSE,TRUE,TRUE,TRUE,TRUE,TRUE,7,...),
  ('SVC','Service',TRUE,FALSE,TRUE,TRUE,TRUE,TRUE,FALSE,8,...),
  ('TOOL','Tooling',TRUE,FALSE,TRUE,TRUE,TRUE,TRUE,FALSE,9,...);
INSERT INTO attachment_category (display_name, sort_order, created_at, updated_at) VALUES
  ('Vendor Link',0,...),('Drawing',1,...),('CAD',2,...),('Datasheet',3,...),('Vendor Document',4,...),
  ('Fabrication',5,...),('Schematic',6,...),('Quote',7,...),('BOM',8,...),('SOP',9,...),
  ('Certificate',10,...),('Photo',11,...),('PDF Preview',12,...),('Thumbnail',13,...);
```
(`...` = the same fixed `'2020-01-01T00:00:00Z'` pair.) Verify the flag values against `models.DefaultCategories()` when writing. No `setval` (no identity column).

### Migration `SQL/postgres/migrations/<YYYYMMDDHHMMSS>_194_category_tables.sql` (new)
Authoring timestamp later than sibling migrations. Match the dead-column-cleanup migration's header, ArxDev guard and ledger style. Whole file in `BEGIN; ... COMMIT;`. Steps:
1. `CREATE TABLE IF NOT EXISTS part_category (...)` and `CREATE TABLE IF NOT EXISTS attachment_category (...)`, same columns as above.
2. Inside the DO block of step 3, `RAISE NOTICE` for each saved code longer than 10 chars (skipped).
3. Import the saved JSON (Go JSON keys `code`, `label`, `purchased`, `bom`, `orders`, `pricing`, `mfgParts`, `suppliers`, `inventory`; missing = false):
   ```sql
   DO $$ BEGIN
     INSERT INTO part_category (code,label,is_purchased,is_bom_visible,is_orders_visible,is_pricing_visible,is_mfg_parts_visible,is_suppliers_visible,is_inventory_visible,sort_order)
     SELECT DISTINCT ON (upper(btrim(e->>'code'))) upper(btrim(e->>'code')), left(COALESCE(btrim(e->>'label'),''),255),
            COALESCE((e->>'purchased')::boolean,FALSE), COALESCE((e->>'bom')::boolean,FALSE), COALESCE((e->>'orders')::boolean,FALSE),
            COALESCE((e->>'pricing')::boolean,FALSE), COALESCE((e->>'mfgParts')::boolean,FALSE), COALESCE((e->>'suppliers')::boolean,FALSE),
            COALESCE((e->>'inventory')::boolean,FALSE), (ord-1)::int
     FROM app_config ac, jsonb_array_elements(ac.setting_value::jsonb) WITH ORDINALITY t(e, ord)
     WHERE ac.setting_key='part_categories' AND length(btrim(COALESCE(e->>'code',''))) BETWEEN 1 AND 10
     ORDER BY upper(btrim(e->>'code')), ord
     ON CONFLICT (code) DO NOTHING;
   EXCEPTION WHEN invalid_text_representation OR invalid_parameter_value THEN
     RAISE NOTICE 'app_config.part_categories is not a valid JSON array; using defaults';
   END $$;
   ```
4. Insert the 10 default rows (seed values, no fixed timestamps) via `INSERT ... SELECT * FROM (VALUES ...) v WHERE NOT EXISTS (SELECT 1 FROM part_category)`.
5. Add codes parts already use but config lacks, with permissive `DefaultCategoryTabs` flags:
   ```sql
   INSERT INTO part_category (code,label,is_orders_visible,is_pricing_visible,is_mfg_parts_visible,is_suppliers_visible,is_inventory_visible,sort_order)
   SELECT c, c, TRUE,TRUE,TRUE,TRUE,TRUE, (SELECT COALESCE(MAX(sort_order),-1) FROM part_category) + ROW_NUMBER() OVER (ORDER BY c)
   FROM (SELECT DISTINCT category AS c FROM part WHERE category IS NOT NULL AND category <> '') d
   WHERE NOT EXISTS (SELECT 1 FROM part_category pc WHERE pc.code = d.c);
   ```
6. Import attachment categories from the comma list, first occurrence wins:
   ```sql
   INSERT INTO attachment_category (display_name, sort_order)
   SELECT DISTINCT ON (btrim(x)) btrim(x), (ord-1)::int
   FROM app_config ac, unnest(string_to_array(ac.setting_value, ',')) WITH ORDINALITY t(x, ord)
   WHERE ac.setting_key='attachment_categories' AND btrim(x) <> ''
   ORDER BY btrim(x), ord
   ON CONFLICT (display_name) DO NOTHING;
   ```
   Then the 14 defaults, guarded by `WHERE NOT EXISTS (SELECT 1 FROM attachment_category)`.
7. `UPDATE part SET category = NULL WHERE category = '';`
8. `ALTER TABLE part DROP CONSTRAINT IF EXISTS ck_part_number_category;`
9. Orphan check (0 rows expected): `SELECT p.id, p.category FROM part p LEFT JOIN part_category pc ON pc.code = p.category WHERE p.category IS NOT NULL AND pc.code IS NULL;`
10. `ALTER TABLE part DROP CONSTRAINT IF EXISTS fk_part_category;` then `ALTER TABLE part ADD CONSTRAINT FK_part_category FOREIGN KEY (category) REFERENCES part_category (code);`
11. `DELETE FROM app_config WHERE setting_key IN ('part_categories','attachment_categories');`
12. `UPDATE app_config SET setting_value = '<new>' WHERE setting_key = 'schema_version';`
13. Comment: DBA grants `SELECT, INSERT, UPDATE, DELETE` on both new tables to the app login (SQL/schema.md "Database privileges").
14. Last statement: `INSERT INTO schema_migrations (version_id, is_applied) SELECT <ts>, TRUE WHERE NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version_id = <ts>);`

## 2. Go

### `internal/config/config.go`
- Line 208–228 block: add `func (c *Config) PartCategoryTable() string { return "part_category" }` and `func (c *Config) AttachmentCategoryTable() string { return "attachment_category" }`.
- Line 21: bump `ExpectedSchemaVersion`.

### `arx_go/models/part.go`
- `Category` comment (63–65): "Stored in the part_category table (#194); DefaultCategories is the fallback when no DB is connected."
- `DefaultCategories` comment (80–81): "Built-in list used when no DB is connected; mirrors the part_category seed rows."
- Keep the JSON tags.

### `arx_go/categories.go` (rewrite body; keep `applyCategoryTabs` unchanged)
- Remove the `partCategoriesKey` const and `encoding/json` import. Add `fmt`, `log`, `sort`.
- `loadPartCategories(ctx)`: `cats := models.DefaultCategories()`; if `h.database() != nil`, call `fetchPartCategories` — on error `log.Printf("warning: could not load part categories: %v", err)` and keep defaults, on success use the result (possibly empty). Then `h.update(func(s *runtimeState){ s.partCategories = cats })`.
- `fetchPartCategories(ctx) ([]models.Category, error)`: `h.queryContext` `SELECT code, label, is_purchased, is_bom_visible, is_orders_visible, is_pricing_visible, is_mfg_parts_visible, is_suppliers_visible, is_inventory_visible FROM %s ORDER BY sort_order, code` (PartCategoryTable). Result initialized to `[]models.Category{}`; check `rows.Err()`.
- `partCategoryUsage(ctx) (map[string]int, error)`: `SELECT category, COUNT(*) FROM %s WHERE category IS NOT NULL GROUP BY category` (PartsTable).
- `categoriesInUse(usage map[string]int, keep map[string]bool) []string`: pure; sorted `"<CODE> (<n> parts)"` for codes in usage not in keep.
- `savePartCategories(ctx, cats []models.Category) error`: `h.beginTx`, `defer tx.Rollback()`; `SELECT code FROM part_category`; for each `i, c`: `INSERT INTO %s (code,label,is_purchased,is_bom_visible,is_orders_visible,is_pricing_visible,is_mfg_parts_visible,is_suppliers_visible,is_inventory_visible,sort_order) VALUES (@p1..@p10) ON CONFLICT (code) DO UPDATE SET label=EXCLUDED.label, is_purchased=EXCLUDED.is_purchased, ... , sort_order=EXCLUDED.sort_order, updated_at=now()`; for each existing code not submitted: `DELETE FROM %s WHERE code=@p1`; commit.
- `SettingsCategoriesSave`:
  - Keep nil-DB redirect.
  - `settingsError := func(msg string){ h.render(w, r, "settings/settings.html", h.settingsData(w, r, map[string]any{"Error": msg})) }` (pattern from `SettingsCompanyLogoSave`).
  - Parse rows as today plus `seen map[string]bool`: skip empty code **or already-seen code** (first wins).
  - `len(cats)==0` → `settingsError("At least one part category is required.")`.
  - `usage, err := h.partCategoryUsage` → on err `settingsError("Could not save categories: "+err.Error())`.
  - `blocked := categoriesInUse(usage, seen)` non-empty → `settingsError("Cannot remove part categories still used by parts: " + strings.Join(blocked, ", ") + ". Reassign those parts first.")`.
  - `savePartCategories` error → `settingsError("Could not save categories: "+err.Error())`.
  - Then `h.loadPartCategories`, redirect `/settings` (302).
  - Doc comment: persists to the part_category table.

### `arx_go/settings.go`
- Line 226: `"AttachmentCategories": strings.Join(h.loadAttachmentCategories(r.Context()), ","),`
- Lines 267–277 `SettingsAttachmentCategoriesSave`: replace `appConfigSet` with `h.saveAttachmentCategories(r.Context(), splitCSV(r.FormValue("attachment_categories")))`; keep the `log.Printf` warning and redirect; update comment.
- Helpers:
  - `loadAttachmentCategories(ctx) []string`: nil if `h.database()==nil`; else `SELECT display_name FROM %s ORDER BY sort_order, display_name`; on error log warning, return nil.
  - `saveAttachmentCategories(ctx, cats []string) error`: dedupe first-wins; one tx: `DELETE FROM %s`, then `INSERT INTO %s (display_name, sort_order) VALUES (@p1,@p2)` each; commit.
- Backup list (lines 671–682): append `h.cfg().PartCategoryTable(), h.cfg().AttachmentCategoryTable()`.

### `arx_go/parts.go`
- Line 1785: `cats := h.loadAttachmentCategories(r.Context())`; `if cats == nil { cats = []string{} }` before marshalling so JS `ATT_CATEGORIES` stays an array.
- Line 555 (insert) and 666 (update): `fv(r, "category")` → `nullableText(fv(r, "category"))`.

### `arx_go/build.go`
- Lines 106 and 233: `p.category` → `COALESCE(p.category, '') AS category` (scans at 118/245 into plain `string`).
- Audit other plain-`string` scans of `part.category` (grep `category` scans across arx_go) and apply the same COALESCE where the target is a plain string.

### `arx_go/templates/settings/settings.html`
- Part category code inputs (~355 row, ~373 template): `maxlength="10"`; label inputs: `maxlength="255"`.

## 3. Docs
- `SQL/schema.md`:
  - `part` row: "`category` → `part_category.code` (`FK_part_category`, #194); NULL = uncategorized. Codes managed in Settings → Part Categories; a code used by any part can't be deleted."
  - `part_attachment` row: options driven by `attachment_category`; still free text, no FK.
  - Table-reference rows for `part_category` (natural-key PK deviation, flags, sort_order, cached) and `attachment_category` (natural-key PK, sort_order, no FK).
  - Reference test data: add `part_category, attachment_category` row; drop "attachment categories" from the app_config row.
- `SQL/schema_diagram.md`: add `part_category` entity + `part_category ||--o{ part : "category (code)"` (full schema and Parts & sourcing domain); stand-alone `attachment_category`; `category` annotation on part (lines 59, 499) → `"FK part_category.code"`; update table count (line 8).
- `CLAUDE.md` "Config load order": "Attachment category options in `attachment_category` table; part categories in `part_category`".
- `CHANGELOG.md` (single batch entry): `### Fixed` parts saved with a Settings-added category no longer rejected (#194); `### Changed` categories moved to tables; requires migration (#194).
- `arx_go/RELEASE_NOTES.md` BUG FIXES: "Parts can now be saved with part categories added in Settings." / "Removing a part category that parts still use now shows a clear message."

## 4. Test plan

### 4.1 Coverage audit
- `categories_test.go` `TestSettingsCategoriesSave_NoDatabaseRedirects`: unchanged.
- `categories_integration_test.go`: tied to app_config JSON (`withRestoredPartCategories`, `readPersistedCategories`, `partCategoriesKey`); `PersistsRows` posts only TST1/TST2, which the in-use check will reject. **Rewrite (4.3).**
- `models/category_test.go`: unaffected; guards defaults the seed/migration must mirror.
- `settings_save_integration_test.go:128`: still valid (seed has rows).
- `state_test.go`: unaffected.
- `smoke_post_test.go` `seedPart` (BUY/FORM) and direct part INSERTs relying on `DEFAULT 'BUY'`: covered by seeded `part_category` rows.
- Attachment tests with free-text categories (integration_test.go 372, 5278–5444, 5692+, 6116+): unaffected under Q1=B.
- Gaps: attachment-category load/save, part save with a non-default category, NULL-category read in `build.go`, in-use deletion, backup table list.

### 4.2 Characterization tests (pass before and after)
- `TestIntegration_PartCategories_SeedMatchesDefaults` (`categories_integration_test.go`): after `h.loadPartCategories`, `h.st().partCategories` DeepEquals `models.DefaultCategories()`.
- `TestIntegration_PartAttachments_CategoryOptions` (`integration_test.go`): part 3002's attachments page body contains `<option value="X">` for each of the 14 seeded attachment categories, in seed order.

### 4.3 Red tests
In `categories_integration_test.go` unless noted. Replace helpers: `withRestoredPartCategories` snapshots `fetchPartCategories`, cleanup `savePartCategories(orig)` + `loadPartCategories`; `readPersistedCategories` → direct `SELECT ... FROM part_category ORDER BY sort_order, code`; add `withRestoredAttachmentCategories`.
1. `TestIntegration_PartCreate_SettingsAddedCategorySucceeds`: save seeded rows + `ZZT` via `SettingsCategoriesSave`, `PartsCreate` with `category=ZZT` → 302 to `/part/<id>`, row `category='ZZT'`. Cleanup deletes the part before category restore. Fails today on the CHECK.
2. `TestIntegration_SettingsCategoriesSave_RejectsRemovingInUse`: seeded set minus `BUY` → 200, body contains `Cannot remove part categories still used by parts` and `BUY (`; table still has BUY; cache unchanged.
3. `TestIntegration_SettingsCategoriesSave_PersistsRows` (rewrite): seeded set + `tst1` → table and cold `loadPartCategories` equal expected slice (TST1 uppercased/trimmed, in order).
4. `TestIntegration_SettingsCategoriesSave_DropsEmptyAndDuplicateCodeRows` (rewrite): seeded + whitespace code row + duplicate `ASM` with different label → blank dropped, first ASM label kept.
5. `TestIntegration_SettingsCategoriesSave_RejectsEmpty`: `cat_count=0` → 200 with `At least one part category is required.`, table unchanged.
6. `TestIntegration_PartCreate_EmptyCategoryStoresNull` (`integration_test.go`): `category=""` → `category IS NULL`; GET `/part/<id>` 200.
7. `TestIntegration_LoadBuildComponents_NullCategory` (`integration_test.go`): throwaway ASM parent, NULL-category component, bom row → `loadBuildComponents` no error, includes component. Clean up.
8. `TestIntegration_SettingsAttachmentCategoriesSave_PersistsOrder`: post `"Zeta, Alpha,Zeta, "` → rows `[Zeta(0), Alpha(1)]`; `loadAttachmentCategories` returns `[Zeta Alpha]`.
9. Unit `TestCategoriesInUse` (`categories_test.go`): usage `{BUY:3, ASM:1, RAW:2}`, keep `{ASM}` → `["BUY (3 parts)", "RAW (2 parts)"]`; empty usage → empty.

### 4.4 Manual-only
- Human runs the migration on ArxDev, then reseeds. Verify row counts (10 / 14), app_config keys gone, schema_version bumped, schema_migrations row, re-run no-op.
- Before ArxProd: review step-2/step-9 output.
- Settings UI: add a category and create a part with it; remove an in-use category → red banner; code input stops at 10 chars; attachment category order reflected in Add Attachment dropdown; Custom... still works.
- Settings backup zip contains `part_category.csv` and `attachment_category.csv`.

## Resolved decisions (2026-09-26)
All open questions accepted as recommended: Q1 = B (attachment_category table, no FK, Custom... stays); Q2 `''` → NULL; Q3 part-numbering out of scope; Q4 no cross-exe refresh; Q5 no rename cascade.
Also: add `part_category.sql` (before `part.sql`) and `attachment_category.sql` (before `part_attachment.sql`) to the `.github/workflows/test.yml` load list (#19).
