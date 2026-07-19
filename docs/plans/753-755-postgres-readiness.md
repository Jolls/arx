# [753+755] Postgres-readiness (combined PR, epic #719)

Two bundled issues. #753 = bring `SQL/postgres/*` reference DDL to parity with the
canonical SQL Server DDL for the lot/build/inventory/traceability tables. #755 =
route SQL-Server-only outlier SQL through the `arxlib/db` dialect seam.

All `SQL/*.sql` (incl. `SQL/postgres/*`) are reference DDL — never auto-run; keep in
sync only. `dialect.Rewrite` is applied to every query by the `h.*Context` /
`*txLogger` wrappers (`arx_go/handlers.go:128-179`); it only rewrites `GETDATE()` and
`@pN` placeholders — nothing else is textually translated, so any other T-SQL-only
construct must be made portable at the call site or via a Tier-2 dialect helper.

---

## Part A — #753 schema parity (determinate)

Compared each `SQL/postgres/*` file against its canonical `SQL/*.sql` sibling. Findings:
`form_record`, `unit`, `genealogy` postgres files are **already at parity** (they carry
lot_id/build_id/unit_id and the correct FKs) — the issue text's "test_record missing
lot_id/build_id" is stale (table was renamed test_record→form_record; the postgres
port already has them). The real gaps are three columns/FKs plus the README run order.

### A1. `SQL/postgres/inventory_transaction.sql` — add `build_id`
Canonical `SQL/inventory_transaction.sql:32` has `build_id INT NULL`; postgres file lacks it.

- After the `lot_id` line (currently line 18), add:
  ```
  build_id    INTEGER       NULL,                                                        -- FK to build.id (#677): build event that produced this row; NULL otherwise.
  ```
- Update the trailing FK comment (currently lines 24) to also note the build FK, mirroring
  `SQL/inventory_transaction.sql:38-41`: add a line
  ```
  -- FK_inv_txn_build (build_id → build.id) is added in SQL/postgres/build.sql, after build exists.
  ```

### A2. `SQL/postgres/build.sql` — add the deferred `FK_inv_txn_build`
Canonical `SQL/build.sql:26-29` appends this FK after build is created; postgres file
stops at the index (line 25). Append at end of file:
```
-- FK_inv_txn_build (inventory_transaction.build_id → build.id, #677) is added here,
-- after build is created, so inventory_transaction can be created first in run order.
ALTER TABLE inventory_transaction ADD CONSTRAINT FK_inv_txn_build FOREIGN KEY (build_id) REFERENCES build (id);
```

### A3. `SQL/postgres/lot.sql` — add `lot_description`
Canonical `SQL/lot.sql:17` has `lot_description VARCHAR(255) NOT NULL DEFAULT ''`;
postgres file lacks it.

- After the `lot_number` line (currently line 13), add:
  ```
  lot_description    VARCHAR(255)  NOT NULL DEFAULT '',   -- Human-readable provenance: "PO <number>" (purchased), "Build #<id>" (manufactured), "Manual entry" (adjustment tab).
  ```
- (Optional, cosmetic) the postgres file header and `lot_number` comment still say the
  number "defaults to the PO number / build ref"; canonical now says it defaults to the
  lot's own id with provenance in lot_description. Update the two comments to match
  `SQL/lot.sql:1-7,16` if touching the file anyway — no DDL effect.

### A4. `SQL/postgres/README.md` — fix the run order
Current run order (lines 93-99) omits `build`, `lot`, `unit`, `genealogy`, and lists
`form_record` **before** `unit` even though `form_record.sql:35` adds
`FK_form_record_unit` → unit must precede it.

Dependency facts (from the DDL): `inventory_transaction` needs part+po_line; `build`
adds a FK onto `inventory_transaction` (must come after it); `lot` adds FKs onto both
`build` and `inventory_transaction` (after both); `unit` needs part+lot+build (after
lot,build); `form_record` needs part+unit (after unit); `genealogy` needs lot+unit.

Replace the run-order list with:
```
uom, contact, company_attachment, company, part, mfg_part, supplier_part, price,
bom, part_attachment, purchase_order, po_line, inventory_transaction, build, lot,
unit, form, form_row, form_record, result, genealogy, form_row_history, form_events,
record_events, record_event_results, app_config, named_queries, users, logs,
release_notes. Run triggers.sql last.
```
(There is a `SQL/postgres/unit.sql` and `SQL/postgres/genealogy.sql`; the README predates
both. `genealogy` only needs lot+unit so its exact slot is flexible as long as it follows
`unit`.)

---

## Part B — #755 dialect-route outlier SQL

### Determinate fixes

#### B1. `arx_go/pos.go:823` — `IF NOT EXISTS ... INSERT` → portable `INSERT ... SELECT ... WHERE NOT EXISTS`
T-SQL `IF NOT EXISTS (...) INSERT` has no Postgres statement equivalent, but the
`INSERT ... SELECT ... WHERE NOT EXISTS` form runs on both engines. Replace the query
string (lines 823-828) with:
```go
if _, err := h.execContext(r.Context(), fmt.Sprintf(`
    INSERT INTO %s (part_id, supplier_id, supplier_pn)
    SELECT @p1, @p2, @p3
    WHERE NOT EXISTS (
      SELECT 1 FROM %s WHERE part_id=@p1 AND supplier_id=@p2 AND supplier_pn=@p3
    )
`, h.cfg.SupplierPartTable(), h.cfg.SupplierPartTable()),
    partID, supplierID, supplierPN,
); err != nil {
```
Args unchanged. (go-mssqldb reuses @p1..@p3 across both clauses fine; Postgres $1..$3 after
Rewrite likewise.)

#### B2. `arx_go/pos.go:2408` — inline BIT `1` in `InsertSelectReturningID` valuesList
The `SELECT @p1, 'draft', 1, 'not_submitted', NULL, ...` splices `1` into the `is_active`
BOOLEAN column. Replace that literal `1` with `%s` bound to `h.dialect.BoolLiteral(true)`.
The selectBody is already built with `fmt.Sprintf(...)`; add the BoolLiteral as the first
`%s` arg (before the existing `h.cfg.POTable()`), i.e.:
```go
fmt.Sprintf(`SELECT @p1, 'draft', %s, 'not_submitted', NULL,
  ... FROM %s WHERE id=@p3`, h.dialect.BoolLiteral(true), h.cfg.POTable()),
```
`GETDATE()` elsewhere in that body is fine (Rewrite handles it).

#### B3. Literal 1/0 bools in `InsertReturningID` valuesLists (the four enumerated sites)
Each passes a static valuesList string with `1`/`0` for is_active/is_locked/is_approved.
Convert the string literal to `fmt.Sprintf` and splice `h.dialect.BoolLiteral(...)`:

- `arx_go/records.go:1498` — `` `@p1,...,@p9,GETDATE(),1,0,@p10` `` (is_active=1, is_locked=0):
  ```go
  fmt.Sprintf(`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,GETDATE(),%s,%s,@p10`,
      h.dialect.BoolLiteral(true), h.dialect.BoolLiteral(false)),
  ```
- `arx_go/records.go:1999` — `` `@p1,...,@p8,GETDATE(),GETDATE(),1,0,0,@p9` `` (is_active=1, is_locked=0, is_approved=0):
  ```go
  fmt.Sprintf(`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,GETDATE(),GETDATE(),%s,%s,%s,@p9`,
      h.dialect.BoolLiteral(true), h.dialect.BoolLiteral(false), h.dialect.BoolLiteral(false)),
  ```
- `arx_go/records.go:2748` — `"@p1, 1, 0, '', @p2, @p3"` (is_active=1, is_locked=0):
  ```go
  fmt.Sprintf("@p1, %s, %s, '', @p2, @p3",
      h.dialect.BoolLiteral(true), h.dialect.BoolLiteral(false)),
  ```
- `arx_go/records.go:2859` — identical to B3/2748, same replacement.

No other **production** call sites inline bool literals: every other `InsertReturningID`
value-list binds bool columns via `@pN` Go-`bool` params (verified pos.go:520 statusIsActive,
parts.go:487 is_lot_tracked, lot.go:45, build.go:311). The "+28 repo-wide" occurrences are
all in `arx_go/integration_test.go` (build-tagged `//go:build integration`, SQL-Server-only
test scaffolding using `OUTPUT INSERTED`, `ISNULL`, literal `1/0`). Those are **out of scope**
for this PR — they only run against SQL Server ArxDev today and get their own Postgres
treatment when the integration suite is ported (note in PR description, do not touch here).

#### B4. `copyFormSteps` raw `*sql.Tx` → `*txLogger` (skips Rewrite + DEBUG logging; reads source outside tx)
`arx_go/records.go:2573` signature takes `tx *sql.Tx`; its `tx.QueryRowContext`/
`tx.ExecContext` calls (2646, 2662) hit the raw driver, bypassing `dialect.Rewrite` and
`logSQL`. It also reads its source rows via `h.queryRowContext`/`h.queryContext`
(2575, 2581) — a **separate** connection, outside the tx it was handed.

Changes:
1. Change signature to `func (h *Handler) copyFormSteps(ctx context.Context, tx *txLogger, sourceID, newFormID int) error`.
2. Change the two source reads to use the tx: `tx.QueryRowContext` at 2575, `tx.QueryContext`
   at 2581 (so they read within the transaction).
3. Callers create a raw tx via `h.db.BeginTx` and pass it in — switch both to `h.beginTx`:
   - `arx_go/records.go:2739` (`CreateForm`): `tx, err := h.beginTx(r.Context())`; the
     downstream `tx.QueryRowContext`/`tx.Rollback`/`tx.Commit` calls (2750-2768) work
     unchanged on `*txLogger` (methods promoted).
   - `arx_go/records.go:2850` (`CreateDuplicate`): same switch; 2861-2877 unchanged.
   Both currently pass `nil` opts to `BeginTx`; `h.beginTx` also passes `nil` — no behavior
   change beyond gaining Rewrite + logging.

Note the InsertReturningID copied-step valuesList at 2644 is all `@pN` (no bool literals),
so no B3-style change there.

### Resolved decisions

#### B5 (was Q1). `arx_go/pos.go:2314` — `UPDATE ... FROM ... OUTER APPLY` → correlated subquery
Decision: rewrite to the portable correlated-subquery form (confirmed semantically
identical — same aggregate, same WHERE scope). Replace the statement with:
```sql
UPDATE <po> SET total_cost = COALESCE((SELECT SUM(pol.qty*pol.unit_cost) FROM <pol> WHERE pol.po_id = <po>.ID),0)
  + COALESCE(tax1,0) + ...,
  date_modified = GETDATE()
WHERE rfq_group_id = @p1
```
(preserve the rest of the original `total_cost` expression terms after the `ls.s` COALESCE
term unchanged — only the `ls.s` source becomes the inline correlated subquery, and the
`FROM ... OUTER APPLY ...` clause is removed). No dialect seam needed; `GETDATE()` still
handled by `Rewrite`.

#### Q2 — deferred, out of scope for this PR
`WITH (UPDLOCK, HOLDLOCK)` at `arx_go/records.go:1485` (the #369 serial-number race guard)
stays as-is. Add a short comment above it noting: SQL-Server-only locking hint, no Postgres
story yet, needs a design decision (advisory lock / SERIALIZABLE / dedicated sequence)
before the #625 cutover — tracked as a follow-up, not fixed in this PR.

#### Q3 — deferred, note only
`arxlib/db/dialect.go:142` `Rewrite`'s blind textual pass (matches `GETDATE()`/`@pN` even
inside string literals; `named_queries` user-authored SQL flows through it unguarded) is
left as-is. Add a comment on `Rewrite` documenting the latent hole (literal `'@p1'` or
`'GETDATE()'` in data/named_queries text would be mangled) as a known limitation for a
future fix — not addressed in this PR.

#### Q4 — leave as-is, note the constraint
`arxlib/db/dialect.go:153` `TryCastInt` keeps its current implementation. Add/confirm a
comment on `TryCastInt` documenting the constraint: callers must pass a side-effect-free,
simple expression (current caller `serial_number` — a bare column — satisfies this); the
regex diverges from T-SQL `TRY_CAST` at whitespace/sign edges and the expression is
evaluated twice, so this only accepts simple digit-string columns, not arbitrary expressions.

---

## Verify
- `cd arx_go; .\build.bat` (build + `go test ./...`) after B1-B5.
- SQL/postgres/* are reference DDL — no runtime verification; eyeball parity against the
  canonical siblings listed above.
- Live ArxDev integration tests only exercise SQL Server; they don't cover the postgres DDL
  or the dialect Postgres branch, so B1-B5 are covered by unit build/test + manual review.
- Suggest a regression test for B1 (the INSERT...SELECT...WHERE NOT EXISTS dedup) only if
  the user wants it — behavior is unchanged on SQL Server, so low value until Postgres runs.
