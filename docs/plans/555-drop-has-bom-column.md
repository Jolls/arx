# 555 — Drop the `has_bom` column

## Decision

Drop `part.has_bom`. Compute BOM presence on demand via the `EXISTS(SELECT 1 FROM bom WHERE parent_part_id = ...)` pattern already codified in [`hasOwnBOMExpr`](../../arx_go/parts.go) and already used by the cost-rollup paths.

**Why drop rather than keep + trigger:**
- The authoritative computation already exists (`hasOwnBOMExpr`) and is already the source of truth for the cost-critical rollup paths — the column is never read there.
- The duplicate flow already distrusts the column and runs its own `EXISTS` ("has_bom is not reliably maintained").
- The only unique readers are two single-part, single-row reads (tab visibility + `/bom` redirect) — no list-level or WHERE-clause usage, so no N+1 to avoid.
- The "decoupled from category" capability (BOM tab on a non-authoring category with no lines) has **no UI** and is unreachable, so the column is currently just a slower-to-trust copy of `EXISTS(bom)`.

A trigger would only earn its keep if the decoupling capability becomes a real, UI-backed product goal — reintroduce the column + trigger + UI together at that point.

## Key design choice — `ShowBOM` (resolved as behavior-preserving)

Keep `Part.HasBOM` field and `ShowBOM() = p.HasBOM || p.Tabs.BOM` **unchanged**. Instead of scanning `has_bom` from the column, populate `HasBOM` from the on-demand `EXISTS` expression in each load query. Net effect: `HasBOM` now means "has actual BOM lines," which preserves today's behavior exactly — any part with lines (including a non-authoring category) keeps its tab; empty authoring categories keep theirs via `Tabs.BOM`.

## Changes

### Go — `arx_go/parts.go`
1. **Three read queries** — replace the `has_bom` column in each `SELECT` with the `EXISTS` BIT expression (reuse `hasOwnBOMExpr` / the same pattern), keeping the existing `hasBOM sql.NullBool` scan target:
   - light load ~line 35
   - part detail load ~line 157
   - the load ~line 604
2. **Delete the maintenance write** in `PartBOMSave` (~lines 961-966) — the `UPDATE ... SET has_bom = CASE WHEN EXISTS(...)` block added in #548.
3. **Delete the `copyBOM` write** (~line 450): `UPDATE %s SET has_bom = 1 WHERE id = @p1`.
4. Leave the duplicate-flow `EXISTS` check (~line 367) and the `SourceHasBOM`/`duplicate_has_bom` form plumbing alone — that's driven by a live line count, not the column.

No change needed to `models/part.go` `ShowBOM` or the `/bom` redirect (~line 220) — both keep working because `HasBOM` is now computed.

### SQL (schema 4-file rule)
5. `SQL/part_number.sql` — remove the `has_bom` column + its comment from the DDL.
6. `SQL/seed_test_data.sql` — drop `has_bom` from the part INSERT blocks.
7. `SQL/schema.md` — remove `has_bom` from the `part` table reference.
8. `SQL/types.sql` — remove any `has_bom` reference.
9. No `cfg.*Table()` helper involved (this is a column, not a table).

### Migration — deferred, not part of this branch
Non-backwards-compatible: existing shipped binaries still `SELECT has_bom` by name, so the actual `ALTER TABLE ... DROP COLUMN` cannot run until all clients are on a build including this change. Per #540 (the tracking issue for this class of change), the migration SQL is posted on that issue rather than committed to `SQL/migrations/` now — add it there once safe to run.

### Tests
10. `models/category_test.go` `TestShowBOM` — the three cases still hold as-is (they exercise `HasBOM`/`Tabs.BOM` on the struct, independent of where `HasBOM` is sourced). Verify no test asserts the column is read/written.

## Verify
- `cd arx_go && go build ./... && go vet ./... && go test ./...`
- Integration tests against ArxDev (touches SQL + handlers): confirm part load, BOM tab visibility, `/bom` redirect, duplicate-with-BOM still work.
- Manual: open an assembly (tab shows empty), a part with lines (tab shows + redirects), a plain part (no tab).
