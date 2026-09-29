# #222: inventory domain → sqlc

Part of #222 / #190. One slice: `lot.go`, `unit.go`, `build.go`, `inventory.go`, plus the inventory
statements still raw in `records.go` and `pos.go`. New package `internal/inventory`
(`inventory.sql` + `Service` over `internal/dbq`).

## Scope (raw sites)
- `inventory.go`: `recordInventoryTxn` (ledger insert + `part.stock_on_hand` bump), `PartTransactions`
  ledger read (LEFT JOIN lot). `PartStockAdjust` only calls helpers.
- `lot.go`: `createLot` (INSERT … RETURNING, then lot_number = id), `activeLotsForPart`,
  `lotBelongsToPart`, `recordGenealogy`, lot list/recent/count/fetch (shared select), `traceNeighbors`,
  `LotUpdate`, `appendLotNote`, `AllLots`.
- `unit.go`: unit list/recent/count/fetch, `unitSerialLocked`, `UnitCreate`, `UnitUpdate` (2 variants).
- `build.go`: `activeBuildsForPart`, `fetchBuildOption`, `loadBuildComponents`, `PartBuild` history,
  `loadBuildLines`, `performBuild` (build insert, output-lot link), `PartBuildCreate` return-record
  lock + conditional link.
- `pos.go` `POReceive`: only calls `createLot`/`recordInventoryTxn` on its tx — no SQL of its own left.
- `records.go` (stays out of `convertedFiles`): unit provenance read in `loadRecordTrace`,
  `recordLinkageArgs` lot/build ownership counts, `upsertUnitForRecord` (lookup + insert).

Out of scope: `settings.go` backup table list and `records.go`'s form_record SQL (records domain),
`scopedRecordsRows` (records). Test files keep using `cfg.*Table()`, so the helpers stay.

## Decisions
- Handler helper names/signatures stay (`createLot`, `recordInventoryTxn`, `lotBelongsToPart`, …) as
  1–3 line delegations to `inventory.New(tx)`, so tx statements stay on the caller's `*txLogger`
  (POReceive, PartStockAdjust, performBuild, SaveResults keep one tx) and existing tests compile.
  Row types move to `internal/inventory` and are aliased in the handler files (`type LotRow = inventory.LotRow`)
  so templates keep working.
- Statement order, `FOR UPDATE` on the return record, the conditional `is_locked = FALSE` link and
  `part.stock_on_hand` bump after the ledger insert are unchanged.
- `traceNeighbors` builds column names dynamically (`g.<near>_lot_id`). Replaced by two flat queries
  (`ListTraceAncestors`/`ListTraceDescendants`) that take `near_type` + `id` and select the edge column
  with `CASE WHEN near_type = 'lot' THEN id END` — equality against NULL never matches, so each
  branch still hits its index. The recursive walk (visited set, DFS order) moves to the service
  unchanged; it stays repeated single-level queries (no recursive CTE), so ordering is identical.
- `lot.source` is never written by `createLot` today (stays NULL); preserved.
- `recordLinkageArgs` deliberately does not require `is_active` (a record may reference a retired lot);
  it gets its own count queries rather than reusing the active-only `lotBelongsToPart`.
- Nullable ledger/lot text (`reference`, `note`, `vendor_lot_number`, lot `notes`, build `note`) keep
  `nullableText` semantics: '' → NULL through `sqlc.narg(x)::text` (a `sql.NullString` built in the service).
- Bug found by the characterization tests: `appendLotNote`'s raw `CONCAT(notes, $2)` leaves `$2`
  untyped, so Postgres rejects it and no lot note can be appended (records "lot note" box).
  `AppendLotNote` types both params `::text`; the test pinned the failure first, then flipped.
- `cfg.UnitTable()` lost its last non-test caller and is deleted (tests use the literal `unit`);
  the ledger/build/lot/genealogy helpers stay for the `settings.go` backup list.
- Numeric quantities cast to `float8` in queries (#193).
- The two `records` statements in `PartBuildCreate` live in `inventory.sql` as `LockReturnRecord` /
  `LinkReturnRecord` (build-specific), so `build.go` can join `convertedFiles`.

## Coverage audit (before)
Already covered (integration_test.go / tx_boundaries / categories / timestamps): PartStockAdjust (all
branches), createLot via POReceive (auto number), lotsForPart/fetchLotRow/lotBelongsToPart/PartLotTrace/
LotEditAndUpdate/AllLots, build consume/genealogy/return-record (+ lock races), build-at-test-time,
unit creation/retest/dup serial/manual create/tested count/serialLocked, ancestors trace for lot and
unit, loadBuildComponents, POReceive lot + ledger + races.
Gaps → new `arx_go/inventory_integration_test.go` (all pass on the unchanged code first):
- `PartTransactions` (ledger order, running balance, lot join)
- `createLot` explicit number/vendor lot/description, `recordInventoryTxn` nullables + stock bump
- `appendLotNote` (empty + append format), descendant trace (lot + unit nodes)
- part dashboard `recentPartLots`/`lotCountForPart`/`recentPartUnits`/`unitCountForPart`
- `activeBuildsForPart`/`fetchBuildOption` labels, missing build
- `UnitCreate` with build link / foreign lot rejected, `UnitUpdate` unlocked vs locked vs wrong part
- `LotUpdate` wrong part, `recordLinkageArgs` (retired lot accepted, foreign/non-numeric rejected),
  `upsertUnitForRecord` create-then-reuse

Verify: go build/vet (+ `-tags integration`), go test ./..., sqlc diff, gofmt -l, live ArxDev
integration run, then rerun `QueryDataQualityParts` / `ReportsDataQualityMissingSupplierExportCSV`.
