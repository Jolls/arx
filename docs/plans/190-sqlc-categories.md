# #190 — sqlc: convert part categories (`arx_go/categories.go`)

Next domain after the contacts pilot (PR #226, commit `9f24fe2`). #190 stays open. Only
`arx_go/categories.go`'s SQL moves (tables `part_category`, `part`).

## Resolved decisions (user, 2026-09-27) — these OVERRIDE anything below that conflicts
1. **Package is `internal/parts`, not `internal/categories`** (matches #220, which will extend it). Everywhere below:
   - `internal/categories/categories.sql` → `internal/parts/parts.sql`; `internal/categories/categories.go` → `internal/parts/parts.go`; `sqlc.yaml` entry `- internal/parts/parts.sql`; generated file `internal/dbq/parts.sql.go`.
   - Package doc: `// Package parts is the parts domain (#190, #220): typed access to the part tables via the sqlc-generated queries in parts.sql. So far only part categories are converted.`
   - Types/methods: `parts.Category`, `parts.Service`, `parts.New(db dbq.DBTX)`, methods `ListCategories`, `CategoryUsage`, `SaveCategories` (names carry "Category" because the package will grow).
   - Handler helper in `arx_go/categories.go`: `func (h *Handler) parts() *parts.Service { return parts.New(handlerDB{h}) }`; save uses `parts.New(tx).SaveCategories(...)`; `SettingsCategoriesSave` uses `h.parts().CategoryUsage(...)`.
   - Docs (step 6): list `parts` (not `categories`) in CLAUDE.md package lists; "(done: contacts, part categories)"; architecture review → "part categories (`internal/parts`)".
   - PR references #220 as partial ("Part of #220"), does not close it.
2. **Category type:** own flat `parts.Category` + two converters in `arx_go/categories.go` (as planned); nothing moves out of `arx_go/models`.
3. **Red test — list-based lint:** new file `arx_go/sqlc_converted_lint_test.go`, `TestSQLCConvertedFilesHaveNoRawSQL`. Table `convertedFiles := []string{"contacts.go", "categories.go"}` (comment: append each file as its domain converts, #190). For each, read `arx_go/<file>` and fail (`t.Errorf` naming file + line number + pattern) on any line matching regexp `Table\(\)|\bh\.(queryContext|queryRowContext|execContext)\(|\.(QueryContext|QueryRowContext|ExecContext)\(`. `h.beginTx` stays allowed. Red today: categories.go lines 32/35/54/55/87/94/115/130. contacts.go already passes. Write it before step 5; confirm it fails only on categories.go.

## Decisions
- (superseded by Resolved decision 1) Package `internal/categories`: queries in `internal/categories/categories.sql`, service in `internal/categories/categories.go`.
- The service has its own flat `categories.Category` type. `models.Category` (with embedded `CategoryTabs`, `DefaultCategories`, `TabsForCategory`) stays in `arx_go/models`; the handler converts between the two. `internal/` never imports `arx_go/...`.
- Handler methods `fetchPartCategories` and `savePartCategories` keep their signatures (`[]models.Category`), because the integration tests' `withRestoredPartCategories` calls them. `partCategoryUsage` is deleted; `SettingsCategoriesSave` calls the service directly.
- The save stays one transaction: the handler opens it with `h.beginTx`, passes the `*txLogger` to `categories.New(tx)`, and commits. The usage check stays outside the tx, before it, as today.
- **No other caller moves.** Everything else reads the cached `h.st().partCategories` (`parts.go`, `build.go`, `rfq_bom.go`, `settings.go:236`), not the DB. `loadPartCategories` callers (`main.go:52`, `settings.go:622`, `integration_test.go:141`) are unchanged.
- **No `cfg.*Table()` helper is deleted.** `PartCategoryTable()` is still used by the backup table list (`settings.go:732`, #224) and the integration tests. `PartsTable()` has many callers.
- Out of scope: attachment categories (`loadAttachmentCategories`/`saveAttachmentCategories` in `settings.go`, table `attachment_category`). They belong to #220/#224.
- Behavior is unchanged. No schema change, no migration, no seed change.

## Steps

### 1. `internal/categories/categories.sql` (new)
```sql
-- name: ListPartCategories :many
SELECT code, label, is_purchased, is_bom_visible, is_orders_visible, is_pricing_visible,
       is_mfg_parts_visible, is_suppliers_visible, is_inventory_visible
FROM part_category
ORDER BY sort_order, code;

-- name: CountPartsByCategory :many
SELECT COALESCE(category, '') AS category, COUNT(*) AS part_count
FROM part
WHERE category IS NOT NULL
GROUP BY category;

-- name: ListPartCategoryCodes :many
SELECT code FROM part_category;

-- name: UpsertPartCategory :exec
INSERT INTO part_category (code, label, is_purchased, is_bom_visible, is_orders_visible, is_pricing_visible,
                           is_mfg_parts_visible, is_suppliers_visible, is_inventory_visible, sort_order)
VALUES (sqlc.arg(code), sqlc.arg(label), sqlc.arg(is_purchased), sqlc.arg(is_bom_visible),
        sqlc.arg(is_orders_visible), sqlc.arg(is_pricing_visible), sqlc.arg(is_mfg_parts_visible),
        sqlc.arg(is_suppliers_visible), sqlc.arg(is_inventory_visible), sqlc.arg(sort_order))
ON CONFLICT (code) DO UPDATE SET label=EXCLUDED.label, is_purchased=EXCLUDED.is_purchased,
  is_bom_visible=EXCLUDED.is_bom_visible, is_orders_visible=EXCLUDED.is_orders_visible,
  is_pricing_visible=EXCLUDED.is_pricing_visible, is_mfg_parts_visible=EXCLUDED.is_mfg_parts_visible,
  is_suppliers_visible=EXCLUDED.is_suppliers_visible, is_inventory_visible=EXCLUDED.is_inventory_visible,
  sort_order=EXCLUDED.sort_order, updated_at=now();

-- name: DeletePartCategory :exec
DELETE FROM part_category WHERE code = $1;
```

### 2. `sqlc.yaml`
Add `- internal/categories/categories.sql` under `queries:`, after the contacts entry.

### 3. Generate
From the repo root, run `sqlc generate` (`~/go/bin/sqlc.exe`, v1.31.1). This creates `internal/dbq/categories.sql.go` and may touch `internal/dbq/models.go`. Commit whatever it writes. Never hand-edit `internal/dbq`. Use the generated field names (e.g. `IsBomVisible`, `PartCount int64`, `SortOrder int`) in step 4.

### 4. `internal/categories/categories.go` (new)
Mirror `internal/contacts/contacts.go`:
- Package doc: `// Package categories is the part-categories domain (#190, #194): typed access to the part_category table via the sqlc-generated queries in categories.sql.`
- `type Category struct { Code, Label string; Purchased, BOM, Orders, Pricing, MfgParts, Suppliers, Inventory bool }`
- `type Service struct{ q *dbq.Queries }`; `func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db)} }`
- `List(ctx) ([]Category, error)`: runs `ListPartCategories`, maps each row, returns `make([]Category, 0, len(rows))` filled, so zero rows gives a non-nil empty slice.
- `Usage(ctx) (map[string]int, error)`: runs `CountPartsByCategory` and returns `map[code]int(PartCount)`. It returns a non-nil empty map when there are no rows.
- `Save(ctx, cats []Category) error`. Doc comment: upserts cats in order (`sort_order` = index) and deletes codes not in cats. The caller runs it on a transaction; `FK_part_category` rejects deleting a code a part still uses. The body copies today's order: `ListPartCategoryCodes`, then `UpsertPartCategory` per cat with `SortOrder: i`, then `DeletePartCategory` for each existing code not in cats. Return the first error.

### 5. `arx_go/categories.go`
- Import `"arx/internal/categories"`.
- Add `func (h *Handler) categories() *categories.Service { return categories.New(handlerDB{h}) }`, the same as `contacts()`.
- Add two unexported converters: `modelCategory(c categories.Category) models.Category` and `serviceCategory(c models.Category) categories.Category`. They map the flat bools to and from the `CategoryTabs` fields.
- `fetchPartCategories`: keep the signature and doc comment. The body calls `h.categories().List(ctx)` and returns `make([]models.Category, 0, len(cs))` filled via `modelCategory`, so it stays non-nil when empty.
- `partCategoryUsage`: delete it and its comment.
- `savePartCategories`: keep the signature and doc comment. The body does `tx, err := h.beginTx(ctx)` → `defer tx.Rollback()` → convert cats with `serviceCategory` → `categories.New(tx).Save(ctx, svcCats)` → `return tx.Commit()`. Remove the `tbl` variable.
- `SettingsCategoriesSave`: replace `h.partCategoryUsage(r.Context())` with `h.categories().Usage(r.Context())`. Nothing else changes.
- `loadPartCategories`, `categoriesInUse`, `applyCategoryTabs`: unchanged. `fmt` stays imported because `categoriesInUse` uses it.
- Verify: `grep -nE 'Table\(\)|queryContext|ExecContext|QueryContext' arx_go/categories.go` returns nothing.

### 6. Docs
- `CLAUDE.md` line 21: `... / dbq / contacts` → `... / dbq / contacts / categories`.
- `CLAUDE.md` Key facts: `internal: package config/db/urlutil/folderpick/migrate/dbq/contacts.` → `.../dbq/contacts/categories.`
- `CLAUDE.md` Data access: `(done: contacts)` → `(done: contacts, categories)`.
- `SQL/SCHEMA.md:62`: `Converted so far: contacts.` → `Converted so far: contacts, part categories.`
- `docs/architecture-review-2026-09-24.md:130`: `and contacts is converted (\`internal/contacts\`)` → `and contacts (\`internal/contacts\`) and part categories (\`internal/categories\`) are converted`.
- The manager handles CHANGELOG. There are no RELEASE_NOTES (not user-facing).

### 7. Verify
`sqlc diff` is clean. `go build ./...`, `go vet ./...` and `go test ./...` pass from the repo root. `ARX_TEST_FROM_CONFIG=1 go test -tags integration ./arx_go/...` passes. The step-5 grep is empty.

## Test plan

### 1. Coverage audit (existing)
- `arx_go/categories_test.go`: `TestSettingsCategoriesSave_NoDatabaseRedirects`, `TestCategoriesInUse`.
- `arx_go/categories_integration_test.go`: `TestIntegration_SettingsCategoriesSave_PersistsRows` (insert, trim/uppercase, flags, order incl. DWG-before-DOC, cold reload), `_DropsEmptyAndDuplicateCodeRows`, `_RejectsRemovingInUse` (message prefix `BUY (`, no write, cache unchanged), `_RejectsEmpty`, `TestIntegration_PartCreate_SettingsAddedCategorySucceeds`, `TestIntegration_PartCategories_SeedMatchesDefaults` (List order and mapping against the seed). These consume the cache but don't exercise the converted SQL: `TestIntegration_PartCreate_EmptyCategoryStoresNull`, `TestIntegration_LoadBuildComponents_NullCategory`. Out of scope (attachment categories): `TestIntegration_SettingsAttachmentCategoriesSave_PersistsOrder`, `TestIntegration_PartAttachments_CategoryOptions`. The helper `withRestoredPartCategories` calls `fetchPartCategories`/`savePartCategories`, so they keep their signatures.
- `arx_go/settings_save_integration_test.go`: `TestIntegration_SettingsSave_TestModeSwap` (reload side effect after a DB swap).
- `arx_go/state_test.go`: `TestRuntimeState_ConcurrentUpdate` (reads the cache).
- `arx_go/models/category_test.go`: `TestTabsForCategory`, `TestDefaultCategories`, `TestShowInventory`, `TestShowBOM`. These are pure model tests and aren't affected.
- Gaps: nothing covers updating an existing row (ON CONFLICT path), deleting an unused code, NULL-category parts in the usage count, the exact count in the in-use message, tx rollback when the delete fails, or the no-DB fallback of `loadPartCategories`.

### 2. Characterization tests (write first, on unchanged code; all must pass)
Integration tests go in `arx_go/categories_integration_test.go` and each calls `liveHandler` + `withRestoredPartCategories`. They use the existing helpers `categoryFormValues`, `withExtraCategory`, `readPersistedCategories`, `postCategoriesSave`, `assert302`, `seedPart`.
- `TestIntegration_SettingsCategoriesSave_ReordersAndUpdatesExisting`: post `DefaultCategories()` reversed, with OPS's label changed to `"Ops Changed"` and `BOM: true`. Assert 302. Assert `readPersistedCategories` equals that exact modified reversed list, and `h.st().partCategories` equals it too. This pins the ON CONFLICT update of label, flags and `sort_order`.
- `TestIntegration_SettingsCategoriesSave_RemovesUnusedCategory`: save `withExtraCategory("ZZR", "Removable", nil)` (302), then save `categoryFormValues(DefaultCategories())` (302). Assert `readPersistedCategories` equals `DefaultCategories()`. This pins the delete path.
- `TestIntegration_SettingsCategoriesSave_IgnoresUncategorizedParts`: `seedPart(t, h, ctx, "")` (NULL category; register its cleanup after `withRestoredPartCategories`). Then save `withExtraCategory("ZZN", "Null Guard", nil)`. Assert 302. This pins `WHERE category IS NOT NULL`: a `""` usage key would block the save with `" (1 parts)"`.
- `TestIntegration_SettingsCategoriesSave_InUseMessageCountsParts`: get `n` from `SELECT COUNT(*) FROM <PartsTable()> WHERE category='BUY'` (via `h.queryRowContext` + `fmt.Sprintf`, the same as the existing tests). Post the defaults minus BUY. Assert status 200 and a body containing `fmt.Sprintf("BUY (%d parts)", n)`. This pins the COUNT → int mapping.
- `TestIntegration_SavePartCategories_RollsBackOnDeleteFailure`: call `h.savePartCategories(ctx, cats)` directly, where cats = the defaults minus BUY plus `{Code: "ZZF", Label: "Rollback Probe"}`. Assert that the error is non-nil and that `strings.ToLower(err.Error())` contains `fk_part_category`. Assert `readPersistedCategories` equals `DefaultCategories()` (no ZZF, BUY intact). This pins that the upserts and the delete share one tx (it fails if Save runs on `handlerDB` instead of the `*txLogger`).
- Unit test in `arx_go/categories_test.go`: `TestLoadPartCategories_NoDatabaseUsesDefaults`. `h := New(nil, &arxbase.Config{}, templatesFS, nil)`, then `h.update(func(s *runtimeState){ s.partCategories = nil })`, then `h.loadPartCategories(context.Background())`. Assert that `h.st().partCategories` equals `models.DefaultCategories()`. This pins the nil-DB guard (no service call on a nil DB).

No new seed rows are needed: seed parts 3002/3003 are BUY, and `seedPart` creates the NULL-category part.

### 3. Red tests
None. This is a behavior-preserving refactor, and the contacts conversion added no structural/lint test to extend. The compile step (removed `partCategoryUsage`, typed service) and the step-5 grep are the structural check.

### 4. Manual-only
- Settings → Part Categories: add a code, rename a label, reorder rows, toggle flags, Save. Reload Settings and the order and values persist.
- Remove an unused code: it's gone. Remove an in-use code (BUY): error banner `Cannot remove part categories still used by parts: BUY (N parts)...`, nothing saved.
- Save with every code blank: `At least one part category is required.`
- A part's detail page shows or hides subtabs per the edited flags. "Create RFQs" still treats Purchased categories as ordered.
- With `DEBUG_MODE=true`, a save logs the sqlc queries, then `COMMIT` (or `ROLLBACK` on failure).
