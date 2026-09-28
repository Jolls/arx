# #221 slice 4: PO lifecycle → sqlc

Part of #221 / #190. Moves the PO status/approval/receiving statements in `arx_go/pos.go` onto
`internal/purchasing`.

## Scope
- `POStatusTransition` (state read), `recordPOStatusChange` (history event + status update),
  `resetApproval`, `POApprovalAction` (state read, approval event, approval update).
- `POReceive`: its own `purchase_order`/`po_line` statements only — state read, `FOR UPDATE`
  status lock + re-check (#191), per-line receipt update, committed-line qty reload.

Out of scope: POReceive's inventory writes (`createLot`, `recordInventoryTxn`) stay raw on the same
tx for #222. RFQ writes and `rfq_bom.go` are slice 5. `pos.go` stays out of `convertedFiles`.

## Decisions
- Tx boundaries, statement order, the `FOR UPDATE` lock and both status checks are unchanged;
  tx statements run through `purchasing.New(tx)`.
- `recordPOStatusChange`'s three UPDATE variants become one `SetPOStatus` with a `CASE` on
  date_closed (close: keep/set `CAST(CURRENT_TIMESTAMP AS DATE)`; reopen from closed: NULL;
  otherwise unchanged). The history row reuses slice 3's `CreatePOStatusEvent` with the prior
  status as written today ('' for a NULL status).
- New `CreatePOApprovalEvent` (nullable note) serves both `resetApproval` and `POApprovalAction`;
  `SetPOApproval` serves both approval updates.
- Reads reuse slice 3's `GetPOState`; the lock is `LockPOStatus`; the reload is `ListPOLineQtys`
  (qty/received only, returned as `POLine`s).
- `ReceivePOLine` takes the receipt date as a DATE param (same pgx date encoding as today).

## Test plan
New `arx_go/po_lifecycle_integration_test.go` (`//go:build integration`), passing on the
unchanged code first; complements the existing status/approval/receive tests in
`integration_test.go` and the #191 race tests in `tx_boundaries_integration_test.go`.
- Status: close keeps an existing date_closed, reopen clears it; is_active, date_modified, history
  actor; cancel resets only a submitted approval ("PO cancelled"); unknown target / PO.
- Approval: empty note → NULL, actor, status untouched; disallowed action, unknown action / PO.
- Receive: freeform line (no ledger row), date_received = txn date (today when blank), untouched
  line, partial → full → closed/inactive/date_closed; invalid / zero qty, unknown PO, wrong status.

Verify: go build/vet (plus `-tags integration`), go test ./..., sqlc diff, gofmt -l on touched
hunks, live ArxDev integration run.
