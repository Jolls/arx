# Test coverage gap audit (2026-07-24)

Plan only — no tests written. For a future (lower-effort) session to build out. Ranked by
severity using the project's `sev:` label scale. `#758` (auth/user-admin/sanitizeLandingRoute/
templates) already closed a narrow slice of the auth surface — items below are what's left
after that, not a duplicate of it.

Current state: `go build/vet/test ./...` is green in both modules with no live DB. All gaps
below are either (a) untestable without the `integration` build tag + live ArxDev, or (b) pure
functions that could be unit-tested today but simply aren't.

## Critical

- **Auth middleware composition never assembled in any test.** `buildRouter` (main.go) — the
  real chi route table wiring `RequireAuth`/CSRF onto ~185 routes — is never constructed by any
  test; `integration_test.go` calls handlers directly, bypassing the whole chain. A route could
  ship unauthenticated and nothing would catch it. Also: `RequireAuth`'s two DB-connected
  branches (anonymous→redirect, logged-in→proceeds) are untested (only no-DB/schema-mismatch
  are); login lockout (`loginBlocked`/`noteLoginFail`/`noteLoginOK`, #757) and
  `sessionIdleTimeout` are zero-coverage.
- **PO receiving & status transitions** (`pos.go`) — only the pure decision tables
  (`poCanTransition`, `derivePOReceiptStatus`, etc.) are tested; the handlers that actually write
  are zero-coverage: `POReceive` (creates `inventory_transaction` rows + lots on receipt),
  `POStatusTransition`/`recordPOStatusChange`, `POApprovalAction`.
- **`rollupCost`/`PartRollupCost`** (parts.go) — the original recursive BOM-cost-rollup that
  writes `last_rollup_cost`/`last_rollup_at` back to every visited assembly, including its own
  cycle handling. Zero coverage (only the newer `buildCost` qty-break path is tested).
- **`SettingsSave`** (settings.go) — swaps the live DB connection, persists secrets, re-runs
  schema check. Zero coverage of success/failure/test-mode-swap branches; a bug here can
  silently corrupt config/secrets or leave the app pointed at the wrong DB.
- **Record lock/approve state machine** (records.go) — `LockRecord`/`ApproveRecord`/
  `UnlockRecord`/`BulkLockRecords` and the completion-audit chain (`completeRecordTx` →
  `logCompletionSnapshot` → `snapshotRecordResults`) are entirely unexercised — the whole
  WIP/Complete/Approved compliance sign-off path.
- **Named-query runtime execution** (`runNamedQuery`/`execQuery`, named_query.go) — zero
  coverage of the exact path that already broke ArxProd once (`spec_nom` went stale after a
  param rename — see `migrate_max_subbatch_result_param_rename.sql`). Parsing is well tested;
  execution against a real named query isn't.

## High

- **Lot handlers & manual stock adjustment** — `PartLots`/`PartLotTrace`/`LotEdit`/`LotUpdate`/
  `AllLots` (lot.go) and `PartStockAdjust` (inventory.go, incl. lot picking) — zero coverage,
  direct or indirect.
- **Attachment resolution/delete/primary logic** — `resolveAttachmentFileInput`,
  `deleteAttachmentFileIfUnshared`, `setPrimaryAttachment` (attachments.go) — zero coverage; risk
  of orphaning a shared file or losing the primary-attachment pointer.
- **`config.Load()` precedence chain** (arxlib/config/config.go) — the documented
  .env→env→local.json→secrets override order is never exercised end-to-end; each piece is
  unit-tested in isolation only.
- **`ComputePassFail`/`CalcPF`/`EffectiveValue`/`EffectiveSpec`** (models/trmodels.go) — pure,
  cheaply table-testable, and it's the core PASS/FAIL determination for every test result. Zero
  tests today despite no DB dependency.
- **RFQ award/convert flow** — `RFQNew`/`RFQAddSupplier`/`RFQCompare`/`RFQCompareSave`/
  `RFQConvert` (pos.go) — the entire multi-supplier quote-to-PO workflow, zero coverage.
- **Form definition CRUD + history** — `SaveFormDef`/`EditFormDef`/`ArchiveStep`/
  `FormDefHistory` (records.go) — zero coverage; governs what a locked historical record renders.
- **`unit.go` read/trace handlers** — `PartUnits`/`PartUnitTrace` and their helpers are never
  called directly by any test (writes are exercised only indirectly via `records.go`).

## Medium

- **Reports family beyond the 3 dashboard cards** — Spend/OnTime/CycleTime/DataQuality queries +
  their CSV exports (reports.go) — read-only, wrong output is visible not destructive, but
  currently untested at the query level.
- **`ReportsDashboard` page handler assembly** — wiring only, low complexity.
- Contacts/Suppliers/MfgParts/SupplierPart **edit/update/detail** handlers — CRUD,
  pattern-following; only the create-paths are smoke-tested.
- PDF thumbnail rendering (`renderPDFFirstPage`/`getPdfiumPool`/`encodePNG`, pdfthumb.go) —
  cgo-adjacent, visual-only impact.
- `migrateLegacy` (arxlib/config/local.go) — legacy two-app-config merge, presumably low
  remaining usage.
- `SettingsCategoriesSave` (categories.go) — config UI, low risk.
- `api.go` attachment-paste/thumbnail handlers beyond the one guard-clause test already in place.

## Low

- `arxlib/folderpick` — OS-native file/folder dialogs, no tests exist; inherently hard to unit
  test and low blast radius.
- `icon.go` systray icon bytes — trivial, no value in testing.
- Profiling-only GET pages (`POList`/`PODetail`/`SupplierDetail`/etc. via
  `TestIntegration_RouteRoundTrips`) — no assertions, but read-only so low risk.
- Template **render-data** assertions beyond parse-only — `TestCoreTemplatesParse` family
  confirms templates parse, not that they render correct data for a given input.
