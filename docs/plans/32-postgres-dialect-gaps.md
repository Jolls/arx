# #32 — Postgres dialect gaps (postgres-integration CI green → required)

## Context
- Wrappers `h.queryContext` / `h.queryRowContext` / `h.execContext` / `h.beginTx` txLogger run `postgresDialect.Rewrite` (internal/db/dialect.go:174): only `@pN`→`$N` and `GETDATE()`→`CURRENT_TIMESTAMP`. Raw `h.DB().X` skips it.
- `OUTPUT INSERTED`, `ISNULL`, `TOP`, `DATEADD`, `DATEDIFF`, `CONVERT`, `TRY_CAST`, 0/1 into BOOLEAN are never rewritten — fix at source.
- **Convention (eases #29):** tests route raw `h.DB().X` → wrappers; keep `@pN` everywhere (#29 does one `@pN`→`$N` sweep + deletes Rewrite); all other T-SQL → literal Postgres (`RETURNING id`, `LIMIT 1`, `COALESCE`, `TRUE/FALSE`, date arithmetic). No new `dia()`/`InsertReturningID` calls; existing one at integration_test.go:5078 stays.
- records.go UPDLOCK is #33 (lands first in this batch).

## Step 1 — App code

| File:line | Current | Replacement |
|---|---|---|
| api.go:171 | `SELECT TOP 1 supplier_pn, min_increment … ORDER BY preference ASC` | drop `TOP 1`, append `LIMIT 1` |
| api.go:182 | `SELECT TOP 1 price_ea … ORDER BY pack_size ASC` | drop `TOP 1`, append `LIMIT 1` |
| reports.go:269 | `AND trec.created_at <= DATEADD(day, -@p2, GETDATE())` | `AND trec.created_at <= CURRENT_TIMESTAMP - make_interval(days => @p2)` |
| reports.go:496 | `" AND %s < DATEADD(day, 1, @p%d)"` | `" AND %s < (CAST(@p%d AS DATE) + 1)"` |
| reports.go:428 (comment) | `DATEADD(day, 1, ...)` | `CAST(... AS DATE) + 1` |
| reports.go:624 | `pol.date_received <= DATEADD(day, pol.lead_time_days, po.date_ordered)` | `pol.date_received <= po.date_ordered + pol.lead_time_days` |
| reports.go:625 | `AVG(CAST(DATEDIFF(day, DATEADD(day, pol.lead_time_days, po.date_ordered), pol.date_received) AS FLOAT))` | `AVG(CAST(pol.date_received - (po.date_ordered + pol.lead_time_days) AS DOUBLE PRECISION))` |
| reports.go:742 | `AVG(CAST(DATEDIFF(hour, entered_at, exited_at) AS FLOAT) / 24.0)` | `AVG(CAST(EXTRACT(EPOCH FROM (date_trunc('hour', exited_at) - date_trunc('hour', entered_at))) AS DOUBLE PRECISION) / 3600.0 / 24.0)` |
| records_filters.go:111 | `" AND record_date < DATEADD(day, 1, @p%d)"` | `" AND record_date < (CAST(@p%d AS DATE) + 1)"` |

Semantics: `date + int` → date, `date - date` → integer days (= DATEADD/DATEDIFF day on DATE). DATEDIFF(hour) counts hour boundaries → truncate both to hour then subtract. `CAST($N AS DATE) + 1` = same exclusive upper bound.

Unit test update — records_filters_test.go:86 `want`: `" AND record_type = @p2 AND record_date >= @p3 AND record_date < (CAST(@p4 AS DATE) + 1)"`.

## Step 2 — Test code: wrapper routing
In integration_test.go (248), records_lock_integration_test.go (24), mfg_parts_integration_test.go (9), contacts_integration_test.go (3), replace_all:
- `h.DB().QueryRowContext(` → `h.queryRowContext(`
- `h.DB().ExecContext(` → `h.execContext(`
- `h.DB().QueryContext(` → `h.queryContext(`
Then `grep -n "h.DB()" arx_go/*_test.go` = 0.

## Step 3 — Test code: T-SQL → Postgres

### 3a. `OUTPUT INSERTED.<col>` → remove; append `RETURNING <col>` after VALUES `)`. BOOLEAN literals 0/1 → FALSE/TRUE in the same statement.
BOOLEAN: part.is_active; form.is_locked/is_active; form_record.is_locked/is_approved/is_active; form_row.archived; lot.is_active; company.is_active; result.pass_fail. Integers unchanged: form_row.type, form_record.form_revision, bom.qty/line_number, literal form_id 6001.

| File:line | New VALUES tail |
|---|---|
| integration_test.go:276, :281 | `INSERT INTO %s (part_number) VALUES (@p1) RETURNING id` |
| integration_test.go:444, 556, 652, 790, 1722, 4116, 4260, 4531, 5063; records_lock_integration_test.go:33 | `VALUES (@p1, 'A', 'Integration Test Part', 'U', TRUE) RETURNING id` |
| integration_test.go:453, 565, 661, 803, 1731, 4125, 4269, 4538, 6485; records_lock_integration_test.go:40, 352 | `VALUES (@p1, '', FALSE, TRUE) RETURNING id` |
| integration_test.go:461 | `INSERT INTO %s (form_id, type) VALUES (@p1, 0) RETURNING id` |
| integration_test.go:491 | `VALUES (@p1, @p2, '1', TRUE) RETURNING id` |
| integration_test.go:682 | `VALUES (@p1, 0, 'Screenshot/File Panel Photo', 'attach') RETURNING id` |
| integration_test.go:697 | `VALUES (@p1, 'ITEST-587', '', '', @p2, '', FALSE, TRUE) RETURNING id` |
| integration_test.go:817 | `VALUES (@p1, 'ITEST-587-LOCKED', '', '', TRUE, TRUE) RETURNING id` |
| integration_test.go:1737 | `(form_id, type, parameter) VALUES (@p1, 0, 'Audit Seed') RETURNING id` |
| integration_test.go:2087 | `VALUES (6001, @p1, @p2, FALSE, TRUE) RETURNING id` |
| integration_test.go:2189 | `VALUES (6001, @p1, @p2, 'ASM-1002', 'Sub-Assembly', '', '', FALSE, TRUE) RETURNING id` |
| integration_test.go:2253, 2470 | `VALUES (6001, @p1, @p2, 'ASM-1003', 'Widget Deluxe Assembly', '', '', FALSE, TRUE) RETURNING id` |
| integration_test.go:2360 | `VALUES (6001, @p1, @p2, 'ASM-1003', 'Widget Deluxe Assembly', '', '', FALSE, TRUE, @p3) RETURNING id` |
| integration_test.go:2683 | `VALUES (6001, 3013, @p1, 'ASM-1003', 'Widget Deluxe Assembly', '', '', TRUE, TRUE, @p2) RETURNING id` |
| integration_test.go:3172 | `(part_number, description, is_active) VALUES (@p1, @p2, TRUE) RETURNING id` |
| integration_test.go:4132 | `(form_id, type, parameter, spec_max) VALUES (@p1, 0, 'Historical Step', '100') RETURNING id` |
| integration_test.go:4276 / 4282 | `VALUES (@p1, 0, 'Old A') RETURNING id` / `VALUES (@p1, 0, 'Keep B') RETURNING id` |
| integration_test.go:4543 | `(form_id, type, parameter, archived) VALUES (@p1, 0, 'Step', FALSE) RETURNING id` |
| integration_test.go:4570 | `(form_id, record_date, serial_number, is_active, is_locked, test_order, form_revision) VALUES (@p1, '2026-07-01', '1', TRUE, TRUE, @p2, 1) RETURNING id` |
| integration_test.go:4676 | `VALUES (@p1, 'ITEST-INACTIVE', 'itest inactive lot', FALSE) RETURNING id` |
| integration_test.go:4778 | `VALUES (@p1, 'ITEST-LOTUPD', 'orig desc', TRUE) RETURNING id` |
| integration_test.go:5310, 5555, 5622, 5730 | `INSERT INTO %s (name, is_active) VALUES (@p1, TRUE) RETURNING id` |
| integration_test.go:5562, 5632 | `INSERT INTO %s (supplier_id, file_path) VALUES (@p1,@p2) RETURNING supplier_attachment_id` |
| integration_test.go:6498 | `VALUES (@p1, @p2, '', '', FALSE, TRUE) RETURNING id` |
| records_lock_integration_test.go:45 / 357 | `VALUES (@p1, 0, 'Output Voltage') RETURNING id` / `VALUES (@p1, 0, 'Weight') RETURNING id` |
| records_lock_integration_test.go:81 | `VALUES (@p1, '2026-07-01', @p2, '', '', @p3, 'New Release', FALSE, FALSE, TRUE) RETURNING id` |
| records_lock_integration_test.go:371 | `VALUES (@p1, @p2, '2026-07-01', 'NL1', '', '', @p3, 'New Release', FALSE, FALSE, TRUE) RETURNING id` |

Comments: integration_test.go:306 "OUTPUT INSERTED.PNID" → "RETURNING id"; :5074 "via SCOPE_IDENTITY() — OUTPUT INSERTED is blocked…" → "via InsertReturningID".

### 3b. Other boolean literals
| File:line | Current | Replacement |
|---|---|---|
| records_lock_integration_test.go:87 | `VALUES (@p1, @p2, '5.00', 1, 'Output Voltage')` | `…, '5.00', TRUE, 'Output Voltage')` |
| integration_test.go:3280 | `VALUES (@p1, @p2, @p3, 'single', 1)` | `…, 'single', TRUE)` |
| integration_test.go:509 | `CASE WHEN pass_fail = 0` | `CASE WHEN pass_fail = FALSE` |
| integration_test.go:512, 605 | `AND is_active = 1%s` | `AND is_active = TRUE%s` |
| integration_test.go:5524 | `SET is_active=0` | `SET is_active=FALSE` |
Unchanged: bom.qty integration_test.go:291/296; categories_integration_test.go:279.

### 3c. `ISNULL(` → `COALESCE(`
integration_test.go 1837, 1951, 1963, 2021 (×2), 2035, 2100, 2112, 2137, 2147 (×2), 2206 (×2), 2223, 2483, 2496, 2515.

### 3d. `SELECT TOP 1` → drop, append ` LIMIT 1`
integration_test.go 3395, 3485, 3535, 3600, 3613, 3678, 4893, 4950, 5025 (`ORDER BY id DESC LIMIT 1`); records_lock_integration_test.go 136, 217 (after WHERE), 239–240 (`ORDER BY re.id DESC LIMIT 1`).

### 3e. Other
- integration_test.go:4342 `CONVERT(varchar, updated_at, 120)` → `to_char(updated_at, 'YYYY-MM-DD HH24:MI:SS')`.
- integration_test.go:606 `ORDER BY TRY_CAST(serial_number AS INT)` → `ORDER BY CASE WHEN serial_number ~ '^[0-9]+$' THEN CAST(serial_number AS INTEGER) END`.

### 3f. Acceptance grep — 0 hits excluding comments and attachments_test.go:251 `"sqlserver"` case
`grep -nE "OUTPUT +INSERTED|ISNULL\(|SELECT TOP|DATEADD|DATEDIFF|CONVERT\(|TRY_CAST|SCOPE_IDENTITY|WITH \(UPDLOCK|h\.DB\(\)\." arx_go/*.go`

Line numbers above are pre-edit; if earlier edits shift them, match by content.

## Step 4 — CI (.github/workflows/test.yml)
- Delete `continue-on-error: true` from `postgres-integration`.
- Replace the "Non-blocking until…" comment with `# Required check: Postgres integration suite against postgres:17 (#32).`
- Post-merge (human): add `postgres-integration` as a required status check on `main`.

## Test plan
- **Coverage audit:** reports.go:269 → TestIntegration_DashboardStaleWIPRecords (~1582); reports.go:496 → spend/on-time/cycle-time report + CSV tests (~6574–6858); :624–625 → QueryOnTimeDelivery, ReportsOnTimeExportCSV; :742 → QueryPOCycleTime, ReportsCycleTimeExportCSV; records_filters.go:111 → TestIntegration_RecordFilters (~546), TestWhereClauses_AllFiltersNumberedFromStart. APISupplierPN: **no test exists**.
- **Characterization:** add `TestIntegration_APISupplierPN_ReturnsFirstByPreference` (seeded part/supplier pair from seed_test_data.sql; asserts supplier_pn and price_ea non-empty). Existing report tests assert SQL-Server-era seeded values → they characterize the DATEADD/DATEDIFF rewrite.
- **Red:** everything seeding via the helpers above; raw-`@pN` tests in mfg_parts/contacts; RecordFilters, DashboardStaleWIPRecords, QueryOnTimeDelivery, QueryPOCycleTime, OnTime/CycleTime CSV; PO history/approval/receipt (~3395–3678); inventory adjustment (~4893–5025). Full list: latest failing `postgres-integration` run `--- FAIL` lines.
- **Verify-only (fresh DB):** QuerySpendBySupplier, QuerySpendByPart, Spend CSV exports, UpdatedAtSentinel, sourcing/suppliers suites. If still failing → seed/data issue, report.
- **Local:** `go test ./arx_go/...`; fresh postgres:17 `arxdev` container → `bash SQL/postgres/build_schema.sh | psql -v ON_ERROR_STOP=1 "$DSN"` → `go test -tags integration ./arx_go/...`.
- **Manual-only:** Reports page custom date range (To date inclusive); PO line supplier-PN autofill (APISupplierPN); branch-protection required check.

## Resolved decisions
- #33 lands first in this same batch, so the workflow edit ships here.
- APISupplierPN has no test → characterization test included.
- reports.go:742 uses UTC-pinned truncation: `date_trunc('hour', exited_at AT TIME ZONE 'UTC') - date_trunc('hour', entered_at AT TIME ZONE 'UTC')`.
- `h.DB()` accessor left in place for #29.
