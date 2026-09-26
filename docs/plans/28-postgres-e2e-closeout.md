# #28 — Postgres integration close-out: seeded end-to-end verification

**Prereqs:** #33 and #32 land first (incl. porting `TestIntegration_RunNamedQuery_StaleSpecNomParamRename`, which still asserts SQL Server "declare the scalar variable" text).

## Findings
1. **Extra bound args — real bug.** `execQuery` (`arx_go/named_query.go`) binds every spec_nom param; pgx fails "expected N arguments, got M" when stored SQL doesn't reference one. Fix: bind only referenced params.
2. **RewriteNamedParams — real bug** (`internal/db/dialect.go`). `@(\w+)` rewrites inside `'...'`, `"..."`, `--`/`/* */` comments, and the tail of `@@name`; `'x@pn'` → `'x$1'` silently. Fix: literal/comment-aware scanner; unrecognized tokens left as-is (stale-param failure preserved). `$tag$` dollar-quoting unsupported — documented invariant.
3. **String args vs INTEGER/DATE.** pgx v5 sends strings as text; server coerces to the inferred param type — expected OK, confirmed by red tests. **`max_subbatch_result` is fragile:** `{record.date}` renders `01/02/2006` (records.go ~L146); `CAST($n AS DATE)` depends on DateStyle, and `CAST('' AS DATE)` errors (SQL Server gave 1900-01-01 → no rows). Fix: `TO_DATE(NULLIF(@record_date, ''), 'MM/DD/YYYY')`.
4. **Mixed-case column folding — no change.** `named_query.go` uses columns positionally; `settings.go` `writeTableCSV` lower-cases `excludeCols` and `rows.Columns()` (L803–820). Characterization only.
5. **Settings test-connection** = `SettingsSave` → reconnect (settings.go ~L500–570); manual smoke only.
6. Stale comment: `SQL/postgres/seed_test_data.sql` L118 names `SQL/named_queries.sql`; actual is `SQL/azure/named_queries.sql`.

## File changes

### 1. `internal/db/dialect.go`
Single-pass scanner skipping `'...'` (with `''` escape), `"..."`, `-- ...` to EOL, `/* ... */`, and whole `@@`-prefixed tokens. Outside those, `@` + `[A-Za-z0-9_]+` is a token.
```go
// scanNamedParams calls fn for each @name token outside literals/comments;
// fn returns the replacement text.
func scanNamedParams(query string, fn func(name string) string) string
func NamedParamRefs(query string) []string // distinct names, first-appearance order
```
`postgresDialect.RewriteNamedParams` uses `scanNamedParams`; names absent from the position map return unchanged `@name`. Delete `namedParamToken` regex. Extend interface doc comment with the invariant (no params in literals/identifiers/comments; `$tag$` unsupported). `sqlServerDialect` stays a no-op.

### 2. `arx_go/named_query.go` `execQuery`
After `isSafeQuery`, `refs := arxdb.NamedParamRefs(sqlText)` as a set; `names` = sorted keys of `params` that are in `refs`. Referenced-but-unsupplied tokens still fail at the driver. Update comment: unreferenced params dropped because Postgres rejects extra positional args (#28).

### 3. `SQL/postgres/seed_test_data.sql`
- `max_subbatch_result`: `CAST(@record_date AS DATE)` → `TO_DATE(NULLIF(@record_date, ''''), ''MM/DD/YYYY'')` (doubled quotes inside the string literal).
- L118 comment → `SQL/azure/named_queries.sql`.
- Note record_date is always `MM/DD/YYYY` from `{record.date}`.

### 4. `SQL/postgres/migrations/<timestamp>_28_max_subbatch_result_to_date.sql` (new)
`UPDATE named_queries SET sql = '<new text>', updated_at = CURRENT_TIMESTAMP WHERE name = 'max_subbatch_result';` — follow folder format (BEGIN/COMMIT, ArxDev DO guard, self-register); `migrations_lint_test.go` must pass.

## Test plan

### Coverage audit
- `internal/db/dialect_test.go` `TestRewriteNamedParams` — simple repeats only.
- `arx_go/integration_test.go` ~L3200–3300 — not-found, stale param, `APINamedQuery` `parts_matching`.
- None: all-seeded-queries on Postgres, extra params, literal `@`, date format / empty date.

### Characterization (pass before change)
- `dialect_test.go`: Postgres rewrite leaves unknown `@name`; repeated tokens → same `$n`.
- Settings backup CSV for `users` excludes `password_hash` with mixed-case `rows.Columns()` (unit test around exclusion helper if isolatable, else integration).

### Red tests
1. `dialect_test.go` `TestRewriteNamedParams_SkipsLiteralsAndComments` (Postgres): `SELECT '@pn', "@pn", x FROM t WHERE a = @pn -- @pn` → `... WHERE a = $1 -- @pn`; `/* @pn */`, `'it''s @pn'`, `@@ROWCOUNT` (with ROWCOUNT in names) unchanged.
2. `dialect_test.go` `TestNamedParamRefs`: distinct, first-appearance order, excludes literals/comments.
3. `integration_test.go` `TestIntegration_SeededNamedQueries_Postgres`, table-driven over all 10 seeded queries with seeded ids/PNs (look up in seed file); assert no error, non-empty where seed allows:
   `fil_category_for_pn(@pn)`, `parts_matching(@pattern=RAW-%)`, `pos_for_pn(@pn)`, `bom_pn_by_item(@pn=<assembly>,@item=1)` (string→INTEGER), `pn_primary_attachment(@pn)`, `form_primary_attachment(@pnid)`, `recent_serial_numbers_for_form(@form_id)`, `max_subbatch_result(@form_row_id,@record_date=12/31/2099)`, `vendor_pns_for_pn(@pn)`, `revision_for_pn(@pn)`.
   Sub-cases (red): extra param `revision_for_pn(@pn=X,@unused=1)` no error; empty date `max_subbatch_result(...,@record_date=)` no error, no rows; DateStyle DMY with `@record_date=01/13/2026` no error (see Open question 2).

### Manual-only
- `bash SQL/postgres/build_schema.sh | psql $DSN`; Settings → Connection: Postgres creds, Save → reconnect succeeds; wrong password → clean error.
- Form spec_nom `query:max_subbatch_result(@form_row_id=..,@record_date={record.date})` auto-fills in record data entry.
- Settings → Backup zip: `users.csv` has no `password_hash`.

### CI
`postgres-integration` already loads seed via `build_schema.sh`; confirm green.

## Resolved decisions
- **Existing Postgres DBs have named_queries copied from Azure (still T-SQL).** Step 4's migration rewrites **all 10 seeded rows**: for each `name` in the seed's named_queries block, `UPDATE named_queries SET sql = <exact text from SQL/postgres/seed_test_data.sql after step 3>, updated_at = CURRENT_TIMESTAMP WHERE name = '<name>';` (also `params` if the seed's differs). Rows with names not in the seed are left untouched (user-authored). Rename file to `<timestamp>_28_named_queries_postgres_text.sql`.
- **DateStyle sub-case:** pinned `*sql.Conn` from the pool (`db.Conn(ctx)`), `SET DateStyle = 'ISO, DMY'` on it, run `max_subbatch_result`'s rewritten SQL on that conn with `@record_date=01/13/2026`; assert no error; `conn.Close()` (defer `RESET DateStyle` first). If the Handler exposes no pool accessor after #32, use the one `liveHandler` builds from.
- `SQL/azure/named_queries.sql` untouched (Azure frozen on main; deleted in #29).
- No new "Test connection" button; save-and-reconnect smoke test is the check.
