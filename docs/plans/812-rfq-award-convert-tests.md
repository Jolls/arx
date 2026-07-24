# Plan: RFQ award/convert flow test coverage (#812)

**File to add:** `arx_go/integration_test.go` (append; build-tagged `integration`, same as existing tests). No production code changes anticipated.

## New shared helper

Place near `seedThrowawayPO` (no #803-added RFQ-specific helper exists in the current tree — only `seedThrowawayPO`/`countRows`/`smokeExec` exist as of HEAD `9194f07`; re-check before implementing in case #803's tests landed a reusable "create PO with lines" helper first, per apply order):

```go
// seedThrowawayRFQ creates a fresh RFQ group with two supplier quotes (suppliers
// 1001 Acme Fasteners, 1002 Precision Machining Co — seeded in SQL/seed_test_data.sql)
// each carrying one line for part RAW-1001 (part_id 3001, qty 10), via RFQNew-shaped
// POCreate calls (isRFQ=1) then a direct po_line insert per quote (mirrors the manual
// line entry a user does on po_edit.html — RFQNew/RFQAddSupplier only render the form,
// they don't insert lines). Returns both quote numbers/IDs, the group id, and cleanup.
```

Also add a generic `withParam(req *http.Request, key, val string) *http.Request` helper (chi route-context injector for a string param) since `RFQAddSupplier`/`RFQConvert` key off `id` (a PO **number** string like `"5010R1"`, not the int `withID` assumes) and `RFQCompare`/`RFQCompareSave` key off `group` (the group's numeric PO id, as a string). `withID` is unsuitable for both.

## New tests

All in `integration_test.go`, `TestIntegration_` prefix, using `liveHandler`/`assert302`/`assertStatus`/`countRows`:

1. **`TestIntegration_RFQNew_RendersRFQForm`** — GET via `h.RFQNew`, assert 200 and body contains RFQ-specific markers. Read `templates/pos/po_edit.html` first to pick a stable string (e.g. "Request for Quotation" or an RFQ hidden field).

2. **`TestIntegration_RFQAddSupplier_ClonesLinesBlanksSupplier`** — seed one RFQ quote (via POCreate isRFQ path) with a line, GET `h.RFQAddSupplier` for that quote's number, assert 200, assert response body contains the cloned part number but not the original supplier name (proves blanking) via body substrings.

3. **`TestIntegration_RFQCompare_BuildsGridWithBestMarkers`** — use seeded in-flight group 5010: quotes 5010R1 (Acme, total 27.50) / 5010R2 (Precision, total 24.00), `rfq_group_id=5010` on both. Call `h.RFQCompare` with `group="5010"`. Assert 200, body contains both supplier names, and the lower-total supplier (5010R2/Precision) is marked "Best" — read `templates/pos/rfq_compare.html` first for the exact marker string/CSS class. Read-only, no cleanup needed.

4. **`TestIntegration_RFQCompareSave_PersistsCostAndRecomputesTotal`** — seed a throwaway RFQ group (via new helper) with one line at cost 0, POST `h.RFQCompareSave` with `cost_<polID>` and `lead_<polID>` form values, assert 302 redirect to `/rfq/<group>/compare`, then query `po_line.unit_cost`/`lead_time_days` and `purchase_order.total_cost` directly to assert both persisted and total recomputed as `qty*cost`.

5. **`TestIntegration_RFQConvert_AwardsWinnerAndCancelsSiblings`** — seed a throwaway 2-supplier RFQ group via the new helper (both quotes with lines/costs so totals differ), POST `h.RFQConvert` for the lower-cost quote's number, assert 302 to `/po/<base>`, then assert:
   - a new PO row exists at the bare base number with status `draft`, correct supplier_id/line items copied
   - the awarded quote's PO row is now `status='closed', is_active=0`
   - the sibling quote is `status='cancelled', is_active=0`
   - two `po_history` rows exist (draft creation + award) for the new/awarded IDs, and a cancel row for the sibling
   - `rfq_group_id` on the new PO is NULL
   Cleanup deletes po_line/po_history/purchase_order rows for all three PO ids (new PO + both original quotes).

6. **`TestIntegration_RFQConvert_RejectsNonRFQStatus`** — call `h.RFQConvert` on an already-`draft` PO (via `seedThrowawayPO`), assert the response body contains the "Only an RFQ can be converted to a PO." error. Verify `renderError`'s exact HTTP status (likely 200-with-error-body, matching other negative-path tests in the file) before asserting on status code.

7. **`TestIntegration_RFQConvert_RejectsWhenBaseNumberTaken`** — seeded group 5006 already has PO 5008 at base "5006"; attempt to convert 5006R1/5006R2, expect the "already in use" error. Read-only against seed, no cleanup.

## Resolved decisions (from open questions)

1. **Reusable helper from #803**: check `arx_go/integration_test.go` for a newer PO-with-lines helper before implementing (per apply order, #803 lands first); build on it if present rather than duplicating line-insertion logic. Otherwise add `seedThrowawayRFQ` as specified above.
2. **Template marker strings**: read `templates/pos/po_edit.html` and `templates/pos/rfq_compare.html` at implementation time to pick exact literal strings/CSS classes — don't guess.
3. **`renderError` status code**: verify by reading its definition before asserting on negative-path test status codes (tests 6/7).
4. **Group param semantics**: confirmed — `group="5010"` refers to `rfq_group_id`, which equals quote 5010R1's own PO id in the seed (5010=Acme quote, 5011=Precision quote, both `rfq_group_id=5010`). Don't confuse group id with either quote's own id.

## Cleanup strategy

Every test that inserts rows uses `defer cleanup()` deleting `po_line` → `po_history` → `purchase_order` for exactly the row IDs it created, matching `seedThrowawayPO`'s pattern (never touch seeded 5001–5011 range). Tests reading only pre-seeded 5006/5010 groups do no cleanup (matches `TestIntegration_DashboardStaleWIPRecords`'s read-only convention).

This is a test-only change — no production code modified.

## Critical files

- arx_go/integration_test.go
- arx_go/pos.go (read only)
- SQL/seed_test_data.sql (read only)
- arx_go/pos_test.go (read only, for existing conventions)
- arx_go/templates/pos/rfq_compare.html (read only, for marker strings)
