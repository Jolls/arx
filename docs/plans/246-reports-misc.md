# #246 / #248 / #225: reports + utilities + misc handlers → sqlc

Part of #246, #248, #225 / #190. One slice: every raw-SQL site in `reports.go`, `utilities.go`,
`handlers.go`, `api.go` (~40 sites, ~36 statements).

## Scope (raw sites)
- `reports.go` (22): dashboard cards (open PO count, received-this-month, top failure modes, lowest
  yield, stale WIP, pending approval, below reorder, recent parts + recent PO events), spend by
  supplier / by part, on-time delivery, PO cycle time, data-quality parts (3 checks over one
  string-built `where`), active form picker.
- `utilities.go` (8): dead-link scans (part + company attachments), orphan pointers (3 string-built
  column/table variants), soft-deleted primary-attachment pointers (part + company), PO `is_active`
  drift.
- `handlers.go` (4): `fetchUnits` (uom), `appConfigGet`, `appConfigSet`, and the
  `CheckSchemaVersion` wrapper (the SQL itself sits in `internal/config.CheckSchemaVersion`).
- `api.go` (1): `APIRecordPasteResultImage` record header lookup.

## Where things live
- **`internal/reports`** (`reports.sql` + `Service`): every dashboard/report/utility-check query,
  including the single-domain ones (open PO count, spend by supplier, data-quality parts, …).
  #246/#225 ask for one reports service; keeping report-shaped reads together beats scattering
  them over purchasing/parts. Domain packages get only non-report reads.
- **`internal/parts`**: `ListUOMs` (the uom reference list feeds part/supplier dropdowns).
- **`internal/appconfig`** (new, tiny): `app_config` key/value get/upsert. Infrastructure, not a
  domain; `settings.go` (PR 2) will call the same service instead of the handler wrappers.
  `config.CheckSchemaVersion` stops taking a raw `queryRow` + table name and takes a
  `get func(ctx, key) (string, error)` (its only caller is the handler wrapper), so its SQL goes
  through `appconfig`. Error text is unchanged.
- **`internal/records`** (new, one query): the paste-image record header lookup. Single-domain
  (records), so it can't go in reports; PR 3 grows this package.
- `handlerDB` itself is infrastructure (the DBTX adapter); no SQL, nothing to move.

## Flat-query decisions (nothing needs a dynamic alternative except as noted)
- Date-range `whereClause` (string-built `AND col >= $n`): replaced by two nullable params per
  query, `(sqlc.narg(date_from)::date IS NULL OR po.date_ordered >= sqlc.narg(date_from)::date)`.
  The inclusive-To → exclusive-next-day bump stays in the service. Cycle time filters the
  timestamptz `entered_at`, so it uses `::timestamptz` params. Zero `From`/`To` → NULL → no bound.
- `queryDataQualityParts(where)` → three flat queries (`ListPartsNoAttachments`,
  `ListPartsMissingDefaultSupplier`, `ListPartsStaleRollup`) sharing one row type.
- `checkOrphanPointers` (column/table interpolated) → three flat queries. Note the three FKs are
  enforced now (#735), so the check can never return rows; kept as-is (not my call to delete).
- Numeric sums/averages cast `::float8`, counts `::int`, so Go types match the current scans.
- Statement order/tx boundaries: none of these sites use a tx. `dashboardRecentActivity` keeps its
  two queries + Go merge/sort.

## Coverage audit (before)
Covered (integration_test.go): spend by supplier/part, on-time, cycle time, data-quality parts and
all report handlers/CSV exports — but only with a zero (unbounded) date range; stale WIP, pending
approval, below reorder, dashboard render (headings only), utility checks (run-without-error only),
paste-image guards/write.
Gaps → new `arx_go/reports_integration_test.go` (all pass on the unchanged code first):
- Date-range bounds on spend/on-time/cycle time (inclusive To, From/To exclusion)
- `dashboardOpenPOCount`, `dashboardPOsReceivedThisMonth` (deltas from fixtures),
  `dashboardTopFailureModes`, `dashboardLowestYieldForms`, `dashboardRecentActivity`
- `loadActiveFormOptions` + both pickers
- Utility checks' content: dead links (part + company), soft-deleted pointers (part + company),
  PO drift, orphan pointers (empty, FK-enforced)
- `fetchUnits`, `appConfigGet`/`appConfigGetOr`/`appConfigSet` (upsert, missing key),
  `CheckSchemaVersion`

Not too big for one PR; no split.

## Helpers
`cfg.*Table()` helpers that lose their last non-test caller are deleted (tests switch to the
literal name); grep decides after conversion.

Verify: go build/vet (+ `-tags integration`), go test ./..., sqlc diff, gofmt -l, live ArxDev
integration run, then rerun `QueryDataQualityParts` /
`ReportsDataQualityMissingSupplierExportCSV`.

## Outcome / deviations
- Real counts at d097bdd: reports 22, utilities 8, handlers 4, api 1 (36 statements incl. the
  `CheckSchemaVersion` one), plus the string-built `where`/column variants (3 + 3 statements).
- `ListPendingApprovalPOs`: sqlc types a nullable aggregate/lateral column as `interface{}` (a
  `::timestamptz` cast makes it non-null), so the query returns `submitted_at` (epoch when absent) plus a
  `has_submitted` flag and the service turns that into `*time.Time`.
- Bugs pinned then flipped (`### Fixed`): the PO `is_active` drift check aborted on a NULL
  status/is_active PO (row Scan into `string`/`bool`); `loadActiveFormOptions` silently dropped a FORM
  part with a NULL description (swallowed Scan error).
- `handlers.go` stays out of `convertedFiles`: its only remaining hits are the `handlerDB`/`txLogger`
  logging wrappers, which must call `db.QueryContext` etc. `reports.go`, `utilities.go`, `api.go` joined.
- No `cfg.*Table()` helper lost its last non-test caller (the rest are `settings.go` backup list /
  unconverted domains), so none were deleted. `LinksTable()` has no caller at all (pre-existing, left).
