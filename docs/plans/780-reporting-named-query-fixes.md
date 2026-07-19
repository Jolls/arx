# Plan: #780 — Reporting/named-query correctness bugs (M26–M28)

Epic #719. Three independent, single-file fixes. No shared code between them —
listed separately, can be implemented/committed independently.

---

## M26 — Failure-mode reports mislabel steps via MAX(res.parameter)

### Summary
`records_failure_modes.go` and `reports.go` (dashboard top-failure-modes)
both group `result` rows by `form_row_id` and label the group with
`MAX(res.parameter)`. Per `SQL/result.sql:3`, `parameter` is a per-record
snapshot taken at record-creation time from the live `form_row` definition —
it can change across records if the step's label was edited on the live form
between record creations. `MAX()` on a VARCHAR picks the alphabetically
greatest snapshot string, not the most recently created one. Result: the
displayed label is arbitrary/stale, and it's purely a display bug — the
`GROUP BY res.form_row_id` (the actual identity) is already correct, so
result rows are never miscounted, only mislabeled.

### Current code

**`arx_go/records_failure_modes.go:63-72`**
```go
rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
    SELECT MAX(res.parameter) AS parameter,
        SUM(CASE WHEN res.pass_fail = %s THEN 1 ELSE 0 END) AS failure_count,
        COUNT(res.pass_fail) AS total_tested
    FROM %s res
    JOIN %s trec ON res.form_record_id = trec.id
    WHERE trec.form_id = @p1 AND trec.is_active = %s AND res.pass_fail IS NOT NULL%s
    GROUP BY res.form_row_id
    ORDER BY failure_count DESC, parameter ASC`,
    h.dialect.BoolLiteral(false), h.cfg.ResultsTable(), h.cfg.RecordsTable(), h.dialect.BoolLiteral(true), dateClause), args...)
```

**`arx_go/reports.go:172-184`** (`dashboardTopFailureModes`) — same pattern,
confirmed same bug:
```go
rows, err := h.queryContext(ctx, fmt.Sprintf(`
    SELECT %sf.id, pn.part_number, MAX(res.parameter) AS parameter,
        SUM(CASE WHEN res.pass_fail = %s THEN 1 ELSE 0 END) AS failure_count
    FROM %s res
    JOIN %s trec ON res.form_record_id = trec.id
    JOIN %s f ON trec.form_id = f.id
    JOIN %s pn ON f.part_number_id = pn.id
    WHERE trec.is_active = %s AND res.pass_fail IS NOT NULL
    GROUP BY f.id, pn.part_number, res.form_row_id
    HAVING SUM(CASE WHEN res.pass_fail = %s THEN 1 ELSE 0 END) > 0
    ORDER BY failure_count DESC`+h.dialect.LimitClause("@p1"),
    h.dialect.TopClause("@p1"), h.dialect.BoolLiteral(false), h.cfg.ResultsTable(), h.cfg.RecordsTable(), h.cfg.FormsTable(), h.cfg.PartsTable(), h.dialect.BoolLiteral(true), h.dialect.BoolLiteral(false)), limit)
```

`SQL/result.sql` has no `created_at`, only `updated_at DATETIME` (nullable,
no default — set by app code on write, not guaranteed populated on every
row historically) and the `id INT IDENTITY` PK, which is monotonically
increasing with insertion order and always populated. Since result rows are
created once per test record and not normally re-inserted, `id` order is a
reliable proxy for "most recently created" — more reliable than `updated_at`,
which could be null on older rows or bumped by unrelated edits (comment/
pass_fail correction) without changing `parameter`.

### Decided fix
Pick the `parameter` from the result row with the greatest `id` per
`form_row_id`, via a correlated subquery (portable across the SQL Server /
future-Postgres dialects already abstracted in this codebase — no
window-function or dialect-specific syntax needed).

**`records_failure_modes.go`** — replace `MAX(res.parameter) AS parameter`
with:
```sql
(SELECT TOP 1 r2.parameter FROM %s r2
 WHERE r2.form_row_id = res.form_row_id
 ORDER BY r2.id DESC) AS parameter,
```
Note `TOP 1` is SQL-Server-specific (matches existing `h.dialect.TopClause`
usage elsewhere in this file's package for limits) — use
`h.dialect.TopClause("1")` in the subquery to stay dialect-agnostic, e.g.:
```go
fmt.Sprintf(`
    SELECT (SELECT %s r2.parameter FROM %s r2
            WHERE r2.form_row_id = res.form_row_id
            ORDER BY r2.id DESC) AS parameter,
        SUM(CASE WHEN res.pass_fail = %s THEN 1 ELSE 0 END) AS failure_count,
        COUNT(res.pass_fail) AS total_tested
    FROM %s res
    JOIN %s trec ON res.form_record_id = trec.id
    WHERE trec.form_id = @p1 AND trec.is_active = %s AND res.pass_fail IS NOT NULL%s
    GROUP BY res.form_row_id
    ORDER BY failure_count DESC, parameter ASC`,
    h.dialect.TopClause("1"), h.cfg.ResultsTable(),
    h.dialect.BoolLiteral(false), h.cfg.ResultsTable(), h.cfg.RecordsTable(),
    h.dialect.BoolLiteral(true), dateClause)
```
(Verify `h.dialect.TopClause` renders as a prefix keyword, e.g. `TOP 1`, not
a suffix `LIMIT` — check `arx_go/dialect*.go` before wiring this in; if it's
suffix-only, use a suffix-based subquery form or a `MAX(id)`-join instead:
`JOIN (SELECT form_row_id, MAX(id) AS latest_id FROM result GROUP BY
form_row_id) latest ON latest.form_row_id = res.form_row_id` then select
`latest_result.parameter` via a second join to `result` on `latest.latest_id`
— slightly more verbose but fully dialect-portable without relying on
TOP/LIMIT semantics at all. Recommend the MAX(id)-join form for portability
since dialect abstraction for TOP is TOP-N list-limiting, not designed for
scalar subqueries.)

**`reports.go` (`dashboardTopFailureModes`)** — same subquery substitution
for `MAX(res.parameter) AS parameter`, keeping the rest of the query
(joins to `trec`/`f`/`pn`, `GROUP BY f.id, pn.part_number, res.form_row_id`,
`HAVING`) unchanged.

### Verification
- Existing tests covering `RecordsFailureModes` / `dashboardTopFailureModes`
  (check `arx_go/*_test.go` for current coverage) should still pass.
- Regression test: seed two result rows for the same `form_row_id` with
  different `parameter` snapshots and different `id` order matching creation
  order; assert the returned label matches the highest-`id` row's parameter,
  not the alphabetically greatest one.

---

## M27 — execQuery silently empties on ≥3-column named queries

### Summary
`arx_go/named_query.go:142-190`, `execQuery`: computes `multiCol := len(cols)
>= 2` but then always does `rows.Scan(&val, &label)` — exactly 2 scan
destinations regardless of actual column count. For a query returning 3+
columns, every `rows.Scan` call errors (`sql: expected N destination
arguments in Scan, not 2`), and the per-row scan error is silently
`continue`d (line 168-170), so the function returns success with an empty
`QueryResult.Rows` and no error. This defeats `SettingsNamedQueryTest`'s
"test before saving" preview — the tester sees "0 rows" instead of an error
or the actual data.

### Design intent (from existing code)
The doc comment directly above `execQuery` (line 141) already states the
intended contract:
> "Column handling matches runNamedQuery: 1 col = value+label, 2+ cols =
> value,label."

I.e., the number of destination fields was always meant to be capped at 2
(`Value`, `Label`) — any extra columns beyond the first two are intentionally
allowed in the query result set and simply ignored for display purposes. The
bug is purely that the `Scan` call was hardcoded to `Scan(&val, &label)`
which requires the **driver's column count**, not the app's destination
count, to be 2. Go's `database/sql` requires `len(scan destinations) ==
len(columns)` — there's no way to always pass exactly 2 destinations to
`Scan` when the underlying row has 3+ columns; the fix must build a
destination slice sized to `len(cols)` and only read the first two entries
back out.

### Decided fix
Scan into a `[]any` slice sized to the actual column count (using
`sql.NullString` for every slot, discarding entries after index 1), so any
number of columns ≥1 is accepted per the documented "extra columns ignored"
intent — no rejection of 3+ column queries, since the comment establishes
that was always meant to work.

**`arx_go/named_query.go` — replace the scan loop (lines ~164-188):**
```go
result := QueryResult{ResultType: resultType}
scanDest := make([]any, len(cols))
vals := make([]sql.NullString, len(cols))
for i := range vals {
    scanDest[i] = &vals[i]
}
for rows.Next() {
    if err := rows.Scan(scanDest...); err != nil {
        continue
    }
    val := vals[0]
    var label sql.NullString
    if multiCol {
        label = vals[1]
    } else {
        label = val
    }
    if !val.Valid || val.String == "" {
        continue
    }
    lbl := label.String
    if !label.Valid || lbl == "" {
        lbl = val.String
    }
    result.Rows = append(result.Rows, QueryRow{Value: val.String, Label: lbl})
    if resultType == "single" {
        break // first row only
    }
}
return result, nil
```
This preserves existing 1-col and 2-col behavior exactly (same `val`/`label`
semantics) and additionally makes 3+-col queries scan successfully, using
only the first two columns for `Value`/`Label` and silently ignoring the
rest — consistent with the pre-existing doc comment's stated contract.

### Verification
- Regression test in `arx_go/named_query_test.go` (check if such a file
  exists; if not, add alongside existing named-query tests): a query
  returning 3 columns (e.g. `SELECT id, name, extra FROM ...`) should
  populate `Rows` using col 1/2 as Value/Label, not return empty.
- Also confirm existing 1-col and 2-col query tests still pass unchanged.

---

## M28 — querySpendByPart fragments a part's spend across rows

### Summary
`arx_go/reports.go:539-565`, `querySpendByPart`, groups by
`(pol.part_number_snapshot, p.title)` — both denormalized/snapshot text
columns — instead of the immutable `part.id`. Per `SQL/po_line.sql:4,14,16`:
`part_number_snapshot` is "denormalized part number at time of order" and
`part_id` is the real FK to `part.id` (nullable — freeform lines with no
catalog part have `part_id IS NULL`). If a part's `part_number` is renamed
after some POs were placed, old PO lines keep their original
`part_number_snapshot` text while new lines (and the `part` row itself) show
the renamed number — so the same part's spend silently splits into multiple
report rows grouped by differing snapshot text, instead of one row totaling
the true spend.

### Current code
**`arx_go/reports.go:539-565`**
```go
func (h *Handler) querySpendByPart(ctx context.Context, rng reportDateRange) ([]spendPartRow, error) {
	where, args := rng.whereClause("po.date_ordered", 1)
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT pol.part_number_snapshot, p.title, COALESCE(SUM(pol.qty * pol.unit_cost), 0) AS total_spend
		FROM %s pol
		JOIN %s po ON pol.po_id = po.id
		LEFT JOIN %s p ON pol.part_id = p.id
		WHERE 1=1%s
		GROUP BY pol.part_number_snapshot, p.title
		ORDER BY total_spend DESC
	`, h.cfg.POLineTable(), h.cfg.POTable(), h.cfg.PartsTable(), where), args...)
	...
	for rows.Next() {
		var partNumber, title sql.NullString
		var total float64
		if err := rows.Scan(&partNumber, &title, &total); err != nil {
			return nil, err
		}
		result = append(result, spendPartRow{PartNumber: partNumber.String, Title: title.String, TotalSpend: total})
	}
	...
}
```

### Decided fix
Group by a single key expression that uses `pol.part_id` when present
(immutable identity — one row per real part, regardless of snapshot-text
drift) and falls back to `pol.part_number_snapshot` only for freeform lines
where `part_id IS NULL` (existing documented behavior in the current
function comment — "Lines with no linked part_id (freeform PO lines) are
grouped by their part_number_snapshot text so their spend stays
accounted for" — preserve this for the null case). Display the *current*
`part.part_number` (not the stale snapshot) when `part_id` is set, falling
back to the snapshot text only when there's no linked part — this also
fixes the secondary staleness bug of displaying a renamed part's old number.

```go
func (h *Handler) querySpendByPart(ctx context.Context, rng reportDateRange) ([]spendPartRow, error) {
	where, args := rng.whereClause("po.date_ordered", 1)
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT COALESCE(p.part_number, pol.part_number_snapshot) AS part_number,
			p.title, COALESCE(SUM(pol.qty * pol.unit_cost), 0) AS total_spend
		FROM %s pol
		JOIN %s po ON pol.po_id = po.id
		LEFT JOIN %s p ON pol.part_id = p.id
		WHERE 1=1%s
		GROUP BY COALESCE(CAST(pol.part_id AS VARCHAR(20)), CONCAT('snap:', pol.part_number_snapshot)),
			p.part_number, p.title, pol.part_number_snapshot
		ORDER BY total_spend DESC
	`, h.cfg.POLineTable(), h.cfg.POTable(), h.cfg.PartsTable(), where), args...)
	...
}
```
Notes on the `GROUP BY` list:
- The leading `COALESCE(CAST(pol.part_id AS VARCHAR(20)), CONCAT('snap:',
  pol.part_number_snapshot))` is the actual dedup key: one bucket per
  `part_id` when present (fixing the fragmentation bug), one bucket per
  distinct snapshot text only for `part_id IS NULL` rows (preserving
  existing freeform-line behavior).
- `p.part_number`, `p.title` are included per SQL Server's requirement that
  every selected non-aggregate column appear in `GROUP BY` — they add no
  further fragmentation because for a fixed `part_id` both are single-valued
  (one row in `part` per id), and for `part_id IS NULL` rows both are always
  NULL.
- `pol.part_number_snapshot` is included for the same SQL-validity reason
  (it's referenced inside the `COALESCE` key but not directly selected, so
  strictly it doesn't need to be in `GROUP BY` for SQL Server since it's
  already grouped via the outer expression — **verify this compiles**; if
  SQL Server rejects the ungrouped column reference, this line is unnecessary
  and can be dropped, or the whole `COALESCE` expression can be repeated
  verbatim in `GROUP BY` instead of being decomposed).

No Go-side change needed beyond the SQL string — `spendPartRow` and the
scan loop (`partNumber, title, total`) stay the same shape.

### Verification
- Regression test: seed two `po_line` rows against the same `part_id` with
  different `part_number_snapshot` values (simulating a rename after the
  first PO); assert `querySpendByPart` returns one row with summed spend,
  not two.
- Confirm freeform lines (`part_id IS NULL`) with differing snapshot text
  still produce separate rows (existing behavior preserved).

---

## Open questions

1. **M26**: Confirm `h.dialect.TopClause` actually renders a prefix `TOP N`
   token suitable for embedding inside a scalar subquery's SELECT list (as
   opposed to being wired only for suffix-style limiting in this codebase's
   existing usages). If it isn't safely reusable in subquery position, use
   the `MAX(id)`-join form described above instead — needs a quick look at
   `arx_go/dialect*.go` before implementing, not guessed here.
2. **M26**: `result.updated_at` exists but is not guaranteed non-null/always
   bumped on creation (no DDL default) — I've assumed `id` (IDENTITY, always
   populated, monotonic) is the reliable "latest" signal instead. Flag if
   there's a reason `updated_at` was intended for this instead (e.g. if
   `parameter` could be corrected retroactively without a new `result` row
   being inserted — I found no evidence of that in the schema, but haven't
   audited every write path that touches `result.parameter`).
3. **M27**: The fix assumes the pre-existing doc comment ("2+ cols =
   value,label") reflects real design intent to always support ≥2 columns,
   just miscoded. If instead 3+-column named queries were never supposed to
   be authored at all (i.e., the doc comment describes an aspiration rather
   than a supported case), the alternative fix is to make `execQuery`
   reject `len(cols) > 2` up front with a clear error ("named query must
   return exactly 1 or 2 columns, got N") surfaced through
   `SettingsNamedQueryTest`. I could not find any caller or test asserting
   3-column behavior either way — recommend confirming with whoever wrote
   the original comment/#729/#724 addenda before choosing between "support
   it" vs. "reject it with a clear error."
4. **M28**: The `GROUP BY` list mixes an expression-based key with the
   underlying raw columns for SQL-validity; I flagged above that
   `pol.part_number_snapshot` in the `GROUP BY` list may be redundant/may
   not compile as written under SQL Server's rules for expressions built
   from an already-grouped source column — needs a build/vet or live-query
   check before shipping, not just a read-through.
