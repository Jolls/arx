# #221 slice 3: PO writes → sqlc

Part of #221 / #190. Moves the PO create/edit write paths in `arx_go/pos.go` onto
`internal/purchasing` (and two price/supplier-part queries onto `internal/parts`).

## Scope
- `POCreate`: RFQ anchor number + quote count, `po_number_seq`, PO insert, creation history event,
  RFQ group, line inserts, total — one tx, unchanged boundaries.
- `POUpdate`: PO id/approval lookup, line delete/update/insert, line sum, header update — one tx.
  The `resetApproval` call stays as-is (slice 4).
- `POAddSuggestions`: supplier_part link-if-missing, price deactivate + insert (no tx, as today).
- `POMarkPrinted`, `createPOFolder`, `POImportPartFile`.
- `PODuplicate`, `PONote`: nothing left after slice 2 (they only call `fetchPO`/`fetchPOItems`).

Out of scope: lifecycle (slice 4: POStatusTransition, recordPOStatusChange, resetApproval,
POApprovalAction, POReceive), RFQ writes and `rfq_bom.go` (slice 5). `pos.go` stays out of
`convertedFiles`.

## Decisions
- Header fields travel as a `purchasing.PO` (Create ignores the id/number/status/approval/
  printed/modified fields; UpdatePOHeader ignores id/number/status/approval/modified), built by one
  `poFromForm` helper shared by POCreate and POUpdate. Lines travel as `purchasing.POLine`.
- The tx stays owned by the handler; each statement runs through `purchasing.New(tx)`. Line
  revisions are still resolved outside the tx (`resolvePolRev` on the handler DB), as today.
- POUpdate's line-total sum uses `po_id` (resolved on the same tx) instead of re-joining by number;
  the header update keeps `WHERE number`.
- Reuse: `parts.CreatePrice` for the suggestion price insert, `purchasing.GetSupplier` in
  `createPOFolder`, `attachments.GetActivePartAttachment` in `POImportPartFile`. New
  `parts.LinkSupplierPN` (insert unless the exact link exists) and `parts.DeactivatePrices`.
- New `purchasing.GetPOState` (id/status/approval by number), also for slice 4; `CreatePOStatusEvent`
  takes a nullable from-status so slice 4's `recordPOStatusChange` can reuse it.
- `POMarkPrinted` passes the app's local date as `YYYY-MM-DD::date` — the same date pgx stored
  from `time.Now()` into the DATE column before (not the session time zone's date).
- Ids and numbers from the form/URL are parsed in Go; a non-numeric value fails with the same error
  prefix it got from the Postgres cast before. POAddSuggestions now validates a price row's cost and
  pack size before deactivating the old price (it used to deactivate, then fail on the insert).
- A `polLine` helper wraps `polRowToArgs` (unchanged, unit-tested) into a `purchasing.POLine`;
  `floatPtrOrNil` parses the optional money fields.

## Fix
POAddSuggestions' "add supplier link" always failed on Postgres ("inconsistent types deduced for
parameter $3": the untyped `SELECT $1,$2,$3 … supplier_pn=$3`). The typed `parts.LinkSupplierPN`
fixes it.

## Test plan
New `arx_go/po_writes_integration_test.go` (`//go:build integration`), passing on the unchanged
code first; reuses slice 2's `seedPOFixture`.
- POCreate: every header field, lines (blank line skipped, revision fallback, freeform), total,
  history event, empty optional numbers → NULL, redirect; RFQ first quote (R1, own group, no folder)
  and second quote (R2, joins group); non-numeric group and missing supplier error pages.
- POUpdate: header fields incl. date_printed, status untouched, date_modified bumped, line
  update/delete/insert, other PO's lines untouched, total, approval reset + history note; unknown PO
  and non-numeric delete id roll back.
- POAddSuggestions: new link, existing link not duplicated, unchecked rows skipped, price replaces
  the active one at the pack size, default pack 1, default supplier set; no supplier → no-op.
- POMarkPrinted: approved → date set; unapproved → 403; RFQ → allowed; unknown → 403.
- createPOFolder: supplier code / none / non-numeric; test-mode suffix.
- POImportPartFile: copies the file; unknown PO, inactive/foreign/non-numeric attachment errors.

Verify: go build/vet (plus `-tags integration`), go test ./..., sqlc diff, gofmt -l on touched
hunks, live ArxDev integration run.
