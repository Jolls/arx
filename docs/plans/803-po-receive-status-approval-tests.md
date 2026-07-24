# 803 — Integration tests for POReceive / POStatusTransition / POApprovalAction

Test-only change; no production code edits. Add build-tagged tests
(`//go:build integration`) to `arx_go/integration_test.go`, following existing
conventions: `liveHandler(t)`, `withID`, `postForm`, `assert302`, `countRows`,
`smokeExec`, `seedThrowawayPO`.

## Resolved decisions (supersede the planning agent's open questions)

1. **Seed fixtures**: use `seedThrowawayPO` + directly-inserted lines via a new
   `seedPOWithLines` helper, forcing status via raw SQL (`setPOStatus` helper). No
   `SQL/seed_test_data.sql` changes. (Option (a) from the agent's writeup.)
2. **`renderError`'s status code**: confirmed by reading `arx_go/handlers.go:497-502`
   — `renderError` calls `h.render(w, r, "shared/error.html", ...)` with no explicit
   `WriteHeader`, so it's a plain `200 OK`. Use `assertStatus(t, ..., http.StatusOK)`
   for all rejected-path assertions, and check the body contains the expected message
   substring (message strings below are confirmed verbatim from `pos.go`).
3. **Config accessor for `inventory_transaction`**: confirmed — it's
   `h.cfg.InventoryTxnTable()` (not `InventoryTable()`), returns `"inventory_transaction"`.
   Use this name throughout.
4. **Cleanup ordering for `inventory_transaction`/`lot` rows**: don't rely on
   `defer`/`t.Cleanup` interleaving. In each `POReceive` test, after assertions,
   explicitly delete `inventory_transaction` (and `lot`, where created) rows by
   `po_line_id` inline, before the deferred `seedThrowawayPO` cleanup runs (which
   deletes `po_line`/`purchase_order_history`/`purchase_order`). Do this via a
   `t.Cleanup` registered *after* `defer poCleanup()` is set up is insufficient to
   guarantee ordering against `defer` — instead, replace `poCleanup`'s defer with an
   explicit ordered cleanup sequence at the end of the test (call inventory/lot
   deletes directly, then call `poCleanup()` directly — not deferred — as the last
   statement of each receive test), or use `t.Cleanup` for *all* steps (inventory/lot
   cleanup registered first — since `t.Cleanup` runs LIFO, register it before
   `poCleanup`'s own `t.Cleanup`/defer so it runs first). **Use `t.Cleanup` exclusively
   in the new receive tests** (not `defer poCleanup()`) to get deterministic LIFO
   ordering: register inventory/lot cleanup via `t.Cleanup` immediately after creating
   those rows (runs first, LIFO), and call `poCleanup()` itself via `t.Cleanup(poCleanup)`
   registered at the top (runs last).
5. **Authorized-approver session injection**: confirmed via `arx_go/auth.go:21,128` —
   `currentUser(r)` reads `r.Context().Value(ctxUserKey).(*User)`, set by `withUser`
   (auth.go:166) via `context.WithValue(r.Context(), ctxUserKey, u)`. Since `ctxUserKey`
   is unexported, add a small test helper in `integration_test.go` (same package,
   so it can reference `ctxUserKey` directly):
   ```go
   // withApprover injects a *User with CanApprovePO=true onto the request context,
   // mirroring what withUser does for a real authenticated request.
   func withApprover(req *http.Request) *http.Request {
       u := &User{ID: 8001, Username: "admin", CanApprovePO: true}
       return req.WithContext(context.WithValue(req.Context(), ctxUserKey, u))
   }
   ```
6. No production bugs found during planning; none to flag.

## New helpers (add to `arx_go/integration_test.go`)

```go
// seedPOWithLines creates po_line rows directly for the given PO (created via
// seedThrowawayPO) and returns the new line IDs in insertion order.
func seedPOWithLines(t *testing.T, h *Handler, ctx context.Context, poID int,
    lines []struct {
        PartID   int
        Qty      float64
        UnitCost float64
    }) []int {
    t.Helper()
    ids := make([]int, 0, len(lines))
    for i, ln := range lines {
        var id int
        err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
            `INSERT INTO %s (po_id, part_id, line_number, description, qty, unit_cost, received_qty)
             OUTPUT INSERTED.id VALUES (@p1, @p2, @p3, 'test line', @p4, @p5, 0)`,
            h.cfg.POLineTable()), poID, ln.PartID, i+1, ln.Qty, ln.UnitCost).Scan(&id)
        if err != nil {
            t.Fatalf("seedPOWithLines: %v", err)
        }
        ids = append(ids, id)
    }
    return ids
}

// setPOStatus force-sets a PO's status/approval_status directly for test setup,
// bypassing the audited transition path.
func setPOStatus(ctx context.Context, h *Handler, poID int, status, approval string) error {
    _, err := h.DB().ExecContext(ctx,
        fmt.Sprintf("UPDATE %s SET status=@p1, approval_status=@p2 WHERE ID=@p3", h.cfg.POTable()),
        status, approval, poID)
    return err
}

// withApprover injects an authorized-approver *User onto the request context.
func withApprover(req *http.Request) *http.Request {
    u := &User{ID: 8001, Username: "admin", CanApprovePO: true}
    return req.WithContext(context.WithValue(req.Context(), ctxUserKey, u))
}
```

Verify `po_line` column names (`part_id`, `line_number`, `description`, `qty`,
`unit_cost`, `received_qty`) against `SQL/po_line.sql` before writing — adjust the
INSERT if actual column names differ.

## Fixture parts to reuse from seed

- Part `3001` (`RAW-1001`) — not lot-tracked. Non-lot-tracked receiving + plain
  status-transition tests.
- Part `3007` (`RAW-1002`) — `is_lot_tracked = 1`. Lot-creation receiving path.

Verify both part IDs/lot-tracked flags against `SQL/seed_test_data.sql` before use.

## New tests

### `TestIntegration_POStatusTransition_AllowedAndRejected`
- Seed throwaway PO (draft). Reject `draft->sent` (must go through open): assert 200,
  body contains `"Cannot change status"`, status unchanged, no new history row.
- Allow `draft->open`: assert 302, `status=="open"`, `is_active==true`, a
  `purchase_order_history` row with `event_type='status'`, `from_status='draft'`,
  `to_status='open'`, non-empty `changed_by`.
- Force `sent`/`approved` via `setPOStatus`, transition `sent->closed`: assert 302,
  `status=="closed"`, `date_closed` set (non-NULL).
- Transition `closed->open` (reopen): assert 302, `status=="open"`, `date_closed`
  cleared (NULL) — covers `recordPOStatusChange`'s COALESCE/NULL branch.

### `TestIntegration_POStatusTransition_ApprovalGateForSent`
- Seed throwaway PO, force `open`/`pending` (not approved).
- `open->sent`: assert 200, body contains `"must be approved"`, status unchanged.
- Force `open`/`approved`, retry `open->sent`: assert 302.

### `TestIntegration_POStatusTransition_CancelClearsApproval`
- Seed throwaway PO, force `open`/`approved`.
- `open->cancelled`: assert 302, `status=="cancelled"`, `approval_status=="not_submitted"`,
  and the latest `purchase_order_history` row with `event_type='approval'` has
  `action=="reset"`.

### `TestIntegration_POReceive_PartialThenFull`
- Seed throwaway PO with one non-lot-tracked line (part 3001, qty 10, unit_cost 2.50).
  Force `sent`/`approved`.
- Partial receive 4 of 10 via `recv[<lineID>]=4`: assert 302, PO `status=="partially_received"`,
  line `received_qty==4`, one new `inventory_transaction` row for that line
  (`txn_type=="receipt"`, `qty==4`, `reference==<poNumber>`).
- Full receive remaining 6: assert 302, PO `status=="closed"`, `received_qty==10`,
  a second `inventory_transaction` row, and exactly 2 `purchase_order_history` rows
  with `event_type='status'` (sent→partially_received, partially_received→closed).
- Cleanup (via `t.Cleanup`, LIFO — register before `poCleanup`): delete
  `inventory_transaction` rows by `po_line_id`.

### `TestIntegration_POReceive_CreatesLotForLotTrackedPart`
- Seed throwaway PO with one lot-tracked line (part 3007, qty 5, unit_cost 4.10).
  Force `sent`/`approved`.
- Receive all 5 with `recv[<lineID>]=5` and `vlot[<lineID>]=VENDOR-LOT-803`: assert 302.
- Assert a new `lot` row exists with `po_line_id==<lineID>`, `vendor_lot_number=="VENDOR-LOT-803"`,
  `lot_description` referencing the PO number (verify exact format against `POReceive`'s
  code before asserting an exact string — read the lot-creation code around the
  `IsLotTracked` branch first).
- Assert the new `inventory_transaction` row for that line has `lot_id` pointing at
  the new lot row.
- Cleanup (via `t.Cleanup`, LIFO, before `poCleanup`): delete `inventory_transaction`
  then `lot` rows by `po_line_id`.

### `TestIntegration_POReceive_RejectsWrongStatus`
- Seed throwaway PO (draft, no status change).
- Attempt receive: assert 200, body contains `"Only a sent or partially-received PO"`.

### `TestIntegration_POApprovalAction_FullWorkflow`
- Seed throwaway PO (`approval_status=="not_submitted"`).
- `action=submit`: assert 302, `approval_status=="pending"`.
- `action=reject` **without** `withApprover` (plain request, no user on context):
  assert 200, body contains `"not authorized"`, `approval_status` unchanged (`"pending"`).
- `action=reject` **with** `withApprover(req)`, `note=needs rework`: assert 302,
  `approval_status=="rejected"`, latest `purchase_order_history` row with
  `event_type='approval'` has `action=="rejected"`, `note=="needs rework"`.
- `action=submit` again (resubmit after reject): assert 302, `approval_status=="pending"`.
- `action=approve` **with** `withApprover(req)`: assert 302, `approval_status=="approved"`.

## Verification

`go test -tags integration ./arx_go/...` with `ARX_TEST_DSN` pointed at ArxDev
(sourced from `arx_go/config/LOCAL-IntTesting.txt`).

## Critical files

- arx_go/pos.go (read only)
- arx_go/auth.go (read only, for ctxUserKey/currentUser)
- arx_go/integration_test.go (additions)
- SQL/po_line.sql, SQL/seed_test_data.sql (read only, verify column names/fixtures)
