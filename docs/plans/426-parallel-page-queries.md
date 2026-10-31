# #426 plan: parallel page queries and explicit connection pool

Branch: `feature/426-parallel-page-queries` (at `ae202ce`, same as `origin/main`). Paths are repo-relative. Items marked (Q#) follow the stated default unless **Open questions** is answered otherwise. No schema change, no migration, no seed change.

## Order of work
1. **Baseline timing first**, before any edit (see "Before/after timing").
2. Add characterization tests (Test plan part 2). Run against the local container; all pass on unchanged code.
3. Add red tests and stubs (Test plan part 3). Confirm they fail on assertions.
4. Sections A-H below, in order. After each: `go build ./... ; go vet ./... ; go test ./...`, plus the integration suite after D-G.
5. Lower budgets as each page lands (H). Repeat timing. Delete the timing file. Changelog. Follow the CLAUDE.md pre-commit sequence.

## Findings that shape the design

**Hazard 1: pool cap.**
- Open `*sql.Rows` while querying: none. Every `internal/dbq` `:many` closes rows before returning; hand-written loops (`arx_go/settings.go:833` backup CSV, `arx_go/named_query.go:184`) issue no query inside the loop. `arx_go/cmd/backfill_attachment_hash` is single-threaded, so it uses at most 2 connections.
- Transaction held while a query goes through the pool (hold-and-wait), all on POST paths:
  - `PartBOMSave`: `parts.go:824`
  - `POCreate`/`POUpdate` via `resolvePolRev`: `pos.go:630, 749, 768`
  - `SaveResults`: `records.go:2054` (`recordLinkageArgs`), `records.go:2115` (`loadBuildLines`)
  - `PartCreateRFQsConfirm`: `rfq_bom.go:280` (`applyPODefaults`), `rfq_bom.go:315, 321, 324` (`insertBOMRFQ`)
  - `SupplierPartCreate`/`SupplierPartUpdate`: `renderSourcingWithError` runs on failure before the deferred rollback (`sourcing.go:99, 105` and the `fail` calls after `beginTx`)
- Stalling requires 12 simultaneous requests all in this state. The app binds 127.0.0.1 and is one process per desktop, serving one user over HTTP/1.1, where a browser allows 6 connections per host. So at most 6 are held and 6 stay free. A wait also ends when the client disconnects, because `database/sql` honours the request context while queueing.
- **Edit: none to these sites (Q1).** `TestIntegration_ParallelPages_OneConnection` pins that fan-out itself can't starve.
- Nested `parallel` / fan-out self-starvation: impossible. A branch holds a connection only for one query and holds none while waiting on a nested group, so a pool of 1 still completes.
- Fan-out inside a transaction: none. All six handlers are GET and call `parallel` outside any `beginTx`. `loadFrozenRows` keeps running sequentially for its other callers.

**Hazard 2: data races.**
- `recordRoundTrip` writes `*sqlStats`, so each branch gets its own child (below).
- Helpers taking `*http.Request` read `r.Context()` and would hit the **parent** stats. These are `fetchPOItems`, `fetchPOHistory`, `fetchPOReceipts`, `fetchSupplierBulkOrderOptions`. **Their branches must pass `r.WithContext(ctx)`.**
- Closures must not share an outer `err`: use a named return `func(ctx context.Context) (err error)` in each one.
- Safe in branches (verified):
  - `h.st()`/`h.cfg()`/`h.database()`: atomic pointer
  - `h.logSQL`: `log` is locked
  - `userLocationCtx`: context read plus `tzCache` (`Load`/`Store`)
  - services: `Service{q}` built per call
  - `inventory.Trace`: state local to the call
- Parent only (never in a branch): `setNavContext`, `h.session`, `h.csrfToken`, `h.render`, `applyCategoryTabs`, and any write to `p` including `p.ThumbnailURL`.
- `currentUser` is a context read, but stays on the parent anyway.

**Hazard 3: error behaviour today.** `renderError` responds with status 200 and the error page; `serverError` responds 500 with `msg`.

| Page | Fatal (message) | Tolerated |
|---|---|---|
| `/reports` | Any of the 11 queries: `Error loading dashboard: <err>` | none |
| `/part/{id}` | `GetPart`: no rows `Part not found`, else `Error retrieving part: <err>` | All others: attachments logged `[part] attachments for part %d`, rest silent, card empty |
| Record detail | `GetRecord`: no rows 404, else 500 `query error`. Form header, results, steps, trace: 500 `query error` | Neighbors silent; events and snapshots logged |
| Lot trace | Part base errors. `Invalid lot id`. `Error retrieving lot:`. `Lot not found for this part`. `Error tracing lot ancestry:`. `Error tracing lot descendants:`. `Error retrieving record types:`. `Error retrieving lot units:` | none |
| Unit trace | Part base errors. `Invalid unit id`. `Error retrieving unit:`. `Unit not found for this part`. `Error retrieving build:`. `Error tracing unit ancestry:`. `Error tracing unit descendants:`. `Error retrieving record types:` | none |
| PO detail | `GetPO`: no rows `Purchase order not found`, else `Error retrieving purchase order:`. `Error loading PO items:` | History and receipts logged, return nil; bulk-order silently defaults |

- **Design:** `parallel` uses the zero-value `errgroup.Group`, not `WithContext`, so there is no sibling cancellation. A context-cancelled text can't appear from a sibling, and tolerant pages can't lose branches.
- Every branch context derives from `r.Context()`. A disconnect cancels all branches and queued connection waits, and the page reports `context canceled` as it does today.
- One failure gives the same message as today. With two or more simultaneous failures, the first in time wins instead of the first in code order (Q2).
- A failing branch no longer short-circuits later queries; they run to completion and are discarded.
- A panic in a goroutine would bypass `net/http`'s per-connection recover and kill `Arx.exe`. `errgroup` v0.24.0 does not propagate panics, so `parallel` recovers, logs the stack, and re-panics on the caller after `Wait`.

**Hazard 4: pgx wiring (v5.11.0, `stdlib/sql.go`).**
- `OptionShouldPing` applies only through `OpenDB`/`GetConnector`, so `Connect` must move from `sql.Open`.
- The `sql.Open` path (`driverConnector.Connect`) calls `pgx.ParseConfig(dsn)` then `pgx.ConnectConfig`. The new path calls the same `ParseConfig` once and `ConnectConfig` per connection, so TLS (`sslmode=require`) and all DSN settings are identical.
- A parse failure now surfaces as `open: ...` rather than `ping: ...`. Text is still password-redacted by `ParseConfigError`, and `ClassifyConnectError` uses `errors.As`/`Contains`, not the prefix.
- `OpenReadOnly` still calls `sql.Open("pgx", ...)`, so the registration is still needed. It stays: the named `stdlib` import still runs `init`. The RO pool keeps pgx's default ping.
- A new connection's `lastResetSessionTime` is zero, so its first reuse pings once regardless. This is why timing discards warm-up iterations.
- `golang.org/x/sync v0.24.0` is already in `go.mod` as `// indirect`; `go mod tidy` makes it direct (no version change).

**Hazard 5: merged queries.**
- `COUNT(*) OVER ()` runs before `LIMIT`, so it returns the full count. A part with no rows returns zero rows, and the service returns total 0, matching `COUNT(*)` today. The joins are FK-to-PK and can't multiply rows.
- `UNION ALL` columns are compatible: `date_ordered`/`effective_date` are `DATE`; `unit_cost`/`price_ea` are `NUMERIC`.
- `ORDER BY source, point_date` reproduces today's output (PO rows chronological, then price rows chronological, because `'po'` < `'price'`).
- A scratch-copy `sqlc generate` confirmed the generated types shown below.
- `p.price_ea IS NOT NULL` moves from Go into SQL.

**Hazard 6: budgets.** See section H. All current counts were reproduced from code and seed, so derivations are exact. The dashboard's 2 is achievable.

## File changes

### A. `internal/db/db.go`
Change the import to named `"github.com/jackc/pgx/v5/stdlib"`; add `"github.com/jackc/pgx/v5"`, `"context"`, `"time"`. Replace `Connect` with:

```go
const (
	maxPoolConns      = 12 // covers the widest page fan-out (#426)
	connMaxIdleTime   = 15 * time.Minute
	pingIdleThreshold = 30 * time.Second
)

// shouldPing replaces pgx's default of pinging any connection idle over 1 s, which
// costs a round trip on nearly every page (#426). A connection that dies inside the
// threshold is noticed on its first query instead.
func shouldPing(_ context.Context, p stdlib.ShouldPingParams) bool {
	return p.IdleDuration > pingIdleThreshold
}

// openPool builds the main pool for dsn without connecting.
func openPool(dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	database := stdlib.OpenDB(*cfg, stdlib.OptionShouldPing(shouldPing))
	database.SetMaxOpenConns(maxPoolConns)
	database.SetMaxIdleConns(maxPoolConns)
	database.SetConnMaxIdleTime(connMaxIdleTime)
	return database, nil
}

// Connect opens and verifies a Postgres connection for dsn.
func Connect(dsn string) (*sql.DB, error) {
	database, err := openPool(dsn)
	if err != nil {
		return nil, err
	}
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return database, nil
}
```
`OpenReadOnly` is unchanged.

### B. `arx_go/handlers.go` and `go.mod`
`go.mod`: run `go mod tidy`; the only expected diff is `golang.org/x/sync v0.24.0` moving to direct.

`sqlStats` (line 193):
```go
// sqlStats accumulates per-request SQL stats. count is round trips on the critical path
// (what the user waits for), queries is every query issued, total is cumulative DB time
// across branches (can exceed wall-clock). A goroutine owns its sqlStats; parallel gives
// each branch a child and folds it in after Wait, so no lock.
type sqlStats struct {
	count   int
	queries int
	total   time.Duration
}
```
`recordRoundTrip`: add `st.queries++` after `st.count++`.

Add after `timeQueryErr` (imports: `runtime/debug`, `golang.org/x/sync/errgroup`):
```go
// parallel runs independent read-only branches concurrently and waits for all of them
// (#426). It returns the first error in time; a failure does not cancel siblings. Each
// branch must use the ctx it is given, write only its own variables, and never run inside
// a transaction; assemble results after parallel returns. Nestable.
func parallel(ctx context.Context, branches ...func(context.Context) error) error {
	parent, _ := ctx.Value(ctxSQLStatsKey).(*sqlStats)
	children := make([]*sqlStats, len(branches))
	panics := make([]any, len(branches))
	var g errgroup.Group
	for i, branch := range branches {
		branchCtx := ctx
		if parent != nil {
			children[i] = &sqlStats{}
			branchCtx = context.WithValue(ctx, ctxSQLStatsKey, children[i])
		}
		g.Go(func() error {
			defer func() {
				if p := recover(); p != nil {
					log.Printf("parallel branch panic: %v\n%s", p, debug.Stack())
					panics[i] = p
				}
			}()
			return branch(branchCtx)
		})
	}
	err := g.Wait()
	for _, p := range panics {
		if p != nil {
			panic(p) // back on the handler goroutine, where net/http recovers it
		}
	}
	if parent != nil {
		longest := 0
		for _, c := range children {
			longest = max(longest, c.count)
			parent.queries += c.queries
			parent.total += c.total
		}
		parent.count += longest
	}
	return err
}

// pageErr prefixes a branch error with its user-facing message; nil stays nil.
func pageErr(prefix string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", prefix, err)
}
```

Display:
- `routeAccumulator` (line 652): add `queries int`.
- `logRouteSummary(r, count, queries int, total)`: `acc.queries += queries`; log `"[PERF SUMMARY] %s | %d round trips (%d queries) | %s total"`.
- `profileRequest`: log `"[PERF] %s %s | %d round trips (%d queries) | %s in DB | %s total"` with `st.queries`; call `h.logRouteSummary(r, st.count, st.queries, elapsed)`.

### C. `/reports`: `arx_go/reports.go` `ReportsDashboard` (lines 73-122)
Replace the ten sequential calls and their error blocks with declared result variables and one call:
```go
err := parallel(r.Context(),
	func(ctx context.Context) (err error) { openPOs, err = h.dashboardOpenPOCount(ctx); return },
	... // one per existing call, same args: receivedThisMonth, activity(10), topFailureModes(5),
	    // lowestYieldForms(5), staleWIPRecords(5), pendingApprovalPOs(5), belowReorderParts(5),
	    // recentLots(5), recentBuilds(5)
)
if err != nil {
	h.renderError(w, r, "Error loading dashboard: "+err.Error())
	return
}
```
Types: `openPOs, receivedThisMonth int`; `activity []dashboardActivityItem`; `topFailureModes []dashboardFailureModeItem`; `lowestYieldForms []dashboardYieldItem`; `staleWIPRecords []dashboardStaleWIPItem`; `pendingApprovalPOs []dashboardPendingApprovalItem`; `belowReorderParts []dashboardBelowReorderItem`; `recentLots []dashboardRecentLotItem`; `recentBuilds []inventory.BuildListRow`. The `data[...]` assignments are unchanged. `dashboardRecentActivity` stays sequential (2 queries) and is not nested.

### D. Merged queries (sqlc)
`internal/inventory/inventory.sql`:
- Delete `CountPartLots` and `CountPartUnits`.
- Add `ListRecentPartLots :many` in place of `CountPartLots`. `ListLots` is unchanged and still serves the other lot lists.
```sql
-- name: ListRecentPartLots :many
-- Part dashboard Lots card: the n newest lots plus the part's total lot count. The window runs
-- before LIMIT, so total_count ignores n; a part with no lots returns no rows (total 0).
SELECT l.id, l.lot_number, COALESCE(l.vendor_lot_number, '') AS vendor_lot, l.part_id,
       COALESCE(p.part_number, '') AS part_number, COALESCE(p.description, '') AS part_description,
       l.lot_description, COALESCE(l.notes, '') AS notes, l.created_at, l.is_active,
       COALESCE(po.number, '') AS po_number, (COUNT(*) OVER ())::int AS total_count
FROM lot l JOIN part p ON p.id = l.part_id
LEFT JOIN po_line pol ON pol.id = l.po_line_id
LEFT JOIN purchase_order po ON po.id = pol.po_id
WHERE l.part_id = sqlc.arg(part_id)
ORDER BY l.created_at DESC, l.id DESC
LIMIT sqlc.arg(n)::int;
```
- `ListRecentPartUnits`: add `, (COUNT(*) OVER ())::int AS total_count` after `u.source`.

`internal/parts/parts.sql`: replace `ListPOPricePoints` and `ListPriceListPoints` with:
```sql
-- name: ListPartPricePoints :many
-- PO lines, then active priced price-list rows, each oldest first ('po' sorts before 'price').
SELECT 'po'::text AS source, po.number AS po_number, COALESCE(po.supplier_name, '') AS supplier_name,
       po.date_ordered AS point_date, pol.unit_cost AS cost, NULL::numeric AS pack_size
FROM po_line pol
JOIN purchase_order po ON pol.po_id = po.id
WHERE pol.part_id = sqlc.arg(part_id)::int AND po.date_ordered IS NOT NULL
UNION ALL
SELECT 'price'::text, ''::text, COALESCE(c.name, ''), p.effective_date, p.price_ea, p.pack_size
FROM price p
LEFT JOIN company c ON p.supplier_id = c.id
WHERE p.part_id = sqlc.arg(part_id)::int AND p.is_active = TRUE AND p.effective_date IS NOT NULL AND p.price_ea IS NOT NULL
ORDER BY source, point_date;
```
Run `sqlc generate` from the repo root and commit `internal/dbq`. Expected generated rows:
- `ListPartPricePointsRow{Source, PoNumber, SupplierName string; PointDate *time.Time; Cost decimal.Decimal; PackSize *decimal.Decimal}`
- `ListRecentPartLotsRow` = the `ListLots` fields plus `TotalCount int`, params `{PartID, N int}`
- `ListRecentPartUnitsRow` gains `TotalCount int`

`internal/inventory/inventory.go`:
- `ListRecentPartLots(ctx, partID, n int) ([]LotRow, int, error)`: call the new query, build each `LotRow` field by field (conversion no longer compiles), take total from `rows[0].TotalCount` when `len(rows) > 0`, else 0. Return non-nil empty slice as today.
- `ListRecentPartUnits`: same shape.
- Delete `CountPartLots` and `CountPartUnits`.

`internal/parts/parts.go` (lines 959-1003): delete `POPricePoint`, `ListPOPricePoints`, `PriceListPoint`, `ListPriceListPoints`; add:
```go
// PricePoint is one dated unit-cost sample. Source is "po" or "price"; PONumber is "" on a
// price row, PackSize nil on a PO row.
type PricePoint struct {
	Source, PONumber, SupplierName string
	Date                           time.Time
	Cost                           decimal.Decimal
	PackSize                       *decimal.Decimal
}

// ListPricePoints returns a part's dated PO lines then its dated active prices, each oldest first.
func (s *Service) ListPricePoints(ctx context.Context, partID int) ([]PricePoint, error)
```
Body: map rows, `Date: *r.PointDate` (guaranteed by the `WHERE`).

`arx_go/parts.go` `partPricePoints` (line 1815): one call; on error return nil; otherwise append `pricePoint{Date: p.Date.Format("2006-01-02"), Cost: p.Cost, PO: p.PONumber, Supplier: p.SupplierName, Source: p.Source, PackSize: p.PackSize}`. `points` stays a nil slice when empty.

`arx_go/lot.go`: `recentPartLots` returns `([]LotRow, int, error)`; delete `lotCountForPart` (lines 76-80).
`arx_go/unit.go`: `recentPartUnits` returns `([]UnitRow, int, error)`; delete `unitCountForPart` (lines 35-39).

(Behaviour: failures now drop list+count together, and both price sources together; the page still renders (Q3).)

### E. `/part/{id}`: `arx_go/parts.go` `PartDetail` (lines 208-301)
Keep fetch and BOM redirect. Then:
1. Move up, before the fan-out: `h.setNavContext(...)`, `h.applyCategoryTabs(r.Context(), &p)`, `sess`/`navBack`. `p` is read-only until `parallel` returns.
2. Declare `primaryAtt *models.Attachment`, `partAtts`, `prefPrice`, `recentPOs`, `recentTxns`, `recentLots`, `lotCount`, `recentUnits`, `unitCount`, `buildCount`, `prefSupplier`, `pts []pricePoint`. Delete `attSvc`.
3. `_ = parallel(r.Context(), ...)` (all branches return nil), one branch each. Each keeps today's `Show*` gate inside the closure and swallows errors as today:
   - `primaryAtt = h.partPrimaryAttachment(ctx, p)`
   - `partAtts, err = h.attachments().ListPartAttachments(ctx, p.ID)`; on error keep the same `log.Printf`
   - `prefPrice, _ = h.parts().PreferredSupplierPrice(ctx, p.ID)` (ungated)
   - `ShowOrders`: `recentPOs, _ = h.parts().ListRecentPOs(ctx, p.ID, 5)`
   - `ShowInventory`: `recentTxns = h.recentPartTxns(ctx, p.ID, 5)`
   - `ShowLots`: `recentLots, lotCount, _ = h.recentPartLots(ctx, p.ID, 5)`
   - `ShowUnits`: `recentUnits, unitCount, _ = h.recentPartUnits(ctx, p.ID, 5)`
   - `ShowBuild`: `buildCount, _ = h.buildCountForPart(ctx, p.ID)`
   - `ShowSuppliers`: `prefSupplier = h.preferredSupplier(ctx, p.ID)`
   - `ShowPricing`: `pts = h.partPricePoints(ctx, p.ID)`
4. After `parallel`, in the existing form on the parent:
   - the attachments loop (the only `p.ThumbnailURL` write)
   - `purchasePrice` and rollup delta
   - `if prefSupplier != nil { prefSupplier.Price = prefPrice }`
   - `if len(pts) > 0 { ...priceJSON... }`
   
   `h.render` is unchanged.

### F. Record detail: `arx_go/records.go`
Split `loadFrozenRows` (line 957): keep its signature for `RecordPrint`/`EditRecord`, make it load `results` and `liveSteps` as today, then call a new pure function holding the rest of the body:
```go
func frozenRows(results map[int]*models.TestResult, liveSteps map[int]*models.TestStep, record *models.TestRecord, form *models.TestForm, includeHidden bool) ([]models.ResultRow, map[int]*models.TestStep)
```
`RecordDetail` (lines 1030-1095), after `record := testRecord(rec)`:
```go
var (
	hdr                       records.FormHeader
	results                   map[int]*models.TestResult
	liveSteps                 map[int]*models.TestStep
	prevID, nextID            int
	eventRows                 []records.Event
	snapshots                 map[int][]models.SnapshotDiffRow
	trace                     *RecordTrace
)
err = parallel(r.Context(),
	func(ctx context.Context) (err error) { hdr, err = h.records().GetFormHeader(ctx, record.FormID); return },
	func(ctx context.Context) (err error) { results, err = h.loadRecordResults(ctx, record.ID); return },
	func(ctx context.Context) (err error) { liveSteps, err = h.loadSteps(ctx, h.records(), record.FormID); return },
	func(ctx context.Context) error { prevID, nextID, _ = h.records().Neighbors(ctx, record.FormID, recordID); return nil },
	func(ctx context.Context) (err error) {
		if eventRows, err = h.records().ListEvents(ctx, recordID); err != nil {
			log.Printf("RecordDetail: audit trail query error: %v", err)
		}
		return nil
	},
	func(ctx context.Context) (err error) {
		if snapshots, err = h.loadEventSnapshots(ctx, recordID); err != nil {
			log.Printf("RecordDetail: event snapshots error: %v", err)
		}
		return nil
	},
	func(ctx context.Context) (err error) { trace, err = h.loadRecordTrace(ctx, &record, false); return },
)
if err != nil {
	serverError(w, "query error", err)
	return
}
form := testForm(hdr)
resultRows, refSteps := frozenRows(results, liveSteps, &record, &form, false)
```
Then the existing `{id}` resolve loop, image rows and events mapping; `canApproveRecords`; render. Delete the moved sequential calls. `record` is not mutated until after `parallel` (`loadRecordTrace` only reads it).

### G. Lot trace, unit trace, PO detail
`arx_go/lot.go` `PartLotTrace` (lines 174-193), after the `found` check. Replace the four sequential calls:
```go
var ancestors, descendants []TraceNode
var typeOptions []string
var units []UnitRow
if err := parallel(r.Context(),
	func(ctx context.Context) error { var err error; ancestors, err = h.genealogyTrace(ctx, lotID, "lot", true); return pageErr("Error tracing lot ancestry", err) },
	func(ctx context.Context) error { var err error; descendants, err = h.genealogyTrace(ctx, lotID, "lot", false); return pageErr("Error tracing lot descendants", err) },
	func(ctx context.Context) error { var err error; typeOptions, err = h.scopedRecordTypeOptions(ctx, records.ScopeLot, lotID); return pageErr("Error retrieving record types", err) },
	func(ctx context.Context) error { var err error; units, err = h.inventory().ListLotUnits(ctx, lotID); return pageErr("Error retrieving lot units", err) },
); err != nil {
	h.renderError(w, r, err.Error())
	return
}
```

`arx_go/unit.go` `PartUnitTrace` (lines 90-120), after the `found` check and building `roots`. Same shape; branches:
- build: `if unit.BuildID == nil { return nil }`, then `pageErr("Error retrieving build", err)`
- ancestors: `pageErr("Error tracing unit ancestry", err)`
- descendants: `pageErr("Error tracing unit descendants", err)`
- typeOptions: `pageErr("Error retrieving record types", err)`

Move `roots` above the fan-out; `build` stays `*BuildOption`.

`arx_go/pos.go` `PODetail` (lines 396-428), after `fetchPO`:
```go
var items []models.PurchaseOrderLine
var bulkOrderDelimiter, bulkOrderPNSource string
var history []POHistoryEvent
var receipts []POReceiptView
err := parallel(r.Context(),
	func(ctx context.Context) (err error) { items, err = h.fetchPOItems(r.WithContext(ctx), num); return },
	func(ctx context.Context) error { bulkOrderDelimiter, bulkOrderPNSource = h.fetchSupplierBulkOrderOptions(r.WithContext(ctx), po.SupplierID); return nil },
	func(ctx context.Context) error { history = h.fetchPOHistory(r.WithContext(ctx), po.ID); return nil },
	func(ctx context.Context) error { receipts = h.fetchPOReceipts(r.WithContext(ctx), po.ID); return nil },
)
if err != nil {
	h.renderError(w, r, "Error loading PO items: "+err.Error())
	return
}
```
Then line total, `setNavContext`, etc. in the same order. In `tplData`, use `history` and `receipts`. The `suggest_links=1` block stays sequential (outside the budgeted route, not in the issue).

### H. Budgets: `arx_go/integration_test.go` `TestIntegration_RouteRoundTrips`
Critical path = sequential prefix + longest branch.

| Route (seed) | Today | Derivation after change | New budget | `queries` |
|---|---|---|---|---|
| reports dashboard | 11 | fan-out, longest `dashboardRecentActivity` = 2 | **2** | 11 |
| part detail (3005, serial, no primary attachment) | 11 | `GetPart` 1 + all single-query branches 1 | **2** | 9 (units and price merges) |
| record detail (7001: no lot/build/unit) | 8 | `GetRecord` 1 + branches 1 (trace = `GetPartTracking` only) | **2** | 8 |
| part lot trace (3012/8302) | 8 | part 1 + lot 1 + longest walk 2 (ancestors: 8302, 8301; descendants: 8302, 8306) | **4** | 8 |
| part unit trace (3005/8503) | 7 | part 1 + unit 1 + longest walk 2 (descendants: 8503, 8501; ancestors 1; build 1; types 1) | **4** | 7 |
| PO detail (5002) | 5 | `GetPO` 1 + branches 1 | **2** | 5 |
| part price-history (3002), side effect of UNION | 3 | part 1 + one query | **2** | 2 |

The dashboard's 2 is achievable with no open question. A nested `parallel` in `dashboardRecentActivity` would reach 1, but the issue fixes 2, so it is not nested.

Also update:
- the test's doc comment: "SQL round trips" means critical path; the total is logged
- the `[PROFILE]` log: add `(%d queries)` with `st.queries`
- the `Est. Azure` calculation is unchanged (`count` × 80 ms is the critical path)

`withStats` and the rollup tests read only `st.count` on sequential code: unaffected.

### I. `CHANGELOG.md`
Insert above `## [0.8.93]`:
```markdown
## [0.8.94] - 2026-10-31
### Changed
- Pages with many independent queries load faster: the Reports dashboard, part Summary, record detail, lot trace, unit trace and PO detail now run them concurrently over an explicitly sized connection pool (12 connections, 15-minute idle expiry, 30 s idle ping threshold); round-trip budgets lowered to match, and debug logging shows round trips (critical path) next to total queries ([#426](https://github.com/Jolls/arx/issues/426))
```
Use the actual merge date if it differs. No `RELEASE_NOTES.md` entry (per release, not per patch). `ROADMAP.md` has no #426 item.

## Before/after timing (acceptance criterion)
Temporary and **untracked**, never committed: `arx_go/zz_426_timing_integration_test.go`. Create it before any other edit. Keeping it would make the suite RTT-dependent for no ongoing value (`RouteRoundTrips` guards the count). Its handlers are GET-only and write nothing.
```go
//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"
)

func TestIntegration_426WarmTiming(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	const warmup, samples = 5, 20
	for _, c := range []struct {
		name   string
		fn     http.HandlerFunc
		target string
		id     int
	}{
		{"reports dashboard", h.ReportsDashboard, "/reports", 0},
		{"part detail", h.PartDetail, "/part/{id}", 3005}, // same non-BOM path as RouteRoundTrips
	} {
		var got []time.Duration
		var st *sqlStats
		for i := 0; i < warmup+samples; i++ {
			time.Sleep(2 * time.Second) // navigation gap: over pgx's 1 s default ping, under the new 30 s
			st = &sqlStats{}
			req := httptest.NewRequest(http.MethodGet, c.target, nil)
			if c.id != 0 {
				req = withID(req, c.id)
			}
			req = req.WithContext(context.WithValue(req.Context(), ctxSQLStatsKey, st))
			rec := httptest.NewRecorder()
			start := time.Now()
			c.fn(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status %d", c.name, rec.Code)
			}
			if i >= warmup {
				got = append(got, time.Since(start))
			}
		}
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		t.Logf("[TIMING] %-18s median %s  p90 %s  round trips %d", c.name, got[samples/2].Round(time.Millisecond), got[samples*9/10].Round(time.Millisecond), st.count)
	}
}
```
Run from the repo root against **shared ArxDev** for real RTT, once on unchanged code and once after (reads only, ~1.5 min):
```
ARX_TEST_FROM_CONFIG=1 go test -tags integration ./arx_go/ -run '^TestIntegration_426WarmTiming$' -v -count=1
```
Paste both `[TIMING]` lines in the PR. On the PR, note that a page revisited after more than 30 s idle pays one parallel ping (+1 RTT), and after more than 15 min idle it reconnects.

## Test plan

Commands used below:
- Unit: `go test ./...`. CI already runs `go test -race ./...` (`.github/workflows/test.yml:37`).
- Integration (preferred target; no new fixture or seed data, so no human refresh):
  ```
  docker compose -f SQL/compose.yml up -d --wait db
  ARX_TEST_DSN='postgres://postgres:postgres@127.0.0.1:55432/ArxDev?sslmode=require' go test -tags integration ./arx_go/... -count=1
  docker compose -f SQL/compose.yml restart db   # between runs
  docker compose -f SQL/compose.yml down         # when done
  ```
- Race: this Windows machine has no `gcc` and `-race` needs cgo, so the user runs these in WSL (requires `gcc` and a native Go toolchain):
  ```
  CGO_ENABLED=1 go test -race -count=1 -run 'TestParallel|TestRecordRoundTrip' ./arx_go/
  docker compose -f SQL/compose.yml up -d --wait db
  CGO_ENABLED=1 ARX_TEST_DSN='postgres://postgres:postgres@127.0.0.1:55432/ArxDev?sslmode=require' go test -race -tags integration -count=1 -run 'TestIntegration_(RouteRoundTrips|ParallelPages)' ./arx_go/
  ```

### 1. Coverage audit (existing)
- Budgets and render: `arx_go/integration_test.go` `TestIntegration_RouteRoundTrips`
- Dashboard: `integration_test.go` `TestIntegration_ReportsDashboard_RendersAllCards`, `..._DashboardStaleWIPRecords`, `..._DashboardPendingApprovalPOs`, `..._DashboardBelowReorderParts`; `reports_integration_test.go` `TestIntegration_DashboardPOCounts`, `..._DashboardFailureModesAndYield`, `..._DashboardRecentActivity`; `dashboard_recent_integration_test.go` `TestIntegration_DashboardRecentLots`, `..._DashboardRecentBuilds`
- Part page:
  - `part_detail_integration_test.go` `TestIntegration_PartDetail_Renders`
  - `part_orders_integration_test.go` `TestIntegration_PartDashboardCards` (price point order and nil), `TestIntegration_PartDetail_AttachmentsAndPrice`
  - `part_summary_details_integration_test.go` `TestIntegration_PartDetail_Characterization_Dashboard`, `..._Characterization_BOMRedirect`, and the summary tests
  - `part_traceability_integration_test.go` `TestIntegration_PartTraceabilityCard`, `..._Hidden`
  - `inventory_integration_test.go` `TestIntegration_InventoryPartCards`
  - `popout_integration_test.go` (part header and units)
  - `categories_integration_test.go:366` (tab gating)
- Record detail: `records_reads_integration_test.go` `TestIntegration_RecordsReads_RecordDetail`, `..._RecordDetailNullColumns`; `cross_links_integration_test.go:46,60`
- Lot/unit trace: `integration_test.go` `TestIntegration_PartLotTrace` and `TestIntegration_PartUnitTraceHandler` (bad id, wrong part, missing, tab gate); `cross_links_integration_test.go` `TestIntegration_LotTraceListsUnits`, `..._LotTraceLinksTransactions`
- PO detail: `cross_links_integration_test.go` `TestIntegration_PODetailReceiptLinksLot`; `suppliers_sqlc_integration_test.go:369,381`
- `db`: `internal/db/readonly_test.go`, `connerr_test.go`
- **Gap:** per-query failure behaviour, thumbnail on Summary, rendered counts, cancellation.

**Edit required by section D:** `TestIntegration_InventoryPartCards` (`inventory_integration_test.go:266-284`), as part of its red change below.

### 2. Characterization tests (pass on unchanged code)
New file `arx_go/parallel_pages_integration_test.go` (`//go:build integration`).

Fault helper:
- `type faultConn struct { *stdlib.Conn; failOn string }` overrides `QueryContext` and `ExecContext`: when `strings.Contains(query, "-- name: "+failOn+" ")` return `errInjected = errors.New("injected fault")`, otherwise delegate. Every sqlc query starts with that comment, and the trailing space stops `GetPart` matching `GetPartBasic`.
- `faultConnector{inner driver.Connector; failOn}` wraps `stdlib.GetConnector(*cfg)`; `Connect` type-asserts `*stdlib.Conn`; `Driver()` delegates.
- `faultHandler(t, failOn) (*Handler, func())`: `liveHandler(t)`, then `sql.OpenDB(faultConnector{...})` on `os.Getenv("ARX_TEST_DSN")`, then `h.update(func(s *runtimeState){ s.conn = &dbConn{db: faulty} })`. Cleanup closes `faulty`, restores the original conn, calls `done()`.

No knowingly locked-in bugs. Tests:

- **`TestIntegration_ParallelPages_ErrorBehaviour`**, table `{page, failOn, status, want[], absent[]}`; every row uses a new `faultHandler`:
  - Dashboard: each of `CountOpenPOs`, `CountPOsReceivedThisMonth`, `ListRecentModifiedParts`, `ListRecentPOEvents`, `ListTopFailureModes`, `ListRecordFailFlags`, `ListStaleWIPRecords`, `ListPendingApprovalPOs`, `ListBelowReorderParts`, `ListLots`, `ListRecentBuilds`: 200, want `Error loading dashboard: injected fault`, absent `Recent Activity`.
  - Part 3005 at `/part/3005/summary`:
    - `GetPart`: 200, want `Error retrieving part: injected fault`.
    - Tolerant rows, each 200, want `ASM-1001`, absent `injected fault`: `ListPartAttachments`, `PreferredSupplierMinPrice`, `ListPartOrders`, `ListRecentPartTxns`, `ListRecentPartUnits`, `CountPartUnits`, `CountPartBuilds`, `GetPreferredSupplier`, `ListPOPricePoints`, `ListPriceListPoints`.
    - `ListPartOrders` additionally wants `SN-3005-001`: siblings aren't cancelled.
  - Part 3007 at `/part/3007/summary`, tolerant: `ListLots`, `CountPartLots`.
  - Lot trace 3012/8302, each 200: `GetPartBasic` `Error retrieving part:`; `GetLot` `Error retrieving lot:`; `ListTraceAncestors` `Error tracing lot ancestry:`; `ListTraceDescendants` `Error tracing lot descendants:`; `ListRecordTypes` `Error retrieving record types:`; `ListLotUnits` `Error retrieving lot units:`.
  - Unit trace 3005/8503, each 200: `GetUnit` `Error retrieving unit:`; `GetBuild` `Error retrieving build:`; `ListTraceAncestors` `Error tracing unit ancestry:`; `ListTraceDescendants` `Error tracing unit descendants:`; `ListRecordTypes` `Error retrieving record types:`.
  - PO 5002: `GetPO` `Error retrieving purchase order:`; `ListPOLines` `Error loading PO items:`; tolerant, want `PO #5002`, absent `injected fault`: `GetSupplierBulkOrder`, `ListPOHistory`, `ListPOReceipts`.
  - Record 7001: `GetRecord`, `GetFormHeader`, `ListRecordResults`, `ListFormSteps`, `GetPartTracking`: 500, want `query error`. Tolerant 200, absent `query error`: `GetRecordNeighbors`, `ListRecordEvents`, `ListRecordEventResults`.
  - **Rename in section D's commit:**
    - `ListPOPricePoints` and `ListPriceListPoints` become one `ListPartPricePoints` row
    - remove `CountPartUnits` and `CountPartLots`
    - the 3007 `ListLots` row becomes `ListRecentPartLots`
    - the dashboard `ListLots` row stays
- **`TestIntegration_ParallelPages_NotFound`**: `PartDetail` on id 999999999 gives `Part not found`; `PODetail` on 999999999 gives `Purchase order not found`; `RecordDetail` gives 404 (trace not-found rows are already covered).
- **`TestIntegration_ParallelPages_CanceledRequest`**: `ReportsDashboard` with an already-cancelled request context gives 200 and `Error loading dashboard: context canceled`.
- **`TestIntegration_ParallelPages_OneConnection`**: `h.database().SetMaxOpenConns(1)`; call all six pages under a 30 s request context; each gives 200 and its table marker. Pins no self-starvation.
- **`TestIntegration_PartSummary_RenderedData`**:
  - 3005 `/summary`: `<strong>Total units:</strong><span>2</span>`, `SN-3005-001`
  - 3007 `/summary`: `<strong>Total lots:</strong><span>2</span>`
  - 3002 `/summary`: `window.PRICE_HISTORY_DASH`
  - `seedPartDetail`'s `full` `/summary`: `data-popout-url="/popout/part/<full>/thumb"` (thumbnail written from the attachments loop)
- `internal/db/pool_test.go` **`TestConnect_Errors`** (no DB; port 1 unreachable):
  - `Connect("postgres://u:SECRETPW@127.0.0.1:1/x?sslmode=require")`: error has prefix `ping: `, doesn't contain `SECRETPW`
  - `Connect("postgres://u:SECRETPW@h:badport/x")`: non-nil error, doesn't contain `SECRETPW` (prefix not asserted; it changes from `ping:` to `open:`)

### 3. Red tests (fail on current code, pass after)
Stubs so these compile and fail on assertions:
- `sqlStats.queries int` field (not incremented)
- `func parallel(ctx context.Context, branches ...func(context.Context) error) error` running sequentially, stopping at the first error, with no stats fold
- `func shouldPing(context.Context, stdlib.ShouldPingParams) bool { return false }`
- `func openPool(dsn string) (*sql.DB, error) { return sql.Open("pgx", dsn) }`
- `recentPartLots`/`recentPartUnits` and service methods returning a third/second `int` value of 0

`arx_go/parallel_test.go` (no build tag; simulates a query with `recordRoundTrip(ctx, "q", time.Millisecond)`):

| Test | Asserts | Why it fails today |
|---|---|---|
| `TestRecordRoundTrip_CountsQueries` | One call gives `count==1, queries==1` | `queries` never incremented |
| `TestParallel_CountsCriticalPath` | Parent seeded with 1 query; branches of 1, 3, 2 give `count==4` (1+3), `queries==7`, `total==7ms` | Stub folds nothing and branches count on parent (`count==7`) |
| `TestParallel_Nested` | Branch A: 1 query, nested (2, 1), 1 query; branch B: 3. Gives `count==4`, `queries==8` | Same |
| `TestParallel_NoStatsInContext` | Without stats, branches see `ctx.Value(ctxSQLStatsKey)==nil`; returns nil | Passes with stub; guards the debug-off path (not red) |
| `TestParallel_RunsConcurrently` | 3 branches wait on a shared barrier (5 s timeout returns an error); returns nil | Sequential stub times out |
| `TestParallel_ErrorDoesNotCancelSiblings` | Branch 1 waits for branch 2 to start (1 s timeout) and returns `errBoom`; branch 2 then waits 50 ms and finishes with `ctx.Err()==nil`; result `errors.Is(errBoom)` and branch 2 finished | Sequential stub stops at branch 1's error, so branch 2 never runs |
| `TestParallel_ParentCancelReachesBranches` | A branch blocks on `<-ctx.Done()` and returns `ctx.Err()`; the test cancels the parent after the branch starts; result `errors.Is(context.Canceled)` | Passes with stub; guards that branch contexts derive from the parent (not red) |
| `TestParallel_RepanicsOnCaller` | A panicking branch does not crash the process; `parallel` panics with the same value on the caller; siblings finished | Stub panics before siblings run, so "siblings finished" fails |

`internal/db/pool_test.go`:

| Test | Asserts | Why it fails today |
|---|---|---|
| `TestOpenPool_Limits` | `openPool` on an unreachable DSN doesn't dial; `Stats().MaxOpenConnections==12` | Stub leaves 0 (unlimited) |
| `TestShouldPing_Threshold` | 29 s false, 30 s false, 31 s true | Stub always false |

`arx_go/parallel_pages_integration_test.go`:

| Test | Asserts | Why it fails today |
|---|---|---|
| `TestIntegration_ParallelPages_Stats` | Exact `(count, queries)` per section H table, for the six pages | Sequential `count` equals today's totals |
| `TestIntegration_PoolKeepsTwelveIdle` | 12 goroutines `SELECT pg_sleep(0.2)` on `h.database()`, then poll up to 2 s: `Stats().Idle==12`, `Stats().MaxOpenConnections==12` | Default idle is 2 and max is 0 |

Existing tests changed to red:
- `TestIntegration_RouteRoundTrips`: new budgets from section H. Fails today because counts exceed budgets.
- `TestIntegration_InventoryPartCards`:
  - `lots, n, err := h.recentPartLots(ctx, 3007, 1)` wants one row and `n==2` (window before `LIMIT`)
  - `units, n, err := h.recentPartUnits(ctx, 3005, 1)` wants one row and `n==2`
  - `recentPartLots(ctx, 3005, 5)` and `recentPartUnits(ctx, 3002, 5)` want empty and `n==0`
  - Delete the `lotCountForPart`/`unitCountForPart` assertions. Fails on the stub's 0 count; the empty cases pass.

`TestIntegration_PartDashboardCards` needs no edit; it must stay green across the UNION (same order, nil when empty).

### 4. Manual-only
- `SetConnMaxIdleTime(15 min)`: inspect `openPool`, no practical test (15 min wait; no getter).
- `OptionShouldPing` wiring: `shouldPing` is unit tested, its attachment isn't observable. Inspect, and compare timing.
- Warm before/after timing against shared ArxDev; paste results on the PR.
- WSL `-race` runs above: user runs them.
- With `DEBUG_MODE=true`, open `/reports` and a part page: `[PERF]` and `[PERF SUMMARY]` show `round trips (queries)`, e.g. dashboard `2 round trips (11 queries)`.
- Browse the six pages and a PO save and record save with BOM: no visible change.

## Resolved decisions
- **Q1. Transaction + pool reads (Hazard 1 list).** No edits in this PR; tracked in a separate low-severity follow-up issue.
- **Q2. `errgroup` vs code-order errors.** Zero-value `errgroup.Group`. With two or more simultaneous failures on lot/unit trace, the message shown is the first in time.
- **Q3. Merged-query failure scope.** Accepted. A failed price query loses PO and price-list points together; a failed recent lots/units query loses the list and count together. The page still renders with empty cards.
