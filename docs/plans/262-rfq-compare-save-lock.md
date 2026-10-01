# #262 RFQ compare save can rewrite quotes already awarded or cancelled

## Current code
- `arx_go/pos.go` `RFQCompareSave` (~1999): begins tx, `pur := purchasing.New(tx)`, `ListRFQLineIDs(groupID)` (all lines of all quotes in group, any status), `SetRFQLineQuote` per id, `RecomputeRFQTotals(groupID)`, commit, 302 to `/rfq/<group>/compare`. No lock, no status check.
- `RFQConvert` (~2111) calls `pur.LockRFQGroup(ctx, *quote.RFQGroupID)` right after the tx begins, before `AwardRFQQuote`. Errors via `h.renderError(w, r, msg)` (200 + error body, no redirect).
- `internal/purchasing/purchasing.sql` 338-353: `ListRFQLineIDs`, `SetRFQLineQuote`, `RecomputeRFQTotals`; 358 `LockRFQGroup` (`... ORDER BY id FOR UPDATE`). Wrappers in `internal/purchasing/purchasing.go` 695-714.
- Precedent for "no quote still 'rfq'": `renderError` with "Only an RFQ can be converted to a PO." (RFQConvert 2079/2128); `RFQCompare` uses `renderError("RFQ group not found.")`. No redirect/flash precedent for this case.

## Changes
1. `internal/purchasing/purchasing.sql`
   - Replace `ListRFQLineIDs` with `ListOpenRFQLineIDs :many`: same SELECT plus `AND po.status = 'rfq'`. (Only caller is RFQCompareSave.)
   - `SetRFQLineQuote`: add `AND po_id IN (SELECT id FROM purchase_order WHERE status = 'rfq')` (belt and braces).
   - `RecomputeRFQTotals`: add `AND status = 'rfq'` to the WHERE.
2. Run `sqlc generate` (never hand-edit `internal/dbq`).
3. `internal/purchasing/purchasing.go`: rename wrapper `ListRFQLineIDs` -> `ListOpenRFQLineIDs`, update doc comments on it, `SetRFQLineQuote`, `RecomputeRFQTotals` to say "quotes still in 'rfq'".
4. `arx_go/pos.go` `RFQCompareSave`, after `pur := purchasing.New(tx)`:
   - Call `pur.LockRFQGroup(r.Context(), groupID)`; on error `renderError("Error locking RFQ group: "+err.Error())` (same text as RFQConvert).
   - Then `ListOpenRFQLineIDs` (after the lock, so it sees the post-convert statuses).
   - If the result is empty: `renderError(w, r, "<message, see Open questions>")` and return (tx rolls back via the defer). Do not redirect.
   - Update the handler doc comment (only still-'rfq' quotes are saved; group is locked like RFQConvert).
5. Docs: add a CHANGELOG.md entry under Unreleased (#262) in the file's existing format.

## Test plan
### 1) Coverage audit
- `TestIntegration_RFQCompareSave_PersistsCostAndRecomputesTotal` (integration_test.go:3559) and `TestIntegration_RFQ_CompareSave` (rfq_integration_test.go:76): happy path, foreign-group isolation, blank/junk input, unknown group (expects 302), non-numeric group (expects "Error loading RFQ lines").
- `tx_boundaries_integration_test.go` has `raceHandler` + `RFQConvert` race tests (439, 470). No test covers save against non-'rfq' quotes or save/convert interleaving.
- Gap: status filter on save; lock queueing against convert; empty-open-group rejection.

### 2) Characterization tests (pass on unchanged code, keep passing)
- All existing tests listed above, except the unknown-group assertion at rfq_integration_test.go:132 if the Open question is answered with rejection (then update that assertion in the same change).
- Mixed group still saves the 'rfq' quote: group of two 'rfq' quotes saves both (already covered by `TestIntegration_RFQ_CompareSave` shape; no new test needed).

### 3) Red tests (all in `arx_go/rfq_integration_test.go`, use `lifecycleSetup`, `seedRFQPO`, `seedRFQLine`; no new seed rows)
- `TestIntegration_RFQ_CompareSave_SkipsNonRFQQuotes`: group of quote A (status 'rfq') and quote B (status 'closed'), plus quote C ('cancelled'), each with a line at cost 9/lead 2 and total_cost preset. POST save with cost/lead for all lines. Assert A's line updated and total recomputed; B and C lines still `9|2`, B/C total_cost and date_modified unchanged. Fails today: save rewrites every line and total in the group.
- `TestIntegration_RFQ_CompareSave_RejectsWhenNoOpenQuote`: group where all quotes are 'closed'/'cancelled'. POST save. Assert response 200 with the error body (not 302), lines/totals unchanged. Fails today: returns 302 and rewrites lines.
- `TestIntegration_RFQ_CompareSave_QueuesBehindConvert` (in `tx_boundaries_integration_test.go`, uses `liveHandler`/`seedRFQQuote`/`raceHandler`): two-quote group; lockTx does `UPDATE purchase_order SET status='closed' WHERE ID=$1` on quote A (holds its row lock) while `RFQCompareSave` runs on the group; after commit assert A's line cost is unchanged and total_cost unchanged. Fails today: save does not block on the lock and, once unblocked, overwrites A's line with the stale price.

### 4) Manual-only
- Two browser sessions: open compare page in both, convert in one, save in the other; confirm error message shown and quote prices on the closed/cancelled quotes unchanged.

## Seed rows
None. Tests insert throwaway rows via `seedRFQPO` / `seedRFQQuote` with cleanup; ArxDev seed is untouched.

## Open questions
1. Message wording for the empty-open-group rejection (no existing text fits exactly; "Only an RFQ can be converted to a PO." is convert-specific). Proposed: "No quotes in this RFQ group are still open; nothing was saved."
2. Unknown group id (no quotes at all) currently redirects 302 (asserted at rfq_integration_test.go:132). With the new empty check it would hit the same rejection. Accept that (update the assertion) or keep 302 for a group with zero quotes and reject only when quotes exist but none are 'rfq'?
3. When some quotes are closed and others 'rfq' (mixed group, e.g. partial states), confirm saving only the 'rfq' ones silently is wanted, versus rejecting the whole save.

## Resolved decisions
- Error text: "No quotes in this RFQ group are still open; nothing was saved." (renderError, like RFQConvert).
- Mixed group: update only quotes still in 'rfq'; silently skip closed/cancelled ones.
- Reject only when the group has quotes but none are 'rfq'. Unknown group id (zero quotes) keeps the current 302; existing test at rfq_integration_test.go:132 unchanged.
