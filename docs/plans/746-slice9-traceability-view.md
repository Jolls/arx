# Slice 9 (#746) — Build/lot/unit traceability view

Part of epic #736. Re-scopes/closes #715. Read-only drill-down walking the `genealogy` edge table to render the as-built lot+unit tree beneath any output. Depends on slice 4 (genealogy, merged) and slice 8 (#745). Migration = indexes only.

## Key finding (verified against DDL)
Slice 4's migration (`migrate_741_genealogy_table.sql`, `SQL/genealogy.sql`) already created all four single-column indexes the walk's WHERE filters need (`IX_gen_parent_lot`, `IX_gen_parent_unit`, `IX_gen_child_lot`, `IX_gen_child_unit`). `unit`'s `IX_unit_part(part_id, is_active)` covers the Units list. The one new indexing opportunity: none carry `INCLUDE` columns, so each recursive walk step does a key lookup. Making the four covering removes it — this is what slice 9's migration contains (file 7).

## Resolved decisions (user, this session)

- **Q1 → Upgrade `PartLotTrace`** to use the shared generalized walk (a lot's descendant tree can now show it fed a serialized unit).
- **Q2 → Both entry points:** generalized `PartLotTrace` rows link out to `/part/{id}/units/{unitID}` when unit-typed, AND add the minimal "Units" subtab + list page.
- **Q3 → verify `Part.TrackingMode`** exists per slice 8 at implement time (slice 8 plan confirms it adds `TrackingMode` + `TracksSerials`).
- **Q4 → Include the covering-index migration.**

## Original open questions (superseded by the decisions above)

1. **Upgrade `PartLotTrace` to the generalized lot+unit walk, or expose unit-inclusive tracing only via a new unit entry point?**
   - **Recommend: upgrade `PartLotTrace`** to use the shared walk. Q11/§0 "recurse to build the as-built lot+unit tree beneath any output" points at one shared walk; it's the only way a lot's descendant tree can show it fed a serialized unit.

2. **Where does the nav link into a unit's trace live (slice 8 adds no Units list page)?**
   - **Recommend both:** (a) generalized `PartLotTrace` rows link out to `/part/{id}/units/{unitID}` when unit-typed; (b) add a minimal **"Units" subtab + list page** (`PartUnits` + `part_units.html`, mirroring `PartLots`/`part_lots.html`) so a purely `serial`-tracked part still has a deterministic entry point. NOT adding a cross-part `/units` page.

3. **ASSUMPTION flag (slice 8 dependency):** assumes slice 8 adds `Part.TrackingMode string` populated from `tracking_mode`. Per the #745 plan this holds (it adds `TrackingMode` + `TracksSerials`). The single load-bearing point is `models.Part.ShowUnits()` (file 3) — verify field name against slice 8's actual diff at implement time.

4. **Is the covering-index migration warranted, or premature?**
   - **Recommend: include it** — real, low-risk, matches "migration = indexes only," natural point in the epic. Droppable without affecting correctness if reviewer judges premature.

## File-by-file changes

### 1. `arx_go/lot.go` — generalize the walk
- Replace `LotTraceNode` with discriminated `TraceNode` (adds `NodeType string` "lot"|"unit", renames `LotNumber`→`Number`).
- Replace `lotNeighbors` with `traceNeighbors(ctx, id, nodeType, ancestors)` — UNION ALL of lot-endpoint and unit-endpoint joins (Q11 CHECK guarantees exactly one parent/child FK set). Lot branch selects `l.po_line_id`; unit branch selects literal NULL — union column types unify on the lot branch's `INT NULL`. **Build-time check:** confirm compiles/runs against SQL Server.
- Replace `lotTrace` with `genealogyTrace(ctx, rootID, rootType, ancestors)` — **visited-set correctness fix**: key by `(nodeType, id)` pair, not bare `id` (lot and unit id spaces are independent; a bare int key would falsely conflate them).
- Update `PartLotTrace` to call `h.genealogyTrace(..., "lot", true/false)`.
- Update the stale union-join-gap comment above the old `lotNeighbors` (this issue resolves it); leave the `recordGenealogy` write-path comment as-is.

Struct + query + walk detail (see original agent output for full Go text): `traceNeighbors` uses `filterCol = "child_"+nodeType+"_id"` (ancestors) or `"parent_"+nodeType+"_id"` (descendants), `joinPrefix` = parent/child; scans `node_type, id, number, vendorLot(NullString), poLineID(NullInt64), partID, partNumber, partTitle, qty`.

### 2. `arx_go/unit.go` (new) — unit rows, list, trace entry point
- `UnitRow` struct (ID, SerialNumber, PartID, PartNumber, PartTitle, LotID *int, LotNumber, BuildID *int, IsActive, CreatedAt).
- `unitRowSelect()` — SELECT joining `UnitTable` → `PartsTable`, LEFT JOIN `LotTable`.
- `scanUnitRow(sc)`, `unitsForPart(ctx, partID)` (newest first), `fetchUnitRow(ctx, unitID)` (ok=false on ErrNoRows).
- `PartUnits` handler — GET /part/{id}/units, `partPageBase(..., "units")`, renders `parts/part_units.html`.
- `PartUnitTrace` handler — GET /part/{id}/units/{unitID}, validates `unit.PartID == p.ID`, optional `fetchBuildOption` for header, ancestors/descendants via `genealogyTrace(..., "unit", true/false)`, renders `parts/part_unit_trace.html`. Read-only, no edit route. Reuses existing `fetchBuildOption`/`BuildOption` from build.go.

### 3. `arx_go/models/part.go`
- `func (p Part) ShowUnits() bool { return p.TrackingMode == "serial" || p.TrackingMode == "lot_serial" }`. **ASSUMPTION (slice 8):** relies on `Part.TrackingMode` — the one line to double-check.

### 4. `arx_go/parts.go`
- In `tabVisible`'s switch add `case "units": return p.ShowUnits()`.

### 5. `arx_go/main.go`
- After existing lots routes (~239–242): `r.Get("/part/{id}/units", h.PartUnits)` and `r.Get("/part/{id}/units/{unitID}", h.PartUnitTrace)`.

### 6. Templates
- `shared/partials.html` — in `part_tabs` after the `$part.ShowLots` block (~193), add `{{if $part.ShowUnits}}` Units subtab link.
- `parts/part_units.html` (new) — mirror `part_lots.html`. Columns: Serial #, Lot (link to `/part/{PartID}/lots/{LotID}` if set else —), Build (`Build #{BuildID}` plain text, no build detail route exists), Created, Status. Serial links to `/part/{Part.ID}/units/{ID}`.
- `parts/part_lot_trace.html` (modify) — rename `.LotNumber`→`.Number`; add node-type badge; branch link target on `.NodeType` (unit→`/part/{PartID}/units/{ID}`, lot→`/part/{PartID}/lots/{ID}` + vendor badge).
- `parts/part_unit_trace.html` (new) — same shape as `part_lot_trace.html`: breadcrumb, `part_tabs`, unit header card (Serial, Part, Lot link, Build label, Created, Status, no Edit), Ancestors/Descendants tables with same `.NodeType`-branching.

### 7. `SQL/migrations/migrate_746_genealogy_trace_indexes.sql` (new)
- `USE ArxDev;` pin + human-changes-for-ArxProd comment. Idempotent (checks `sys.index_columns` for `qty_consumed` INCLUDE before drop/recreate). Single batch, no GO. Drops + recreates the four `IX_gen_*` indexes with covering INCLUDE columns (parent indexes INCLUDE child_lot_id/child_unit_id/qty_consumed; child indexes INCLUDE parent_lot_id/parent_unit_id/qty_consumed). Postgres equivalents in comments. No `schema_version` bump (perf-only).

### 8. `SQL/genealogy.sql` + `SQL/postgres/genealogy.sql` (reference DDL)
- Update the four `CREATE INDEX` lines to carry matching `INCLUDE` columns so a from-scratch build matches migrated ArxDev. Confirm Postgres index syntax before editing.

### 9. `SQL/schema.md` — no change (index names not referenced there today).

## Not in scope
Cross-part `/units` list; unit edit/update routes; any `is_lot_tracked`/`tracking_mode`/unit-creation logic (slice 8); build detail page (none exists).
