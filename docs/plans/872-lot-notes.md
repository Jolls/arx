# #872 + #870 — Lot Notes and Test Record Notes

## Context

Two related asks:

- **#872** — lots have nowhere to record free-text notes. Add a Notes field editable at
  `part/<id>/lots/<id>`, and surface the note in the upstream/downstream genealogy tables.
- **#870** — test records have no record-level comment. Individual test *fields* can be
  commented, but some remarks apply to the whole session.

The issue asks whether these should be the same field. **They should not share storage**,
but they must be visible together. Two findings drove that:

1. **`form_record.comments` is already taken.** Despite the name, the UI uses it as the
   record **Type** dropdown ("New Release", "Re-Test") — [record_new.html:65](../../arx_go/templates/records/record_new.html#L65),
   [record_edit.html:39](../../arx_go/templates/records/record_edit.html#L39), and
   `models.TestRecord.Comments` is literally annotated `// used as "Type" in the UI`
   ([trmodels.go:68](../../arx_go/models/trmodels.go#L68)). #870 needs a new column.

2. **A single shared column loses data.** Two testers open records on the same lot, both
   see note "A". Tester 1 appends B → "A\nB". Tester 2's textarea still holds the stale
   "A" from page load; they append C → "A\nC". B is gone silently. Record edit pages stay
   open for a whole test session, so the stale window is hours. Separately, `is_locked` /
   `is_approved` freeze a record as a quality document — a shared field would keep mutating
   inside an approved record. And records on non-lot-tracked parts have no lot at all.

**Outcome:** two columns, one combined display. The lot note is batch-level and persistent;
the record note is session-level and freezes with the record. Where the record page needs
to contribute to the lot note, it does so as an **append-only delta** — the browser sends
only the new sentence and the server concatenates server-side, so simultaneous appends both
land.

## Decisions

| Question | Decision |
|---|---|
| Storage | Two columns: `lot.notes`, `form_record.notes` |
| Field type | `VARCHAR(MAX)` / `TEXT`, rendered as a textarea |
| Lot note from record page | Append-only delta box; full rewrite only on the lot edit page |
| Locked/approved records | `form_record.notes` freezes with the record; lot appends blocked too |
| Scope | Both issues in one PR |

**Column naming.** Both columns are plain `notes`, matching the schema's existing convention:
entity tables carry `notes` (`part`, `contact`, `purchase_order`, `company_attachment`,
`release_notes`) while event/row tables carry singular `note` or `comment(s)` (`build`,
`inventory_transaction`, `purchase_order_history`, `form_row`, `result`, `record_events`).
`lot` and `form_record` are both entity tables — `form_record` is the parent of
`record_events`, not an event itself — so `notes` is correct on both. No `lot_note` /
`record_note` prefixing.

**Design call to confirm during implementation:** appended lot notes get a server-side
attribution prefix (`[jolls 2026-08-06] `) via `h.currentUser(r)` ([auth.go:132](../../arx_go/auth.go#L132)).
Without it a multi-author free-text field becomes unreadable. One line of Go — strike it if
unwanted.

## Schema

New migration `SQL/azure/migrations/migrate_872_lot_and_record_notes.sql`, pinned to
`USE ArxDev;` with the usual "human changes this for ArxProd" comment. Two guarded adds:

```sql
IF COL_LENGTH('dbo.lot', 'notes') IS NULL
    ALTER TABLE dbo.lot ADD notes VARCHAR(MAX) NULL;
IF COL_LENGTH('dbo.form_record', 'notes') IS NULL
    ALTER TABLE dbo.form_record ADD notes VARCHAR(MAX) NULL;
```

No `EXEC(N'...')` dynamic-SQL wrapper needed — nothing later in the batch references the new
columns (no CHECK constraint, no backfill), so the same-batch name-resolution trap documented
in CLAUDE.md doesn't apply here. Single batch, no `GO`.

Reference DDL to keep in sync (never auto-run):
- [SQL/azure/lot.sql](../../SQL/azure/lot.sql) and [SQL/postgres/lot.sql](../../SQL/postgres/lot.sql)
- [SQL/azure/form_record.sql](../../SQL/azure/form_record.sql) and [SQL/postgres/form_record.sql](../../SQL/postgres/form_record.sql)

Seed data — both files use **explicit column lists**, so new columns are not picked up
automatically: `SQL/azure/seed_test_data.sql` lot INSERT (line ~454) and form_record INSERT
(line ~572), plus the postgres mirror. Seed at least one lot with a multi-line note and one
record with a note so the display work is testable without hand-editing rows.

`SQL/schema.md` table reference: add both columns with their semantics.

No `cfg.*Table()` helper needed — these are new columns on existing tables, not new tables,
so the 4-file new-table rule doesn't apply.

## Backend

### Lot side — [arx_go/lot.go](../../arx_go/lot.go)

- `LotRow` (L117) gains `Notes string`; add the column to `lotRowSelect()` (L131),
  `scanLotRow` (L141, scan through a `sql.NullString` like `VendorLot`), and the parallel
  hand-written SELECT in `recentPartLots` (L177) — it duplicates the column list rather than
  reusing `lotRowSelect`.
- `LotUpdate` (L440) extends its UPDATE to `notes = @p3`, reading `fv(r, "notes")`.
- `TraceNode` (L225) gains `Notes string`; `traceNeighbors` (L247) adds `l.notes` to the lot
  half of the UNION and a matching `NULL` to the unit half — **both halves or the UNION
  breaks on column count** — plus the scan at L279.
- New `appendLotNote(ctx, lotID int, text, username string) error`. Dialect-neutral, no
  `Dialect` interface change needed (`CASE`/`COALESCE`/`CONCAT` all work on both engines):

  ```sql
  UPDATE <lot> SET notes = CASE WHEN COALESCE(notes,'') = '' THEN @p1 ELSE CONCAT(notes, @p2) END
  WHERE id = @p3
  ```

  args: `entry`, `"\n\n" + entry`, `lotID` — where `entry` is the attribution prefix plus the
  submitted text. Use `h.execContext` per the debug-mode wrapper rule.

### Record side — [arx_go/records.go](../../arx_go/records.go)

`form_record.notes` must be threaded through every full-record SELECT and its scan. The sites
all share the `subject_part_number, subject_pn_description` column-list shape:

- SELECTs at L1215, L1354, L1855, L2110, L2367, L2679 (+ their `&record.Comments`-adjacent scans)
- INSERT column lists at L1582 and L2152
- UPDATE statements at L2645 / L2649
- `models.TestRecord` ([trmodels.go:68](../../arx_go/models/trmodels.go#L68)) gains
  `Notes string` next to the existing `Comments`

This mechanical breadth is the bulk of #870's cost. Work from the L1215 site outward so the
column order stays consistent across all of them.

**Query hygiene:** any statement selecting both tables' notes in one go (e.g. joining `lot`
for the record page) must alias — `SELECT fr.notes, l.notes AS lot_notes`. Not a scan hazard,
since `records.go` scans positionally rather than by column name, but keep the aliases for
readability.

**Duplicate behavior:** the record-duplicate path (L2110–L2159) should *not* copy the record
note — it's specific to one test session. Leave it blank on the new record.

**Lock enforcement:** the record update handler already gates on `is_locked` / `is_approved`;
route the record-note writes and the lot-append POST through the same guard rather than
adding a parallel check.

### New route

`POST /records/{id}/lot-note` → appends to the linked lot's note, then redirects back to the
record. Register alongside the existing record routes in [main.go](../../arx_go/main.go).
CSRF token required (`h.csrfToken`), consistent with every other POST.

> **As built — no separate route.** A dedicated route was implemented and then removed: its
> POST + redirect discarded every unsaved edit on the record page (a whole test session's
> results), which a code-review pass caught. The lot-note box is instead an ordinary field of
> the record's edit form, appended by `SaveResults` inside the record-save transaction. The
> append-only delta shape is unchanged — the box still carries only the new sentence — so the
> concurrency property this plan exists for still holds.

**Resolving the lot:** a record's lot is often reached *through* `unit_id`, not `lot_id`
directly — the #745/Q8 invariant, already implemented at [records.go:1679-1686](../../arx_go/records.go#L1679-L1686).
Reuse that resolution rather than reading `form_record.lot_id` alone, or notes will silently
fail to attach on unit-linked records.

## Templates

| File | Change |
|---|---|
| `parts/part_lot_edit.html` | Notes textarea (full rewrite of the field), after Vendor Lot |
| `parts/part_lot_trace.html` | Notes row in the header `<dl>`; Notes column in **both** the Source-lots and Downstream-lots tables |
| `parts/part_lots.html` | Notes column in the per-part Lots subtab table |
| `parts/all_lots.html` | Notes column (sortable, matching the existing `sortable` headers) |
| `records/record_edit.html` | Lot note read-only + "Add to lot note" delta box near the existing Lot/Build row (L62-89); Record Note textarea near the top |
| `records/records_show.html` | Both notes, read-only |
| `records/record_print.html` | Both notes, if they belong on the printed record |

Long notes in table cells: truncate with the full text in a `title` attribute rather than
letting a multi-line note blow up row height. Use Bootstrap utilities (`text-truncate`,
`d-inline-block` with a max width) rather than inline styles.

## Out of scope

- **Do not rename `form_record.comments` → `record_type`.** Filed as
  [#874](https://github.com/Jolls/arx-legacy/issues/874). It is genuinely misnamed — it holds the
  Type dropdown value — but a rename migration also has to patch stored query text in
  `named_queries` (`sp_rename` doesn't touch it), and it would ripple through templates,
  models, and the report layer.

  No ordering dependency between #874 and this PR: `form_record.notes` and
  `form_record.comments` can coexist. If this PR lands first the table briefly carries both,
  which reads oddly until #874 clears it; if #874 lands first, cleaner. Either order works.
- The `note` thread table (append-only rows with author/timestamp, attachable to lot or
  record) was considered and set aside as heavier than needed. If lot notes later need
  per-entry attribution, deletion, or ordering, that's the shape to revisit — the existing
  `record_events` table ([records.go:1287](../../arx_go/records.go#L1287)) is the precedent.

## Verification

1. `cd arx_go; go build ./... ; go vet ./... ; go test ./...` — template parse tests
   (`templates_parse_test.go`) will catch malformed template edits.
2. Human runs `migrate_872_lot_and_record_notes.sql` against ArxDev, then reseeds
   (reseeding is a human action — do not script it).
3. Live integration tests: `$env:ARX_TEST_DSN=...ArxDev...; go test -tags integration ./arx_go/...`.
   Check `arx_go/config/local.json` for `test_engine: "postgres"` first — it blocks a
   sqlserver `ARX_TEST_DSN` connect.
4. Manual pass (describe to the user, don't run the app):
   - Lot edit page → enter a multi-line note → saved, shows on the lot detail header.
   - Lot note appears in Lots subtab, `/lots`, and both trace tables (find a lot with
     genealogy in both directions — seed lot 8302 has upstream and downstream edges).
   - Record edit → lot note shows read-only; append a line → lands with attribution, prior
     text intact.
   - **Concurrency check (the whole point):** open the same lot's record page in two tabs,
     append from tab 1, then append from tab 2 without reloading → both lines present.
   - Record note saves; lock the record → both the record note and the lot-append box go
     read-only.
   - A record on a *unit-linked* lot (lot reached via `unit_id`, not `lot_id`) still
     resolves and appends correctly.

## Branch

Current branch is `feature/sql-azure-postgres-folder-split` — unrelated. Branch fresh off
`main` as `feature/872-lot-record-notes`. PR body closes both:

```
Closes #872
Closes #870
```

Schema change + wide record-layer edits — expect a `/code-review high` pass before commit.
