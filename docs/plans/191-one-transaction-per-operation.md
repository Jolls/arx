# #191 — One transaction per business operation (SaveResults first)

Scope: every write in SaveResults goes into one transaction. Lock/state checks become conditional writes (or a row lock) inside that transaction, with a rows-affected check. The same two defects are fixed in ReceivePO, Build and ConvertRFQ only where the current code has them. Postgres-only; SQL Server compatibility is not preserved. Use the handler wrappers: `h.beginTx` → `*txLogger`, then `tx.ExecContext` / `tx.QueryRowContext` / `tx.QueryContext`. Keep the existing `@pN` / `GETDATE()` / `h.dia().BoolLiteral(...)` style, because `Rewrite` still translates it.

Out of scope, for follow-ups:
- Making stored counts work one way (triggers maintain `attachment_count`, `po_line_count`, `SUNumOfLNKs`, `SUNumOfPOs`; app code maintains `stock_on_hand` at `arx_go/inventory.go:44-46`).
- The sqlc service layer (#190).

## Current transaction boundaries (audit)

| Operation | Pre-tx reads/checks | Writes outside tx | Tx | Defect |
|---|---|---|---|---|
| SaveResults `arx_go/records.go:2446` | record + `is_locked` check 2458-2478; form 2482; steps 2489; existing results 2539; tracking_mode 2643 | result UPDATE 2591, result INSERT 2608 (`h.execContext`) | begin 2658, commit 2779 | **Both** |
| completeRecordTx `arx_go/records_history.go:127` | none | none | begin 135, conditional UPDATE 151 + RowsAffected 157, commit 163 | None (this is the reference pattern) |
| PartBuildCreate `arx_go/build.go:366` | BOM 393 (no state check) | none | begin 408, commit 455 | **Read-then-write**: return_record `SELECT is_locked` 436, then UPDATE by id 444 |
| POReceive `arx_go/pos.go:1605` | PO status read 1614 + check 1625; items 1630 | none | begin 1651, commit 1709 | **Read-then-write**: the status check is outside the tx; status is derived from the stale in-memory `items` (1697, 1701) |
| RFQConvert `arx_go/pos.go:2499` | status read 2509 + check 2520; base-number check 2529 | none | begin 2539, commit 2666 | **Read-then-write**: the awarded-quote close UPDATE 2616 has no status guard; siblings are SELECTed at 2625, then UPDATEd by id at 2657 with no guard |

RFQConvert's base-number check (2529) is not changed. `UQ_purchase_order_number` (`SQL/postgres/purchase_order.sql:17`) already makes a racing insert fail inside the tx, which rolls it back.

## Changes

### 1. `arx_go/records.go` — SaveResults

1. Right after the `r.ParseForm()` block (2452-2455), insert the transaction start that is currently at 2658-2663 (`tx, err := h.beginTx(r.Context())` … `defer tx.Rollback()`). Delete it from 2658-2663.
2. Directly after the tx begins, add the lock guard:
   ```go
   // #191: claim the WIP record inside the tx. Row lock serializes against Lock/Complete;
   // 0 rows = locked (or missing — distinguished by the SELECT below).
   guard, err := tx.ExecContext(r.Context(), fmt.Sprintf(
       "UPDATE %s SET updated_at=GETDATE() WHERE id=@p1 AND is_locked=%s",
       h.cfg().RecordsTable(), h.dia().BoolLiteral(false)), recordID)
   if err != nil {
       serverError(w, "could not save record", err)
       return
   }
   claimed, _ := guard.RowsAffected()
   ```
3. Record SELECT (2458): change `h.queryRowContext` to `tx.QueryRowContext`. Keep the `sql.ErrNoRows` → 404 and error branches. Replace `if record.IsLocked {` (2475) with `if claimed == 0 {`, keeping the same redirect body.
4. Change these reads from `h.queryRowContext` / `h.queryContext` to `tx.QueryRowContext` / `tx.QueryContext`: form 2482, steps 2489, existing results 2539, tracking_mode 2643.
5. Change the result writes from `h.execContext(` to `tx.ExecContext(`: UPDATE 2591 and INSERT 2608.
6. Leave `recordLinkageArgs`, `loadBuildLines`, `collectLotPicks`, `performBuild`, `appendLotNote`, `upsertUnitForRecord`, the final record UPDATE (2766-2774) and the Commit (2779) unchanged. Their early returns now roll back through the deferred `tx.Rollback()`.

### 2. `arx_go/build.go` — PartBuildCreate return_record link (431-453)

Replace the `SELECT COALESCE(part_id,0), is_locked` plus `if err == nil && recPart == partID && !recLocked` block with one conditional UPDATE:
```go
var lotArg any
if outputLotTracked {
    lotArg = outputLotID
}
res, err := tx.ExecContext(r.Context(), fmt.Sprintf(
    `UPDATE %s SET lot_id = @p1, build_id = @p2, updated_at = GETDATE() WHERE id = @p3 AND part_id = @p4 AND is_locked = %s`,
    h.cfg().RecordsTable(), h.dia().BoolLiteral(false)), lotArg, buildID, recID, partID)
if err != nil {
    h.renderError(w, r, "Error linking build to test record: "+err.Error())
    return
}
if n, _ := res.RowsAffected(); n > 0 {
    linkedRecord = recID
}
```
Remove the `recPart` / `recLocked` variables. Keep the comment above; it still describes the behaviour (skipped silently).

### 3. `arx_go/pos.go` — POReceive

1. Keep the pre-tx read and check (1612-1628) as the fast path for "not found" and the wrong-status message.
2. Right after the tx begins (after the `defer` at 1661), lock the PO row and re-check its status. This uses `SELECT … FOR UPDATE`, not a conditional UPDATE: a no-op UPDATE on `purchase_order` would fire `trg_PO_company_count_upd` and needs a column to touch.
   ```go
   // #191: lock the PO row and re-check status inside the tx.
   if err := tx.QueryRowContext(r.Context(), fmt.Sprintf(
       `SELECT status FROM %s WHERE ID = @p1 FOR UPDATE`, h.cfg().POTable()), poID).Scan(&status); err != nil {
       h.renderError(w, r, "Error loading PO: "+err.Error())
       return
   }
   if status.String != "sent" && status.String != "partially_received" {
       h.renderError(w, r, "Only a sent or partially-received PO can receive goods.")
       return
   }
   ```
3. Delete `items[i].ReceivedQty += d // keep in-memory copy…` (1697).
4. Before the `derivePOReceiptStatus(items)` call (1701), re-read the lines inside the tx and derive status from them:
   ```go
   lineRows, err := tx.QueryContext(r.Context(), fmt.Sprintf(
       `SELECT qty, received_qty FROM %s WHERE po_id = @p1`, h.cfg().POLineTable()), poID)
   if err != nil {
       h.renderError(w, r, "Error reloading PO lines: "+err.Error())
       return
   }
   var current []models.PurchaseOrderLine
   for lineRows.Next() {
       var l models.PurchaseOrderLine
       if err := lineRows.Scan(&l.Qty, &l.ReceivedQty); err != nil {
           lineRows.Close()
           h.renderError(w, r, "Error reloading PO lines: "+err.Error())
           return
       }
       current = append(current, l)
   }
   if err := lineRows.Err(); err != nil {
       lineRows.Close()
       h.renderError(w, r, "Error reloading PO lines: "+err.Error())
       return
   }
   lineRows.Close()
   ```
   Change `derivePOReceiptStatus(items)` to `derivePOReceiptStatus(current)`.

### 4. `arx_go/pos.go` — RFQConvert

1. Move `now := time.Now()` and `actor := h.actorName(r)` (2551-2552) to just after the tx `defer` (2549).
2. After those two lines, insert the award guard, moved and conditioned from 2616-2621:
   ```go
   // #191: claim the quote — only a still-'rfq' quote can be awarded.
   res, err := tx.ExecContext(r.Context(), fmt.Sprintf(
       `UPDATE %s SET status='closed', is_active=%s, date_modified=@p1 WHERE ID=@p2 AND status='rfq'`,
       h.cfg().POTable(), h.dia().BoolLiteral(false)), now, poID)
   if err != nil {
       h.renderError(w, r, "Error closing awarded quote: "+err.Error())
       return
   }
   if n, _ := res.RowsAffected(); n == 0 {
       h.renderError(w, r, "Only an RFQ can be converted to a PO.")
       return
   }
   ```
   Delete the original UPDATE at 2616-2621. Keep the "Awarded" history INSERT (2609-2615) where it is. Keep the pre-tx status check (2520) as the fast path.
3. In the sibling loop (2649-2663), swap the order: run the UPDATE first with `AND status='rfq'` added to its WHERE, then `if n, _ := res.RowsAffected(); n == 0 { continue }`, then the history INSERT.

### 5. CHANGELOG.md

Add under the current top `0.8.x` entry, `### Fixed`:

`- Saving test results, receiving a PO, converting an RFQ and linking a build to a record now happen as one all-or-nothing step and cannot write to a record/PO that was locked or changed at the same moment ([#191](https://github.com/Jolls/arx/issues/191))`

No migration and no schema change.

## Test plan

All tests below are integration tests. Put them in a new `arx_go/tx_boundaries_integration_test.go` (`//go:build integration`).

Seeding and assertions go only through `h.queryRowContext` / `h.execContext`, `h.dia().InsertReturningID(...)` and `h.dia().BoolLiteral(...)`. Don't use raw `h.DB()` with `@pN`, `OUTPUT INSERTED`, `ISNULL` or `TOP`, which fail on Postgres.

Add these helpers in the new file:
- `requirePostgres(t, h)`: `t.Skip` unless `h.dia().Name() == "postgres"`.
- `waitForLockWaiter(t, h, ctx)`: polls every 20ms, for up to 5s, `SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'` until it is > 0; otherwise `t.Fatal`.
- `seedSRRecord(t, h, ctx) (recordID, testID int, cleanup func())`: a throwaway part, form, one form_row (`test_order` set) and a WIP form_record (strings non-NULL, `is_locked` false), plus one result row with `result='5.00'`. It mirrors `seedLockTestForm` + `seedWIPRecord`, written dialect-neutrally, and cleanup mirrors `seedLockTestForm`'s.

For PO tests, seed via `h.POCreate` / `setPOStatus` like `seedThrowawayPO` and `seedRFQQuote` do, but look up ids with `h.queryRowContext`. Clean up with `h.execContext` DELETEs.

Race tests all use the same pattern:
1. Open `lockTx := h.beginTx(ctx)` and apply an uncommitted change.
2. Start the handler in a goroutine.
3. Call `waitForLockWaiter`, then `lockTx.Commit()`.
4. Wait for the goroutine (10s timeout), then assert.

### 1) Coverage audit (existing)

- SaveResults:
  - `TestIntegration_AttachStepPassFail` (integration_test.go:642): result update and pass/fail.
  - `TestIntegration_RecordLinkageSave` (2173): linkage, and a 400 on a foreign lot.
  - `TestIntegration_SerialUnitCreationAndRetest` (2236) and `TestIntegration_SaveDoesNotDuplicateUnitOnSerialMismatch` (2338): units.
  - `TestIntegration_BuildAtTestTime` (2449): the build panel.
  - Gaps: nothing covers saving to a locked record, a 404, or rollback after a failure.
- completeRecordTx: `records_lock_integration_test.go` (lifecycle and bulk lock). Unchanged.
- PartBuildCreate: `TestIntegration_BuildConsumesOnlyStockedComponents` (1802), `BuildLotGenealogy` (1925), `BuildReturnsToRecord` (2068). Gap: nothing covers a locked return_record.
- POReceive:
  - `POReceive_PartialThenFull` (3495), `CreatesLotForLotTrackedPart` (3572), `RejectsWrongStatus` (3620).
  - Unit tests: `TestParseReceiveDeltas`, the `derivePOReceiptStatus` table test (pos_test.go:226), and the `poCanTransition` tests.
- RFQConvert: `AwardsWinnerAndCancelsSiblings` (3879), `RejectsNonRFQStatus` (3959), `RejectsWhenBaseNumberTaken` (3975). Unit test: `TestRFQBaseNumber`.
- Caveat: the existing integration tests use SQL Server-only SQL through raw `h.DB()` (`OUTPUT INSERTED`, `ISNULL`, `TOP 1`, `@pN`), so they don't run on Postgres as written.

### 2) Characterization tests (pass now and after)

- `TestIntegration_SaveResults_LockedRecordWritesNothing`: lock the record (committed), then POST `result_<testID>=9.99` and `record_type=Changed`. Expect 303 with Location `/records/{id}`, the result still `5.00`, and `record_type` unchanged.
- `TestIntegration_SaveResults_NotFound`: POST to id `-1`. Expect 404.
- `TestIntegration_SaveResults_HappyPathUpdatesResultAndRecord`: POST `result_<testID>=9.99` and `record_type=Changed`. Expect 303, the result `9.99`, and `record_type='Changed'`.
- `TestIntegration_BuildReturnRecord_LockedRecordNotLinked`: use the same setup and cleanup as `BuildReturnsToRecord` (part 3012, lot[3007]=8301, form 6001), but seed the record with `is_locked` true (committed). Expect 302 with Location `/part/3012/build?built=1`, and the record's `build_id` and `lot_id` NULL.

### 3) Red tests (fail now, pass after)

- `TestIntegration_SaveResults_FailureAfterResultWritesRollsBack`, with two subtests on `seedSRRecord`:
  - (a) POST `result_<testID>=9.99` with `lot_id=8301`, a lot that doesn't belong to the record's part. Expect 400.
  - (b) POST `result_<testID>=9.99` with `build_panel=1` and `qty=1`; the record has no part, so there's no BOM. Expect 400.
  - Both assert the result is still `5.00`. Today it is `9.99`, because the result is written before the error.
- `TestIntegration_SaveResults_LockRaceWritesNothing` (`requirePostgres`): lockTx runs `UPDATE form_record SET is_locked=TRUE WHERE id=@p1`, then SaveResults POSTs `result_<testID>=9.99`. Expect 303 to `/records/{id}` and the result still `5.00`. Today the result is written before the handler blocks on the final record UPDATE.
- `TestIntegration_BuildReturnRecord_LockRaceNotLinked` (`requirePostgres`): same setup as the characterization test with a WIP record. lockTx sets `is_locked=TRUE`, then PartBuildCreate runs with `return_record`. Expect the record's `build_id` NULL and Location not `/records/{id}/edit?built=1`.
- `TestIntegration_POReceive_StatusRaceRejected` (`requirePostgres`): a sent/approved PO with one line on part 3001 (qty 10). lockTx runs `UPDATE purchase_order SET status='cancelled' WHERE ID=@p1`, then POReceive runs `recv[line]=4`. Expect a 200 body containing "Only a sent or partially-received PO", PO status `cancelled`, the line's `received_qty` 0, and 0 `inventory_transaction` rows for the line. Cleanup restores `stock_on_hand` for 3001 only if a row was written.
- `TestIntegration_POReceive_StatusDerivedFromCommittedLines` (`requirePostgres`): a sent PO with two lines (qty 10 each, part 3001). lockTx runs `SELECT ID FROM purchase_order WHERE ID=@p1 FOR UPDATE` and `UPDATE po_line SET received_qty=10 WHERE id=<line2>`, then POReceive runs `recv[line1]=10`. Expect PO status `closed`. Today it is `partially_received`.
- `TestIntegration_RFQConvert_StatusRaceRejected` (`requirePostgres`): a standalone quote from `seedRFQQuote(…, 1001, 0, 10, 2.50)`. lockTx sets the quote's status to `cancelled`, then RFQConvert runs. Expect a body containing "Only an RFQ can be converted to a PO.", 0 POs with `number = rfqBaseNumber(quote)`, and the quote status `cancelled`.
- `TestIntegration_RFQConvert_SiblingChangedConcurrentlyNotCancelled` (`requirePostgres`): acme = `seedRFQQuote(…,1001,0,…)`, pmc = `seedRFQQuote(…,1002,acmeID,…)`. lockTx sets acme's status to `draft`, then pmc is converted. Expect acme status `draft` and 0 acme history rows with `to_status='cancelled'`. Clean up the new PO at the base number.

### 4) Manual-only

- Record editor: save results, notes and record_type on a WIP record; they persist. Open a record in two tabs, Complete it in tab A, save in tab B: tab B lands on the read-only record page and its edits aren't applied.
- Build this unit from a record: the return leg still auto-restores the draft (`?built=1`).
- Receive part, then the rest, of a sent PO: status goes partially_received, then closed. Convert an RFQ group: the winner is closed, siblings are cancelled, and the new PO opens.
- With `DEBUG_MODE=true`, the SaveResults SQL log shows the `UPDATE … AND is_locked=FALSE` right after BEGIN, and no result writes before BEGIN.

### Critical files
- arx_go/records.go
- arx_go/pos.go
- arx_go/build.go
- arx_go/tx_boundaries_integration_test.go (new)
- arx_go/records_history.go (reference pattern only, unchanged)

## Resolved decisions (2026-09-26)
- Test helpers: new file brings its own dialect-neutral helpers (shared helpers are ported later in the Azure-cleanup pass).
- POReceive uses `SELECT … FOR UPDATE`: accepted.
- SaveResults guard bumps `updated_at` (rolled back on failure): accepted.
- RFQ sibling-cancel guard: keep.
- Note: #192 applies first and replaces `now`/`date_modified=@pN` in RFQConvert with `GETDATE()`; adapt the award guard accordingly (`date_modified=GETDATE() WHERE ID=@p1 AND status='rfq'`).
