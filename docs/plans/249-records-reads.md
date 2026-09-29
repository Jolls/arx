# #249 / #223: records reads → sqlc (PR 3 of 4)

Part of #249, #223 / #190. One slice: the **read** paths of the records domain. Writes (save / lock /
approve / unlock / duplicate / events / form CRUD) are PR 4 (they hold the record and part locks, #191).
`named_query*.go` untouched (#250 decision is PR 4).

## Site classification (raw statements at b93f160; the grep's 172 hits in records.go are multi-line
fragments and writes)

**Read → convert (records.go, ~46 statements):**
`FormsList`; `RecordsList` (form header, type options); `RecordsRows`; `scopedRecordsRows` /
`scopedRecordTypeOptions` (Part/Lot/Unit tables); `FormDef` (header, steps, history stamps);
`FormDefHistory`; `EditFormDef` (header, steps); `loadSteps`; `loadRecordResults`; `RecordDetail`
(record, form header, prev/next, audit events); `RecordPrint` (record, form header); `NewRecord`
(header, BOM parts, next serial); `loadRecordTrace` (part tracking + BOM count); `EditRecord`
(record, form header); `formPNList`; `NewForm` (source forms); `DuplicateForm` (header, step count);
`TestReport` (header, step); `TestReportRows` (step format, rows).
Also: `records_history.go` `loadEventSnapshots` (1), `records_yield.go` (2), `records_failure_modes.go` (2).

**Write / tx → left for PR 4:** `CreateRecord` (incl. its pre-tx form/BOM-part lookups, the serial
allocation and insert), `materializeRecordSteps`, `Lock/Approve/Unlock/BulkLock`, `DuplicateRecord`
(its reads sit in the write flow), `SaveResults` (guard, lock, step load, existing-results load, inserts),
`ResyncRecord`, `Lock/UnlockForm`, `ArchiveStep`, `SaveFormDef`, `CreateForm`, `CreateDuplicate`,
`copyFormSteps`, `setAuditUser`; in `records_history.go`: `snapshotRecordResults`, `completeRecordTx`,
`logCompletionSnapshot`. No read path uses `FOR UPDATE`, an advisory lock or a tx.

## Files that can join `convertedFiles`
- `records_yield.go`, `records_failure_modes.go`: every raw site moves → join.
- `records.go`, `records_history.go`: still hold writes/tx reads → **cannot join in this PR** (PR 4). No
  lint exemption added.

## Flat-query decisions
- Form header + part: one `GetFormHeader` (all columns: revision, record_types, instrument_types; `test_order`
  COALESCE'd) replaces 8 near-identical statements. Handlers copy back only the fields each one loaded before.
- Steps: one `ListFormSteps` (full column set) replaces `loadSteps`, `FormDef`, `EditFormDef` (the duplicated
  step-loading query of #223; `SaveResults`' copy joins it in PR 4).
- Record row (detail/print/edit): one `GetRecord` with lot/build/unit (extra columns harmless to print).
- Scoped record tables: one `ListScopedRecords` / `ListScopedRecordTypes` taking three nullable ids
  (`part_id` / `lot_id` / `unit_id`) via `sqlc.narg`; the service takes a fixed `Scope` enum, exactly one id is
  ever set, so the old `whereCol` string splice is gone.
- Yield / failure-modes date range: `record_date >= from` / `< to::date + 1` become
  `(narg IS NULL OR ...)` flat clauses; status/type filters are not used by these two reports
  (`dateRangeClauses` only), so `recordFilters.whereClauses` keeps no reads to convert.
- Ordering `serialIntExpr(...) DESC, record_date DESC` written out literally (NULLs first, as before).
- Prev/next: the same `LAG/LEAD` CTE; error ignored in the handler as before.

## Coverage audit (before)
Covered: `FormsList`, `RecordsYieldSummary` (with data, integration_test.go), `FormDef`/`EditFormDef` on seed
form 6001, `FormDefHistory` (pre-change snapshot, both branches), the lock lifecycle (writes).
Gaps → new `arx_go/records_reads_integration_test.go`, all passing on the unchanged code first:
`RecordsList` + type options, `RecordsRows` (order, status, form-rev label, inactive excluded, NULL columns),
`PartRecordsRows`/`LotRecordsRows`, `RecordDetail` (rows, prev/next, audit events, snapshots, trace),
`RecordPrint`, `EditRecord` (locked redirect, trace pickers), `NewRecord` (BOM parts, next serial),
`TestReport` / `TestReportRows`, `RecordsFailureModes` (+ date range, NULL parameter), yield date range,
`NewForm` / `DuplicateForm` pickers, missing-id 404s.

Not too big for one PR (~56 statements, no tx code); no split.

## Helpers
The `cfg.*Table()` helpers still have unconverted callers (records writes, and ~90 test sites). Deletion waits
for PR 4.

Verify: go build/vet (+ `-tags integration`), go test ./..., sqlc diff, gofmt -l, live ArxDev integration run,
then rerun `QueryDataQualityParts` / `ReportsDataQualityMissingSupplierExportCSV`.
