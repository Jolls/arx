# #192 — Audit/event timestamps: UTC `timestamptz`, assigned by the database

Postgres-only (`main`). Do not touch `SQL/azure/`. Applies after `docs/plans/dead-column-cleanup.md`, which drops `logs` (skip `logs.date_logged` everywhere below) and renames `company.SUNotes`→`notes` (so suppliers.go args use `fv(r, "notes")`). Migration self-registration form matches the dead-column plan: `INSERT INTO schema_migrations (version_id, is_applied) SELECT <ts>, TRUE WHERE NOT EXISTS (...)`.

## Open questions (plan assumes the default)
1. `form_record.record_date` is a zoneless `TIMESTAMP` with time of day (`datetime-local` input). Default: leave it as zoneless `TIMESTAMP` (user-typed wall clock).
2. `part.created_date`/`modified_date`, `po.date_printed`, `build_date`/`txn_date` defaults, `price.effective_date` are `DATE` from desktop `time.Now()`. Default: unchanged (DB `CURRENT_DATE` would be the UTC date).
3. Migration reads desktop-clock legacy values as `America/Los_Angeles`; DB-clock values in the migration session's `TimeZone`.
4. Nullable audit columns get `DEFAULT now()` but stay nullable, no backfill.

## Facts this plan relies on (verified)
- `internal/db/dialect.go:174` `postgresDialect.Rewrite` turns `GETDATE()` into `CURRENT_TIMESTAMP` = `now()` (timestamptz fixed at transaction start). **Every existing `GETDATE()` write site is already correct and needs no change.**
- pgx v5: a `timestamp` param is encoded discarding the zone; a `timestamp` value scans as a UTC-labelled wall clock. A `timestamptz` param is encoded as an instant; a `timestamptz` value scans in **`time.Local`**. So display must convert with `.In(userLocation)`.
- A `time.Time` sent for a `date` param is encoded from its own y/m/d, so a date built in the user's zone is safe.
- `triggers.sql:209` (`trg_form_row_history`) writes `changed_at` = `CURRENT_TIMESTAMP` (DB clock). **No change to triggers.sql.**
- Most integration tests call `h.DB()` directly with `@pN` placeholders. New tests below use `h.queryRowContext` / `h.beginTx` (dialect rewrite applied).

## 1. Column classification

| table.column | class | new type | default | NULL | legacy clock |
|---|---|---|---|---|---|
| app_config.updated_at | audit | TIMESTAMPTZ | now() | NOT NULL | DB |
| build.created_at | audit | TIMESTAMPTZ | now() | NOT NULL | desktop |
| company.date_modified | audit | TIMESTAMPTZ | now() | NULL | desktop |
| contact.updated_at | audit | TIMESTAMPTZ | now() | NULL | desktop |
| form_events.event_date | event | TIMESTAMPTZ | now() | NOT NULL | DB |
| form_record.created_at | audit | TIMESTAMPTZ | now() | NULL | DB |
| form_record.updated_at | audit | TIMESTAMPTZ | now() | NULL | DB |
| form_row.created_at | audit | TIMESTAMPTZ | now() | NULL | DB |
| form_row.updated_at | audit | TIMESTAMPTZ | now() | NULL | DB |
| form_row_history.changed_at | event | TIMESTAMPTZ | now() | NOT NULL | DB |
| inventory_transaction.created_at | audit | TIMESTAMPTZ | now() | NOT NULL | desktop |
| lot.created_at | audit | TIMESTAMPTZ | now() | NOT NULL | desktop |
| named_queries.created_at | audit | TIMESTAMPTZ | now() | NULL | desktop |
| named_queries.updated_at | audit | TIMESTAMPTZ | now() | NULL | desktop |
| part.last_rollup_at | event (NULL = never) | TIMESTAMPTZ | **none** | NULL | desktop |
| purchase_order.date_modified | audit | TIMESTAMPTZ | now() | NULL | desktop |
| purchase_order_history.changed_at | event | TIMESTAMPTZ | now() | NOT NULL | desktop |
| record_events.event_date | event | TIMESTAMPTZ | now() | NOT NULL | DB |
| result.updated_at | audit | TIMESTAMPTZ | now() | NULL | DB |
| schema_migrations.tstamp | audit | TIMESTAMPTZ | now() | NULL | DB |
| unit.created_at | audit | TIMESTAMPTZ | now() | NOT NULL | DB |
| users.created_at | audit | TIMESTAMPTZ | now() | NOT NULL | DB |
| users.updated_at | audit | TIMESTAMPTZ | now() | NOT NULL | DB |
| form_record.record_date | **user-typed** datetime | unchanged `TIMESTAMP` | — | — | — |

Already `DATE`, unchanged: `build_date`, `txn_date`, `part.created_date`, `part.modified_date`, `po.date_ordered`, `po.date_requested`, `po.date_closed`, `po.date_printed`, `po_line.date_received`, `price.effective_date`.

## 2. SQL files

**DDL.** For each row above, rewrite the column as `<col> TIMESTAMPTZ <NOT NULL?> DEFAULT now()`; `part.last_rollup_at` → `TIMESTAMPTZ NULL`, no default. Files: `app_config.sql:10`, `build.sql:21`, `company.sql:13`, `contact.sql:21`, `form_events.sql:11`, `form_record.sql:22-23`, `form_row.sql:30-31`, `form_row_history.sql:12`, `inventory_transaction.sql:20`, `lot.sql:19`, `named_queries.sql:20-21`, `part.sql:32`, `purchase_order.sql:54` and `:90`, `record_events.sql:11`, `result.sql:26`, `schema_migrations.sql:12`, `unit.sql:22`, `users.sql:19-20`. (Line numbers pre-date the dead-column edits; locate by column name.) `form_record.sql:11` (`record_date`) unchanged.

**`SQL/postgres/seed_test_data.sql`.** Add `SET LOCAL TimeZone = 'UTC';` on the line after `BEGIN;`. No other seed edits.

**New migration `SQL/postgres/migrations/<YYYYMMDDHHMMSS>_192_timestamptz_audit_columns.sql`** (sorts after dead-column migration; same header/ArxDev guard convention):

```sql
BEGIN;
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
SELECT <YYYYMMDDHHMMSS>, TRUE
WHERE NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version_id = <YYYYMMDDHHMMSS>);
COMMIT;
```
No `schema_version` bump (non-breaking: old binaries' `time.Now()` is accepted by timestamptz).

**`SQL/SCHEMA.md`.** Line 19: "Timestamps: `created_at`, `updated_at` — `TIMESTAMPTZ DEFAULT now()`, assigned by the database (never sent from Go); displayed in the viewing user's zone (#192). User-typed dates are `DATE`; `form_record.record_date` is a user-typed zoneless `TIMESTAMP`." Example block (32-33): `TIMESTAMPTZ DEFAULT now()`. `users` row: replace "stamped by `GETDATE()` which is UTC on Azure SQL" with "a `timestamptz` assigned by the database (#192)". `schema_migrations` row: `tstamp` is `TIMESTAMPTZ`.

**`SQL/postgres/README.md`.** Translation table `DATETIME` row → "`TIMESTAMPTZ DEFAULT now()` for audit/event columns (#192); `TIMESTAMP` only for `form_record.record_date`". `DEFAULT GETDATE()` row → `DEFAULT now()` / `DEFAULT CURRENT_DATE`.

## 3. Go write sites

`GETDATE()` sites unchanged: `auth.go` 452/472/489/506/529, `build.go:445`, `pos.go:2480`, all `records.go`/`records_history.go` sites, `dialect.UpsertAppConfig`.

- **`inventory.go:36-40`**: remove `, created_at` from columns, `, @p11` from VALUES, and the `time.Now(),` arg.
- **`lot.go:46-52`**: columns `part_id, lot_number, lot_description, vendor_lot_number, po_line_id, is_active`; values `@p1..@p6`; remove `time.Now()` arg.
- **`build.go:283-287`**: columns `part_id, output_lot_id, qty, build_date, username, note`; values `@p1..@p6`; remove `time.Now()` arg.
- **`contacts.go:211-223`**: remove `, updated_at` and `,@p15`; arg line `fv(r, "CNNotes"),` without `time.Now()`.
- **`contacts.go:265-276`**: `updated_at=@p15` → `updated_at=GETDATE()`, `WHERE id=@p16` → `WHERE id=@p15`; args `fv(r, "CNNotes"), id,`.
- **`suppliers.go` create (~246-256)**: remove `, date_modified` and `,@p8`; drop the `time.Now()` arg.
- **`suppliers.go` update (~314-326)**: `date_modified=@p8` → `date_modified=GETDATE()`; renumber `bulk_order_delimiter=@p8`, `bulk_order_pn_source=@p9`, `WHERE id=@p10`; remove `time.Now()` arg.
- **`parts.go:1409-1427`** (rollup): delete `now := time.Now()`; SQL `UPDATE %s SET last_rollup_cost=@p1, last_rollup_at=GETDATE() WHERE id=@p2`, args `result.cost, partID`; comment "…in one transaction, so `now()` gives every assembly the same timestamp."
- **`pos.go:566-613`** (PO create): delete `now := time.Now()`; `insertPO` column list drop `date_modified,` and last placeholder `,@p39`; args `now, 0.0,` → `0.0,`; history INSERT remove `, changed_at` and `, @p4`, args `newID, newStatus, h.actorName(r)`.
- **`pos.go:823-841`** (PO update): `date_modified=@p34` → `date_modified=GETDATE()`; then `total_cost=@p34`, `supplier_contact_id=@p36`, `receiver_contact_id=@p37`, `WHERE number=@p35`; args end `totalCost, num,`. (Verify renumbering against the actual placeholder list.)
- **`pos.go:1527-1544`** (`recordPOStatusChange`): history INSERT drop `, changed_at`, `, @p5`, `time.Now()` arg; UPDATE `SET status=@p1, is_active=@p2, date_modified=GETDATE()` … `WHERE ID=@p3`, args `to, statusIsActive(to), poID`.
- **`pos.go:1759-1762`** (`resetApproval`): drop `, changed_at`, `, @p4`, `time.Now()` arg.
- **`pos.go:1845-1848`**: drop `, changed_at`, `, @p5`, `time.Now()` arg.
- **`pos.go:2551-2660`** (RFQ award; note #191 later moves `now`/`actor` and the award UPDATE — this plan applies first): delete `now := time.Now()`; `insertPO` SELECT `@p2,` → `GETDATE(),`, `WHERE id=@p3` → `WHERE id=@p2`, args `base, poID`; all three history INSERTs drop `, changed_at`, `, @p4`, `now` arg; both UPDATEs `date_modified=@p1 WHERE ID=@p2` → `date_modified=GETDATE() WHERE ID=@p1`, args drop `now`.
- **`rfq_bom.go:351-397`**: keep `now` (fills `date_requested`); drop `date_modified,` and last placeholder `,@p39`; `now, nil, now, 0.0,` → `now, nil, 0.0,`; history INSERT drop `, changed_at`, `, @p3`, `now` arg.
- **`named_query_settings.go:106-133`**: delete `now := time.Now()`; UPDATE `updated_at=GETDATE() WHERE id=@p7`, args end `active, id`; INSERT columns `name, description, sql, params, result_type, is_active`, values `@p1..@p6`, drop `now, now`; response `"updated": time.Now().In(h.userLocation(r)).Format(nqDateFormat)`.

Unchanged: `records.go:1635` (`record_date`), `records.go:2713`, `build.go:389`, `inventory.go:162`, `pos.go:1637` (date defaults), `pos.go:489`, `pos.go:2168`, `parts.go:541`, `parts.go:668`, `pos.go:1109`.

## 4. Display in the user's zone

**`arx_go/handlers.go`**
- Add `func (h *Handler) userLocationCtx(ctx context.Context) *time.Location` with the current body of `userLocation`, reading the user via `u, _ := ctx.Value(ctxUserKey).(*User)`. `userLocation(r)` → `return h.userLocationCtx(r.Context())`.
- Top-level:
  ```go
  // localTime converts an audit timestamp (timestamptz; pgx scans it in time.Local) to the
  // viewing user's zone for display (#192). A nil loc (template tests not going through
  // render) leaves t unchanged.
  func localTime(loc *time.Location, t time.Time) time.Time { if loc == nil { return t }; return t.In(loc) }
  func localTimePtr(loc *time.Location, t *time.Time) *time.Time { if t == nil { return nil }; lt := localTime(loc, *t); return &lt }
  ```
- `coreTemplateFuncs`: add `"localTime": localTime, "localTimePtr": localTimePtr`.
- `render()`: `m["UserLoc"] = h.userLocation(r)`.

**`arx_go/render_records.go`**: `renderRecords()` add `m["UserLoc"] = h.userLocation(r)`; `recordsTemplateFuncs` add `"localTimePtr": localTimePtr`.

**Templates** (all inside `{{define "content"}}`, `$` = root map):
- `contacts/contact_detail.html:27`: `{{formatDate (localTimePtr $.UserLoc .Contact.UpdatedAt)}}`
- `suppliers/supplier_detail.html:28`: `{{formatDate (localTimePtr $.UserLoc .Supplier.DateModified)}}`
- `pos/po_detail.html:80`: `{{formatDate (localTimePtr $.UserLoc .PO.DateModified)}}`
- `pos/po_detail.html:354`: `{{formatDateTime (localTime $.UserLoc .ChangedAt)}}`
- `parts/part_bom_edit.html:69`: `{{(localTimePtr $.UserLoc .Part.LastRollupAt).Format "2006-01-02 15:04"}}`
- `{{(localTime $.UserLoc .CreatedAt).Format "2006-01-02"}}`: `parts/all_lots.html:38`, `parts/part_detail.html:239` and `:268`, `parts/part_lots.html:32`, `parts/part_units.html:37`
- `parts/part_lot_trace.html:30`: `{{(localTime $.UserLoc .Lot.CreatedAt).Format "2006-01-02"}}`
- `parts/part_unit_trace.html:29`: `{{(localTime $.UserLoc .Unit.CreatedAt).Format "2006-01-02"}}`
- `records/records_show.html`: line 236 `{{formatDateTime (localTimePtr $.UserLoc .Result.UpdatedAt)}}`; lines 262-263 same for `.Step.StepCreatedAt` / `.Step.StepUpdatedAt`; line 337 same for `.EventDate`.

(Line numbers approximate; verify each field's actual expression and pointer vs value type before editing.)

**Go-side formatting**
- `named_query_settings.go:41-60` (`loadNamedQueriesFull`): `loc := h.userLocationCtx(ctx)` before the loop; `q.Updated = updated.Time.In(loc).Format(nqDateFormat)`.
- `reports.go:350` (`dashboardRecentActivity`): `loc := h.userLocationCtx(ctx)` before the PO loop; PO branch `local := changedAt.In(loc)`, `When: formatDate(&local)`; keep `Timestamp: changedAt`. Parts branch unchanged.
- `reports.go` lines 767 and 786 (`ReportsCycleTime` + export): `resolveSpendDateRange(r.URL.Query(), time.Now().In(h.userLocation(r)))`. In `resolveSpendDateRange`'s `custom` branch, both `time.Parse(spendDateLayout, …)` → `time.ParseInLocation(spendDateLayout, …, now.Location())`.
- `records.go:572-575` and `:662-665`: comments "stamped … with GETDATE() = UTC on Azure SQL" → "a timestamptz assigned by the database (#192)". No code change.

No change: `ageDays` (`reports.go:75`), `sql.NullTime`/`time.Time` scans, settings backup CSV (now shows zone offset — accepted).

## 5. `CHANGELOG.md` (single batch entry)
`### Changed`: "- Audit timestamps (created/updated/changed/event times) are now UTC `timestamptz` values assigned by the database instead of the desktop clock, and are shown in each user's timezone ([#192](https://github.com/Jolls/arx/issues/192))"

## Test plan

### 1) Coverage audit
- Unit: `templates_parse_test.go` parse tests (bad func names); `TestContactDetailTemplateRenders` (nil `UserLoc` safe); `reports_test.go` `TestResolveSpendDateRange` / `_ThisQuarterBoundary`.
- Integration: `TestIntegration_UpdatedAtSentinel`, `TestIntegration_PartRollupCostHandler`, `TestIntegration_FormDefHistory_ReturnsPreChangeSnapshot`, `TestIntegration_DashboardStaleWIPRecords` / `PendingApprovalPOs`, `TestIntegration_POStatusTransition_*`, `POApprovalAction_FullWorkflow`, `RFQConvert_*`, `TestIntegration_LotsForPart`, `PartStockAdjust`, contacts/suppliers `*Update`/`*New`, `TestIntegration_PostRoutesSmoke`.
- Gaps: nothing asserts column types, who assigns the timestamp, or the display zone.
- Required edit: `TestIntegration_UpdatedAtSentinel` (integration_test.go ~864, 879, 890) compare `got.Time.UTC().Format(...)`.

### 2) Characterization tests (pass now and after)
- `templates_parse_test.go` `TestAllLotsTemplateRenders_UTCLocation`: parse `shared/layout.html` + `shared/partials.html` + `parts/all_lots.html` with `coreTemplateFuncs()`; data `coreLayoutFakeData` + `Lots: []LotRow{{ID:1, PartID:1, LotNumber:"L-192", CreatedAt: time.Date(2026,1,2,3,0,0,0,time.UTC), IsActive:true}}`, `UserLoc: time.UTC`, `ActiveTab: "parts"`; assert `<td>2026-01-02</td>`. (Before the change the template ignores `UserLoc`; still passes.)
- `integration_test.go` `TestIntegration_RecordDateStaysWallClock`: `h.queryRowContext` `SELECT record_date FROM form_record WHERE id=@p1` (7001) into `time.Time`; `.Format("2006-01-02 15:04") == "2026-06-01 00:00"`.

### 3) Red tests
- `integration_test.go` `TestIntegration_AuditColumnsAreTimestamptz` (skip unless `h.dia().Name() == "postgres"`): per §1 (table, column), `SELECT data_type, COALESCE(column_default,''), is_nullable FROM information_schema.columns WHERE table_schema = current_schema() AND table_name=@p1 AND column_name=@p2`; assert `timestamp with time zone`; default contains `now()` except `part.last_rollup_at` (empty); nullability matches; `form_record.record_date` is `timestamp without time zone`.
- `integration_test.go` `TestIntegration_AuditTimestampsAreDBAssigned` (same skip). Each case in its own `h.beginTx`, `SELECT now()` into `dbNow`, then `Rollback`:
  - (a) `h.createLot(ctx, tx, 3007, lotCreateArgs{Description:"itest-192"}, nil)` → lot `created_at` `.Equal(dbNow)`.
  - (b) `h.recordInventoryTxn(<request ctx with user tz America/Los_Angeles>, tx, 3007, "adjustment", 1, time.Now(), "itest-192", "", nil, nil, nil)` → newest row `created_at` `.Equal(dbNow)`.
  - (c) `h.recordPOStatusChange(req, tx, 5002, "open", "sent")` → newest history `changed_at` and PO 5002 `date_modified` `.Equal(dbNow)`.
  - (Match actual function signatures when writing.)
- `templates_parse_test.go` `TestAllLotsTemplateRenders_UserZone`: as characterization but `UserLoc` = `America/Los_Angeles`; assert `<td>2026-01-01</td>` present, `2026-01-02` absent.
- `reports_test.go` `TestResolveSpendDateRange_CustomUsesNowLocation`: `now := time.Date(2026,7,10,15,0,0,0,LA)`, `range=custom&from=2026-02-01&to=2026-02-28` → `got.From.Equal(time.Date(2026,2,1,0,0,0,0,LA))`.

### 4) Manual-only
- Run migration on ArxDev (psql), then reseed. `\d lot`, `\d purchase_order_history`, `\d users` show `timestamp with time zone` default `now()`.
- Settings → My Preferences timezone Eastern then UTC; these shift: PO history times + Last Modified, part lots/units Created, BOM Last Rollup, record audit log + result updated, supplier/contact Last Modified, Named Queries Updated, dashboard PO activity dates.
- Create a PO, lot, build and inventory adjustment; timestamps show current time in your zone.

## Out of scope
- `CAST(GETDATE() AS DATE)` for `date_closed` (`pos.go:1539`) and `date_ordered` (`pos.go:2578`) uses the DB session zone.
- `DATEADD`/`DATEDIFF` in `reports.go` (269, 491, 619-620, 737) and `records_filters.go:111` are T-SQL and fail on Postgres (pre-existing).

## Resolved decisions (2026-09-26)
All open questions accepted as the defaults stated at the top: `record_date` stays zoneless TIMESTAMP; DATE columns unchanged; legacy desktop-clock values read as America/Los_Angeles; nullable audit columns not backfilled.
