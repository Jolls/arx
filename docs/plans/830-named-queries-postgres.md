# Plan: Postgres named_queries seed rows (issue #830)

## Critical finding — param binding is broken for Postgres as currently designed

`arx_go/named_query.go` `execQuery` builds args via:
```go
for k, v := range params {
    args = append(args, sql.Named(k, v))
}
```
This iterates a Go `map[string]string` — **iteration order is randomized per run**. For
SQL Server this is harmless: `go-mssqldb` matches `sql.Named` args to `@name` tokens in the
query text **by name**, not position. For Postgres, `github.com/jackc/pgx/v5/stdlib`
**discards the `Name` field entirely** and passes args to `pgx.Conn.Query` **positionally**,
and the query text is sent to the server verbatim — Postgres has no `@name` placeholder
syntax server-side (that's a `psql`-client-only feature), so any `@word` token reaching the
wire is a syntax error unless `dialect.Rewrite` first turns it into `$N`. Today's `Rewrite`
only matches numeric `@pN` (`pgPlaceholder = regexp.MustCompile(`@p(\d+)`)`); named-query
text uses `@pn`, `@pattern`, `@item`, `@form_id`, `@record_date` etc., none of which match
that regex.

Net effect: multi-param named queries (`bom_pn_by_item`: `@pn`,`@item`;
`max_subbatch_result`: `@form_row_id`,`@record_date`) would bind to the wrong parameter at
random on Postgres, and single-param queries would fail outright (literal `@pn` sent to
Postgres, not translated to `$1`).

### Required code fix

`execQuery` (`arx_go/named_query.go`) must build args deterministically instead of relying
on driver-side name matching:

1. Build the ordered, de-duplicated name list from **the caller-supplied `params` map**
   (sorted, not scanned from `sqlText`) — critically, *not* by scanning the query text for
   `@\w+` tokens. `TestIntegration_RunNamedQuery_StaleSpecNomParamRename` (the regression
   test for a real production incident, `SQL/migrations/migrate_max_subbatch_result_param_rename.sql`)
   depends on a query-text token with **no matching entry in `params`** getting no bound arg
   at all, so the driver errors ("Must declare the scalar variable @x" on SQL Server) instead
   of silently binding an empty string. Deriving names from the query text instead (first
   attempt, caught by this test) breaks that safety net: a missing key just returns `""` from
   the map lookup, which becomes a valid bound value for the stale token.
2. For each name in that (sorted) order, bind `sql.Named(name, params[name])` — keep the
   `sql.Named` wrapper (don't switch to plain positional values): SQL Server's driver matches
   `@name` tokens by `Name`, unaffected by slice order; Postgres's pgx stdlib driver ignores
   `Name` and binds by slice position instead, so wrapping is harmless there and this alone
   fixes the map-iteration nondeterminism for both engines.
3. Add a `Dialect` method that rewrites the query text's `@name` tokens to match that same
   order: SQL Server is a no-op (already confirmed — see Resolved decisions). Postgres's
   version replaces each `@name` occurrence with `$1`, `$2`, ... per the ordered list from
   step 1; a token with no entry in the ordered list (the stale-param case) is left as literal
   `@name` text, which Postgres rejects as a syntax error — preserving the same "stale param
   fails loudly" behavior, just with a different error string than SQL Server's.
4. New `Dialect` method `RewriteNamedParams(query string, orderedNames []string) string`,
   implemented in `arxlib/db/dialect.go` alongside the existing `pgPlaceholder`/`Rewrite`
   machinery.
5. Call this new method from `execQuery` before executing, using the ordered name list from
   step 1, then pass args built in that same order.

**Resolved via integration testing**: the first implementation derived names by scanning
`sqlText` (matching the plan text this replaced) and was caught immediately by
`TestIntegration_RunNamedQuery_StaleSpecNomParamRename` failing (`got nil, want error`)
against live SQL Server. Fixed by deriving names from `params` instead, per steps 1-2 above.

## Scope: SQL text translation for the 9 seeded named queries

Source: `SQL/seed_test_data.sql` lines 113-139 (9 rows, `dbo.named_queries` INSERT block).
Target: new INSERT block for `named_queries` in the Postgres seed script (see "File landing
point" below), translated per row:

1. **`fil_category_for_pn`** — `is_active = 1` → `is_active = TRUE`. No other changes.
2. **`parts_matching`** — `is_active = 1` → `is_active = TRUE`. `LIKE @pattern` unchanged
   (no string concat).
3. **`pos_for_pn`** — `LIKE @pn + '%'` → `LIKE @pn || '%'`.
4. **`bom_pn_by_item`** — no `TRY_CAST`/concat/`is_active`; copy as-is.
5. **`pn_primary_attachment`** — `SELECT TOP 1` → `SELECT` (drop `TOP 1`, Postgres form
   appends `LIMIT 1` at the end); `is_active = 1` → `is_active = TRUE`; final clause becomes
   `... ORDER BY CASE WHEN part.primary_attachment_id > 0 AND part_attachment.id =
   part.primary_attachment_id THEN 0 ELSE 1 END, part_attachment.sort_order ASC LIMIT 1`.
6. **`form_primary_attachment`** — same `TOP 1` → trailing `LIMIT 1` and `is_active = 1` →
   `is_active = TRUE` translation as #5.
7. **`recent_serial_numbers_for_form`** — `SELECT TOP 20 serial_number` → `SELECT
   serial_number`, append `LIMIT 20` at the end; `is_active = 1` → `is_active = TRUE`;
   `TRY_CAST(serial_number AS INT)` → `CASE WHEN serial_number ~ '^[0-9]+$' THEN
   CAST(serial_number AS INTEGER) END` (matches `postgresDialect.TryCastInt`'s exact output
   format in `arxlib/db/dialect.go`, substituting `expr` = `serial_number`).
8. **`max_subbatch_result`** — `TRY_CAST(r.result AS INT)` → `CASE WHEN r.result ~
   '^[0-9]+$' THEN CAST(r.result AS INTEGER) END`; `tr.is_active = 1` → `tr.is_active = TRUE`;
   `CAST(tr.record_date AS DATE) <= CONVERT(DATE, @record_date, 101)` →
   `CAST(tr.record_date AS DATE) <= CAST(@record_date AS DATE)` (ISO-formatted input
   confirmed — see Resolved decisions).
9. **`vendor_pns_for_pn`** — `LIKE '%' + @pn + '%'` → `LIKE '%' || @pn || '%'`.

`created_at`/`updated_at` values: source uses `GETDATE()` and the literal
`'2020-01-01T00:00:00'`. Postgres seed script convention (per
`docs/plans/829-postgres-seed-data.md` rule 6) is `GETDATE()` → `CURRENT_TIMESTAMP`; the
literal timestamp string is carried over unchanged (Postgres parses it natively).

`params`, `result_type`, `description`, `name` columns: copy verbatim from the source rows,
no translation needed (plain strings).

## File landing point

`SQL/postgres/seed_test_data.sql` is created by #829, which explicitly left the
`named_queries` DELETE-with-no-INSERT and a "see #830" comment for this issue to fill in.
Since apply order is #829 before #830 (confirmed), this issue's implementation edits the
already-landed file, replacing that comment with the INSERT block below:

- Keep `DELETE FROM named_queries;`.
- Add the INSERT block with the 9 translated rows, in the same order as the source.
- No `SET IDENTITY_INSERT` (Postgres — per #829 plan, omitted entirely).
- No explicit `id` column values — same column list/behavior as the source
  (`name, description, sql, params, result_type, created_at, updated_at`), letting identity
  assign ids.

## Files to change

1. `arx_go/named_query.go` — `execQuery`: replace the `for k, v := range params { args =
   append(args, sql.Named(k, v)) }` loop with the ordered-token-scan approach (see "Required
   code fix" above). Add the `@\w+` scanning regex as a package-level var near
   `unsafeKeywordRE`.
2. `arxlib/db/dialect.go` — add `RewriteNamedParams(query string, orderedNames []string)
   string` to the `Dialect` interface; implement on both `sqlServerDialect` and
   `postgresDialect`.
3. `arxlib/db/dialect_test.go` — add a test for the new method on both dialects, modeled on
   existing tests (`TestBoolLiteral` pattern).
4. `SQL/postgres/seed_test_data.sql` — add the translated `named_queries` INSERT block (9
   rows) at the section #829 reserved for this issue.
5. `SQL/postgres/README.md` — remove/update the "`named_queries` seed rows are omitted"
   bullet under "Decisions worth a second look", since this issue removes that omission.
   Replace with a short note that rows are now ported, following the same table's translation
   rules (`TRY_CAST` → `postgresDialect.TryCastInt` pattern, `+` concat → `||`,
   `is_active = 1` → `is_active = TRUE` literal).

## Resolved decisions
- **Landing order**: #829 lands before #830 (confirmed by batch apply order) — this plan's
  "File landing point" section assumes and specifies editing #829's already-created file.
- **`RewriteNamedParams` naming/placement**: proceed as a new, separate `Dialect` method
  from the existing `Rewrite` method — `Rewrite` has no access to the params map/ordered
  name list needed here, so folding this into it isn't viable. No naming collision risk
  identified.
- **SQL Server side of the param-rewrite fix (was open question 1)**: confirmed safe as a
  no-op. `arx_go/named_query.go:122`'s own `@p1`-style query already executes through
  `h.queryContext` → `dialect.Rewrite`, and SQL Server's `Rewrite` is already a no-op on that
  path today — i.e. this exact mechanism (named `@x` tokens reaching go-mssqldb unrewritten,
  bound via `sql.Named`) is already the working, in-production pattern. `sqlServerDialect`'s
  `RewriteNamedParams` returns the query unchanged; args stay wrapped in `sql.Named` for both
  engines (harmless for Postgres — pgx binds by position/value, ignoring `Name`).
- **`@record_date` format contract (was open question 2)**: ISO 8601. Every `record_date`
  form input in the app is an HTML `datetime-local`/date field (`records.go:1510`, `:2523`,
  `templates/records/record_edit.html:36`, `record_new.html:43-44`), which browsers always
  submit as ISO 8601 — never `mm/dd/yyyy`. `max_subbatch_result`'s Postgres translation uses a
  bare `CAST(@record_date AS DATE)`, per item 8 above.

### Critical Files for Implementation
- `arx_go/named_query.go`
- `arxlib/db/dialect.go`
- `arxlib/db/dialect_test.go`
- `SQL/postgres/seed_test_data.sql` (created by #829)
- `SQL/postgres/README.md`
