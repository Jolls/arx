## Plan: #831 BIT → BOOLEAN call-site treatment

### State of the world (verified against live code, not the issue text)

The bulk of this issue is **already done**. `arxlib/db/dialect.go` already declares and implements `BoolLiteral(v bool) string` (`1`/`0` vs `TRUE`/`FALSE`) and `ToggleBoolExpr(column string) string` (`1 - col` vs `NOT col`) on both dialects, with tests at `arxlib/db/dialect_test.go:147` (`TestBoolLiteral`) and `:168` (`TestToggleBoolExpr`). 84 call sites in `arx_go/*.go` already route through `h.dia().BoolLiteral(...)`; the four user-flag toggles in `auth.go:474,494,514,540` use `ToggleBoolExpr`. All parameter-bound boolean columns (`contacts.go:276`, `named_query_settings.go:112/127`, `auth.go:358`, `lot.go:51`, `pos.go:1456`) bind Go `bool` values already.

The helper-vs-rewrite question in the issue body is **settled by T-SQL semantics**: SQL Server has no boolean data type usable as an expression, so `WHERE is_active` (no `= 1`) is a syntax error and `SET is_active = NOT is_active` is invalid. A bare-boolean rewrite is *not* portable back to SQL Server, which still coexists until #834. The existing `BoolLiteral`/`ToggleBoolExpr` helper approach is the correct and already-committed pattern; this issue only closes the residual gaps.

`docs/plans/tsql-inventory.md` was deleted in commit `ef2e116`; its historical content (`git show 379461c:docs/plans/tsql-inventory.md`) never enumerated the BIT call sites individually, so the survey below is authoritative. Do not re-add the inventory file.

### Residual gaps (the actual work)

Two classes remain, both of which produce a runtime type error on Postgres:

**A. `CAST(CASE ... THEN 1 ELSE 0 END AS BIT)` / `CASE ... THEN 1 ELSE 0 END` result columns scanned into Go `bool`/`sql.NullBool`.** Postgres `BIT` is a bit-string type, not boolean; an `int4` result column cannot be scanned into `*bool` by pgx stdlib. Six sites.

**B. One Go `int` bound as a parameter to a boolean column** (`form_row.archived`).

### Step 1 — add `BoolFromCondition` to the Dialect interface

File: `arxlib/db/dialect.go`.

Add to the `Dialect` interface, next to `BoolLiteral`/`ToggleBoolExpr`, with a doc comment matching the surrounding style:

```go
// BoolFromCondition renders a SELECT-list expression that yields a boolean
// value from a boolean-valued SQL condition, for scanning into a Go bool /
// sql.NullBool. SQL Server has no boolean expression type, so the condition
// must be wrapped in CASE and cast to BIT; Postgres yields the condition's
// boolean value directly.
BoolFromCondition(cond string) string
```

Implementations:

```go
func (sqlServerDialect) BoolFromCondition(cond string) string {
	return "CAST(CASE WHEN " + cond + " THEN 1 ELSE 0 END AS BIT)"
}

func (postgresDialect) BoolFromCondition(cond string) string { return "(" + cond + ")" }
```

The SQL Server form is byte-identical to the existing inline text at `parts.go:174` etc., so SQL Server behavior does not change.

File: `arxlib/db/dialect_test.go` — add `TestBoolFromCondition` modeled on `TestBoolLiteral` (`:147`): assert sqlserver returns `CAST(CASE WHEN a > b THEN 1 ELSE 0 END AS BIT)` and postgres returns `(a > b)`.

### Step 2 — convert `hasOwnBOMExpr` from a const format string to a function

File: `arx_go/parts.go:1095`. Current:

```go
const hasOwnBOMExpr = "CAST(CASE WHEN EXISTS(SELECT 1 FROM %[1]s c WHERE c.parent_part_id = %[2]s) THEN 1 ELSE 0 END AS BIT)"
```

It is spliced into four outer `fmt.Sprintf` format strings and consumes the first two args via explicit indices `%[1]s`/`%[2]s` — which is why the outer arg lists start with `h.cfg.BOMTable(), "p.id"` and why the trailing `%s` verbs resume at index 3. Replace with a function that returns finished SQL:

```go
// hasOwnBOMExpr is the "does this part have its own BOM" EXISTS check shared by
// getPart, rollupCost and aggregateLeafQty — both walk the same bom table shape
// to decide whether to recurse into a sub-assembly or treat a component as a leaf.
func hasOwnBOMExpr(d arxdb.Dialect, bomTable, parentIDCol string) string {
	return d.BoolFromCondition(fmt.Sprintf(
		"EXISTS(SELECT 1 FROM %s c WHERE c.parent_part_id = %s)", bomTable, parentIDCol))
}
```

(`arxdb` is already the import alias for `arx/arxlib/db` in this package — see `handlers.go:73`. If `parts.go` does not import it, add the import.)

Update all four call sites. **Each edit must drop the now-unused leading two args from the outer `Sprintf`,** since the explicit-index verbs are gone:

- `parts.go:34-37` — replace `` `+hasOwnBOMExpr+` `` with `` `+hasOwnBOMExpr(h.dia(), h.cfg.BOMTable(), "p.id")+` ``; outer args become `h.cfg.PartsTable()` only.
- `parts.go:241-250` — same substitution; outer args become `h.cfg.PartsTable()` only.
- `parts.go:715-722` — same substitution; outer args become `h.cfg.PartsTable()` only.
- `parts.go:1116` — `hasBOM := fmt.Sprintf(hasOwnBOMExpr, pl, "pn.id")` becomes `hasBOM := hasOwnBOMExpr(h.dia(), pl, "pn.id")`. The outer `Sprintf` at `:1117` is unchanged (it already passes `hasBOM` through a plain `%s`).

Note the returned string for Postgres contains no `%` verbs, so splicing it into an outer format string is safe.

### Step 3 — convert the four remaining inline `CAST(... AS BIT)` / `CASE ... 1/0` boolean result columns

All four currently emit T-SQL-only text and are scanned into Go booleans.

1. `arx_go/parts.go:174` — the below-min flag, scanned into `p.BelowMin bool` at `:190`. Replace the literal `CAST(CASE WHEN reorder_min IS NOT NULL AND stock_on_hand < reorder_min THEN 1 ELSE 0 END AS BIT),` line with a `%s` verb fed by `h.dia().BoolFromCondition("reorder_min IS NOT NULL AND stock_on_hand < reorder_min")`, inserted into the existing arg list **before** `h.dia().BoolLiteral(true)` (the verbs are positional; the new verb precedes the attachments-subquery `%s` on line 175).
2. `arx_go/parts.go:795` — the child-has-BOM column in `bomItems`, scanned into `childHasBOM sql.NullBool` at `:812`. Replace with `%s` fed by `hasOwnBOMExpr(h.dia(), pl, "pn.id")`. The outer `Sprintf` currently passes `prc, h.dia().BoolLiteral(true), pl, pl, pn` (`:801`) — the second `pl` was the arg for the removed inline `%s` inside the CAST; after the change the arg list is `prc, h.dia().BoolLiteral(true), hasOwnBOMExpr(...), pl, pn`. Verify verb/arg alignment by reading the whole query string before editing.
3. `arx_go/parts.go:2360` — same expression in the BOM CSV export, scanned into `childHasBOM sql.NullBool` at `:2379`. Same treatment; check the `:2366` arg list alignment the same way.
4. `arx_go/sourcing.go:189` and `arx_go/suppliers.go:358` — `CASE WHEN sp.uom_id IS NOT NULL THEN 1 ELSE 0 END AS unit_is_explicit`, scanned into `var unitIsExplicit bool` (`sourcing.go:210`, `suppliers.go:378`). Replace each with `%s AS unit_is_explicit` fed by `h.dia().BoolFromCondition("sp.uom_id IS NOT NULL")`, added at the correct position in each outer arg list.

Do **not** touch these `CASE ... THEN 1 ELSE 0 END` sites — they are integer aggregates scanned into Go `int`, valid on both engines: `records_yield.go:122`, `reports.go:177`, `:184`, `:212`, `records_failure_modes.go:67`, `records.go:573`.

### Step 4 — fix the one int-bound boolean parameter

File: `arx_go/records.go:2289-2307` (`ToggleStepArchived`). Change

```go
archived := 0
if r.FormValue("archived") == "1" {
	archived = 1
}
```

to `archived := r.FormValue("archived") == "1"` (a `bool`). The `UPDATE ... SET archived=@p1` at `:2306` and its arg at `:2307` are otherwise unchanged; `form_row.archived` is `BIT` on SQL Server (`SQL/form_row.sql:26`) and `BOOLEAN` on Postgres, and go-mssqldb binds Go `bool` to `BIT` correctly. Update the comment at `:2271` if it references the integer form.

### Step 5 — verification sweep

Run these to confirm no site was missed (the bool-column set is exactly the `BIT` columns in `SQL/*.sql`: `is_active`, `is_supplier`, `is_manufacturer`, `is_locked`, `is_approved`, `archived`, `is_lot_tracked`, `pass_fail`, `show_users`, `is_admin`, `can_approve_po`, `can_approve_records`):

- `grep -rnE "\b(is_active|is_locked|is_approved|is_supplier|is_manufacturer|is_admin|is_lot_tracked|archived|show_users|pass_fail|can_approve_po|can_approve_records)\s*(=|<>|!=)\s*[01]\b" arx_go --include=*.go` — after the change, the only hits must be inside `//` comments and inside `arx_go/integration_test.go` (see below).
- `grep -rn "AS BIT" arx_go --include=*.go` — must return nothing.
- `go build ./... && go test ./...` from the repo root (`go.work` covers `arx_go` and `arxlib`).

`arx_go/records_filters_test.go:54,64-65` asserts on `" AND is_locked = 0"` etc. — these are correct as-is: the test explicitly constructs `arxdb.NewSQLServerDialect()` and asserts the SQL Server rendering. Leave unchanged.

### Resolved decisions

1. **Helper name**: `BoolFromCondition` (as proposed) — matches the existing `BoolLiteral`/`ToggleBoolExpr` naming family. Confirmed.
2. **`hasOwnBOMExpr` const → func**: proceed with the function conversion as specced in Step 2. Read each of the four call sites' full query string carefully before editing to preserve verb/arg alignment — this is the one step in the plan with silent-corruption risk (wrong alignment compiles fine, just binds the wrong value to the wrong verb), so double-check each edit against the live file rather than trusting the line numbers above.
3. **`integration_test.go` deferral**: confirmed out of scope for #831 — stays SQL-Server-only (`is_active = 1`, `TRY_CAST`, `OUTPUT INSERTED.id`) for now. Add a note to #833 (integration close-out) that this file still needs full Postgres-dialect conversion, not just the boolean literals, before it can run against Postgres.
4. **Scope of #831**: proceed with a real PR covering Steps 1-4 (one new dialect method + six call-site fixes + one parameter type change), not a close-as-substantially-complete. The six remaining sites are genuine Postgres-breaking gaps, worth a tracked fix even though most of the issue turned out to be pre-existing work.

### Explicitly out of scope

`arx_go/integration_test.go` (build tag `integration`) still hardcodes `is_active = 1` / `pass_fail = 0` at `:334`, `:353`, `:356`, `:449`, alongside `OUTPUT INSERTED.id` and `TRY_CAST` in the same statements — deferred to #833 (see Resolved decision 3).

`SQL/postgres/*.sql` needs no change — `SQL/postgres/triggers.sql:118,122,126,233` already uses boolean-native `WHERE f.is_active`. `SQL/named_queries.sql` / `SQL/postgres/named_queries.sql` are #830's scope.

### Critical Files for Implementation
- `arxlib/db/dialect.go`
- `arxlib/db/dialect_test.go`
- `arx_go/parts.go`
- `arx_go/records.go`
- `arx_go/sourcing.go` (and `arx_go/suppliers.go`, same one-line change)
