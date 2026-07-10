# [#275] SUP-1 — AVL Attributes on Sourcing Links

**Issue:** [#275](https://github.com/Jolls/arx-legacy/issues/275) · Milestone: v0.6

## Goal

Extend the `supplier_part` (legacy "LNK") sourcing record with Approved Vendor List
attributes, surface them on the part's **Sourcing** tab, and let users edit them via
the existing add/edit forms.

## Acceptance criteria (from the issue)

- `supplier_part` gains: **preferred flag, approved date, approval status
  (Approved / Qualified / Disqualified), MOQ, lead time (days), pack size**
- Sourcing tab on part detail displays these fields per link
- Preferred supplier indicated visually (star or badge)
- Add/edit form updated to include the new fields

## What already exists (and the overlaps to resolve)

`supplier_part` today ([SQL/supplier_part.sql](../../SQL/supplier_part.sql)) already has
columns that partially collide with the AC. These need decisions, not silent choices:

| AC field | Existing column | Tension / decision |
|---|---|---|
| preferred flag (boolean) | `preference INT` (a ranking, DEFAULT 1; but forms store free text like "Primary") | **Add new `is_preferred BIT`.** The existing `preference` is a separate, muddled "ranking" field (INT column populated with free-text — a latent bug, out of scope here). A dedicated boolean is what drives the star/badge. Leave `preference` untouched. |
| lead time (days) | `lead_time VARCHAR(55)` (descriptive text, e.g. "4-6 weeks") — DDL even has a `TODO: consider INT (days)` | **Add new `lead_time_days INT`.** Don't repurpose the free-text column (no clean migration of "4-6 weeks" → int). Two options below. |
| MOQ | `min_increment DECIMAL(11,2)` ("minimum order **increment**") | **Add new `moq DECIMAL(11,2)`.** MOQ (floor you can order at all) ≠ increment (step size). Distinct concepts; keep both. |
| pack size | `pack_size` exists on the **price** table, not the link | **Add `pack_size DECIMAL(11,2)` to `supplier_part`** as the catalog default pack for the sourcing link. Minor duplication with price rows — acceptable; the link value is the vendor's standard pack. |
| approved date | — | New `approved_date DATE` (nullable). |
| approval status | — | New `approval_status VARCHAR(20)` (nullable; NULL = unassessed). Values constrained in UI to Approved / Qualified / Disqualified. |

### Decisions to confirm before building

1. **New columns vs. repurposing `lead_time`/`min_increment`.** Recommendation: **add new
   columns** (`lead_time_days`, `moq`) and leave the existing text/increment columns as-is.
   Simplest, no data migration, no behaviour change for existing links. The alternative
   (retire `lead_time` text, migrate to int) is a larger, riskier change and loses "4-6 weeks"
   style values.
2. **`approval_status` — CHECK constraint or app-only validation?** Recommendation: keep it a
   plain `VARCHAR(20)` and constrain values in the form (`<select>`). A DB CHECK constraint is
   defensible but adds a migration wrinkle for little gain given single-app writes.
3. **`is_preferred` — enforce one-preferred-per-part?** Recommendation: **no** — allow the flag
   freely; the AC only asks to indicate it visually. Enforcing uniqueness is speculative scope.

## Files to change

### 1. Schema (follow the 4-file checklist in CLAUDE.md)

- **[SQL/supplier_part.sql](../../SQL/supplier_part.sql)** — add the six columns in the
  appropriate sections:
  ```sql
  -- Purchasing
  moq                DECIMAL(11,2),   -- Minimum order quantity (floor, not increment).
  pack_size          DECIMAL(11,2),   -- Vendor's standard pack size for this link.
  lead_time_days     INT,             -- Lead time in days (numeric; see lead_time text col).

  -- AVL / approval
  is_preferred       BIT  CONSTRAINT DF_supplier_part_is_preferred DEFAULT 0,
  approval_status    VARCHAR(20),     -- Approved / Qualified / Disqualified. NULL = unassessed.
  approved_date      DATE,            -- Date the vendor link was approved.
  ```
- **[SQL/seed_test_data.sql](../../SQL/seed_test_data.sql)** — extend the `supplier_part`
  INSERT column list + values (rows 4001/4002) with representative AVL data so the fields are
  exercised in ArxDev (e.g. one preferred+Approved link, one Qualified).
- **[arxlib/config/config.go](../../arxlib/config/config.go)** — no new helper needed;
  `SupplierPartTable()` already exists. (Checklist item is table-level, already satisfied.)
- **[SQL/schema.md](../../SQL/schema.md)** — update the `supplier_part` table-reference row
  (line ~178) and the seed-ID note (line ~114) to mention the AVL columns.

### 2. Model — [arx_go/models/supplier.go](../../arx_go/models/supplier.go)

Add to `SupplierPart`:
```go
MOQ          *float64
PackSize     *float64
LeadTimeDays *int
IsPreferred  bool
ApprovalStatus string   // "" = unassessed
ApprovedDate *time.Time
```

### 3. Handler — [arx_go/sourcing.go](../../arx_go/sourcing.go)

- `fetchSupplierLinks` — add the six columns to the SELECT, scan (nullables via
  `sql.NullFloat64` / `sql.NullInt64` / `sql.NullString` / `sql.NullTime`, `is_preferred`
  as `bool`), map onto the struct. Consider `ORDER BY sp.is_preferred DESC, c.name` so the
  preferred vendor sorts first.
- `SupplierPartCreate` — add the six columns to the INSERT and pull from the form
  (`nullableFloat`, a new `nullableInt` already exists per the create call, `approved_date`
  as a nullable date string, `is_preferred` from a checkbox → `r.FormValue("is_preferred") == "on"`).
- `SupplierPartUpdate` — mirror the same columns in the UPDATE SET list.
- `SupplierPartEdit` — add the columns to the SELECT + scan so the edit form pre-fills.
- Helper: add a `nullableDate(s string) any` alongside `nullableFloat` (parse `YYYY-MM-DD`,
  nil on empty/unparseable) if one doesn't already exist. Verify whether `nullableInt` exists
  (the create handler already calls it) — reuse it for `lead_time_days`.

### 4. Template — [arx_go/templates/parts/part_sourcing.html](../../arx_go/templates/parts/part_sourcing.html)

- **Table:** add columns for Status (badge), MOQ, Pack, Lead (days). Show the **preferred**
  indicator as a star/badge next to the supplier name, e.g.
  `{{if .IsPreferred}}<span class="badge bg-warning text-dark" title="Preferred vendor">★ Preferred</span>{{end}}`.
  Render `approval_status` as a Bootstrap badge (Approved → `bg-success`, Qualified →
  `bg-info`, Disqualified → `bg-danger`, blank → nothing). Keep the table readable — the row is
  already wide; may fold Lead Time text + Lead days, or drop the least-used existing column into
  a tooltip. Also bump the pricing sub-row `colspan` to match the new column count.
- **Add form + Edit form:** add inputs for each field:
  - `is_preferred` → `<input type="checkbox" name="is_preferred">` (Bootstrap `form-check`)
  - `approval_status` → `<select>` with blank / Approved / Qualified / Disqualified
  - `approved_date` → `<input type="date">`
  - `moq`, `pack_size` → `<input type="number" step="any" min="0">`
  - `lead_time_days` → `<input type="number" min="0">`
  Both forms are near-duplicates — keep them in sync (same fields, edit form pre-fills from
  `.EditingLink`).

## Verification

1. `cd arx_go && go build ./... && go vet ./... && go test ./...` (must pass — includes
   `smoke_post_test.go` which exercises `SupplierPartCreate`; update its posted form if the
   INSERT requires new non-null values — it won't, all new columns are nullable/defaulted).
2. Reseed ArxDev (human action — ask the user) so the new seed columns land, then run the
   integration suite:
   `go test -tags integration ./arx_go/...` against ArxDev.
3. Manual (user): open a part's Sourcing tab → add a supplier with AVL fields → confirm badge/
   star renders, edit pre-fills, values persist, and preferred sorts first.

## Out of scope

- Fixing the pre-existing `preference INT` vs free-text mismatch (separate cleanup).
- One-preferred-per-part enforcement, approval workflow/audit, RFQ, alternate parts (#278),
  price tiers (#304) — all tracked elsewhere.

## Test to consider after (per CLAUDE.md §5)

A smoke/integration assertion that a created link round-trips the AVL fields (esp. the
nullable date and the `is_preferred` bool) would catch a scan/column-order regression cheaply.
Suggest, don't pre-write.
```
