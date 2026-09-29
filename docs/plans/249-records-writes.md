# #249 / #223 / #190: records writes → sqlc (PR 4 of 4)

Part of #249, #223 / #190. One slice: every remaining raw site in `records.go` (~50 statements; the grep's
53 hits include multi-line fragments) and `records_history.go` (5), plus the closing sweep (`named_query*.go`,
`cfg.*Table()` deletion, CLAUDE.md cleanup). Reads were PR 3 (`internal/records`, 25 queries, `Service`).

Not too big for one PR: the statements are flat INSERT/UPDATE/`:execrows`; risk is concentrated in three
flows (SaveResults, completeRecordTx, CreateRecord) whose tx shape and lock order stay byte-for-byte the same.
No split proposed.

## Lock / tx order (must not change; #191)

| Flow | Order inside the tx |
|---|---|
| `SaveResults` | `UPDATE form_record SET updated_at WHERE id AND NOT is_locked` (record row lock, the claim) → read record → form header → steps → existing results → result UPDATE/INSERTs → part tracking read → optional `performBuild` (inventory: part / lot / stock locks) → `appendLotNote` → `upsertUnitForRecord` → `UPDATE form_record` → commit |
| `completeRecordTx` | `UPDATE form_record SET is_locked WHERE id AND NOT is_locked [AND form_id]` (record row lock) → on rows>0: INSERT `record_events` → read `test_order` → read results → INSERT `record_event_results` × n → commit |
| `CreateRecord` | (pool: form header, BOM-part lookup) → tx: `pg_advisory_xact_lock(369, form_id)` only for an auto serial → `MAX(serial)+1` → INSERT `form_record` → materialize `result` rows → commit |
| `ResyncRecord` | pool reads (record, form, steps, results) → tx: result UPDATE/INSERT per step → `UPDATE form_record` → commit (pre-existing: the `is_locked` check is not re-done in the tx; fixed after review, see below) |
| `DuplicateRecord` | pool read of source → tx: form revision → INSERT `form_record` → copy `result` rows → commit |
| `SaveFormDef` / `ArchiveStep` | tx: `set_config('arx.username')` (history trigger) → UPDATEs/INSERTs → commit; then (outside the tx) `test_order` / `record_types` updates |
| `CreateForm` / `CreateDuplicate` | (pool: part check, source types) → tx: INSERT `form` → copy steps + `UPDATE form.test_order` → commit |
| Approve / Unlock / LockForm / UnlockForm | **no tx today**: guarded UPDATE, then the event INSERT only when rows>0. Moved into one tx after review, see below |

Rule for the conversion: a method that ran inside the caller's tx becomes `records.New(tx).X(...)` at the
same point in the sequence; a method that ran on the pool stays on the pool. Handlers keep owning `beginTx`.

## Site classification

**Convert to `internal/records` (flat queries):**
- `InsertRecord`, `NextFormSerial` (reuse; identical to the string-built `MAX(serialIntExpr)+1`), advisory lock
  (`LockSerialAlloc(formID)`, namespace passed by the handler: keeps `serialAllocLockNS` in one place).
- `InsertResultFromStep` (one insert for materialize / resync-new-row / SaveResults legacy path: same column list),
  `UpdateResultSnapshot` (resync), `UpdateResultValue` (SaveResults), `UpdateRecordAfterSave` (record_date
  optional via `COALESCE(narg, record_date)`), `UpdateRecordResync`, `ClaimRecord` (`:execrows`).
- `CompleteRecord` (`:execrows`, `form_id` optional narg), `ApproveRecord`, `UnlockRecord` (`:execrows`),
  `InsertRecordEvent` (comments narg), `InsertEventResult`; snapshot reads reuse `GetRecord` + `ListResults`.
- Forms: `LockForm`, `UnlockForm` (`:execrows`), `InsertFormEvent`, `UpdateStep`, `InsertStep`,
  `SetStepArchived`, `SetFormTestOrder`, `SetFormTypes`, `IsActiveFormPart` (replaces `COUNT(1)`), `InsertForm`
  (source's `record_types`/`instrument_types` via scalar subqueries, keeps NULL as NULL, drops the
  ignored-error pre-read), `CopyStep` (`INSERT … SELECT … RETURNING id` per source step: preserves NULLs
  exactly, replaces the hand-scanned 18-column stepRow struct), `GetPartLabel` (BOM subject part).
- `DuplicateRecord`: `InsertDuplicateRecord` = `INSERT … SELECT` from the source row (fresh date, current
  form revision via subquery, unit carried over) + `CopyResults` = `INSERT … SELECT` (replaces the
  scan-and-reinsert loop). Row order of the copy was never defined (no ORDER BY), so nothing observable
  changes except that NULL source columns stop 500ing (see Bugs).
- `setAuditUser` → `records.SetAuditUser(ctx, tx-backed service, username)`; `integration_test.go` keeps
  calling `h.setAuditUser`.
- `loadSteps` / `loadRecordResults` take the `*records.Service` to read on, so `materializeRecordSteps`,
  `SaveResults` and `ResyncRecord` use the tx where they ran in the tx (their duplicated step-loading
  query is gone: #223's "one named query" = `ListFormSteps`).
- Reads inside write flows use existing `GetRecord`, `GetFormHeader`, `ListFormSteps`, `ListResults`,
  `GetPartTracking`.

**Leave raw (needs a decision, see below):** `named_query.go` (3), `named_query_settings.go` (3).

**Helper deletion:** after conversion no non-test caller remains for Forms/Records/Steps/Results/
RecordEvents/RecordEventResults/FormRowHistory/FormEvents/Parts/BOM/NamedQueries (if #250 converts CRUD).
`LinksTable` already has no caller at all (dead now). `AttachmentsTable`/`CompanyAttachmentsTable` still have
non-test callers in `arx_go/cmd/backfill_attachment_hash/main.go` (decision below).

## Bugs expected (pin first on unchanged code, then fix, report under ### Fixed)
Same NULL-scan class as PR 3: `CreateRecord` scans nullable `form.test_order` and `part.description` into
plain strings; `DuplicateRecord` / `SaveResults` / `ResyncRecord` scan nullable `serial_number`,
`subject_part_number`, `subject_pn_description`, `record_type`, `test_order` into plain strings. A NULL there
= 500. Also `SaveResults`' private step query omits `format`, so the legacy first-value insert stores
`result.format = ''` instead of the step's format (fixed for free by `ListFormSteps`). Tests pin each first.

## Coverage audit (before)
Covered: `CreateRecord` auto serial + concurrent distinct serials; lock/approve/unlock lifecycle with
permissions, events and snapshot counts; bulk lock; lot-tracked lock; `SaveResults` happy path, locked writes
nothing, 404, mid-tx failure rollback, lock-race.
Gaps → new `arx_go/records_writes_integration_test.go` (all PASS on unchanged code first):
`CreateRecord` (typed serial as-is, BOM subject part, materialize skips archived / non-applicable instrument
type, headings kept, `form_revision` captured, missing form 404); `SaveResults` (P/F computed, unchanged
result not rewritten, legacy insert path, record metadata + `record_date` optional, snapshot P/F from
frozen spec); `ResyncRecord` (refresh fields, new step materialized, order/revision, locked redirect);
`DuplicateRecord` (fields + unit carried, results copied, fresh date/current revision, 404);
`LockForm`/`UnlockForm` (revision bump once, events, comment required, already-locked no-op);
`ArchiveStep` (toggle + history attribution); `SaveFormDef` (unchanged skip, update with NULLs, new rows,
order token substitution, types); `CreateForm` (blank, copy from source with NULL types, invalid PN 400);
`CreateDuplicate`; `copyFormSteps` order + NULL preservation; the NULL-column bugs above; `completeRecordTx`
snapshot ordering by `test_order`. Race coverage stays in `tx_boundaries_integration_test.go`.

## Files that can join `convertedFiles`
`records.go` and `records_history.go` join (every raw site moved, no lint exemption). `records_filters.go`'s
test-only `dateRangeClauses` / `whereClauses` are deleted with their two inline-SQL tests.

## Decisions (user, 2026-09-28)
- #250 → (a): the `named_queries` CRUD (list, active list, lookup, insert, update) is in `internal/records`;
  `execQuery` keeps its one raw call, allowed by name in the lint. Recorded in `docs/conventions.md`.
- Test churn: every `cfg.*Table()` reference in tests → literal (`replace_all` per file, Sprintf wrappers kept);
  every helper deleted. `cmd/backfill_attachment_hash` uses literals.
- `loadSteps` takes the Service to read on: `materializeRecordSteps` and `SaveResults` read steps on their tx
  (READ COMMITTED sees the same rows; one connection instead of two), `ResyncRecord` and the read pages on the pool.
- `DuplicateRecord` drops its pool pre-read: `INSERT … SELECT … JOIN form` returns no row → 404. A record
  whose form row is gone (no FK on `form_record.form_id`) now 404s instead of 500.

## Review fixes (user: "fix the issues", 2026-09-28)
Pre-existing defects `/code-review high` found, each with a regression test that failed first:
- `ResyncRecord` now claims the record (`ClaimRecord`) at the start of its tx and reads record, form, steps
  and results on the tx: lock order record row → writes, same as `SaveResults`.
- `UnlockRecord` query re-checks approval in its WHERE (`may_unlock_approved`); the pool pre-read only
  picks the 403.
- Approve / Unlock / LockForm / UnlockForm: state change + event in one tx (`recordsTx`, like
  `attachmentTx`).
- `SaveFormDef`: test_order and types updates move into the step tx; the order read's error is no longer
  dropped.
- `CreateRecord`: only `sql.ErrNoRows` on the subject-part lookup means "no part"; other errors 500.
- Lint: deny by default over every non-test `arx_go/*.go`; exemptions `handlers.go` + the `execQuery` line.

## Verify
go build/vet (+ `-tags integration`), `go test ./...`, `sqlc diff`, `gofmt -l` (no new hunks), live ArxDev
integration run, race tests, then rerun `QueryDataQualityParts` / `ReportsDataQualityMissingSupplierExportCSV`.
