# #693 - Align Go struct fields with renamed DB columns

The DB tables/columns were renamed to snake_case in the db-table-rename effort, but the Go
**struct fields** kept the old abbreviation prefixes (`FILFileName`, `POLQty`, `PNID`, `CNName`).
Reads are **positional**, so field names aren't bound to columns by tag - this is a Go-side +
template-side rename only. No SQL, no behavior change.

Delivered as **3 PRs by prefix family** so each `models` struct lives entirely in one PR and
Jolls can run integration tests between them.

## Scope rules (contextual, not a bulk rename)

- **In scope:** the `models` package entity structs that map to renamed columns, their `.Field`
  accesses across Go, and template `.Field` accesses.
- **Out of scope (left as-is):**
  - `company` table `SU*` columns - **not renamed in the DB** (`company.sql` still has `SUWeb`,
    `SUNotes`, `SUSupplierCode`, `SUNumOfLNKs`, `SUNumOfPOs`). So `Supplier.SU*` fields stay.
  - HTML form input `name="..."` attributes and `fv(r, "PNReqBy")` request-param keys - POST
    keys, not columns.
  - SQL column strings (already snake_case) and DB trigger names (`trg_FIL_part_count`).
  - Local handler-scoped view/form structs and request DTOs that carry `PNID`/short names but
    are not `models` types (see "Deferred" below).
- **Method:** `gopls` isn't installed, so rename one struct at a time and let
  `GOOS=windows go build ./...` flag every stale Go access (complete safety net for Go).
  Templates aren't compile-checked - grep every renamed field across `templates/` by hand.
  The app builds Windows-only (`folderpick` uses `syscall.HideWindow`), and cross-compiled
  test binaries can't exec on darwin, so verification here is `build` + `vet -tags integration`;
  the actual test run happens on Jolls's Windows box (the reason for separate PRs).

## PR 1 - Parts (`PN` / `FIL` / `PL`) - DONE

Branch `feature/693-field-rename-parts`.

| Struct (models) | Table | Field renames |
|---|---|---|
| `Part` | `part` | `PNID→ID`, `Active→IsActive`, `PNReqBy→RequestedBy`, `PNNotes→Notes`, `PNDate→CreatedDate`, `PNDateModified→ModifiedDate`, `PNFILIDPrimary→PrimaryAttachmentID`, `PNCurrentCost→CurrentCost`, `PNLastRollupCost→LastRollupCost`, `PNLastRollupAt→LastRollupAt`, `PNFILLinks→AttachmentCount`, `PNPOLinks→POLineCount` |
| `Attachment` | `part_attachment` | `FILID→ID`, `FILPNID→PartID`, `FILFileName→FileName`, `FILPNRev→PartRevision` |
| `BOMItem` | `bom` (+ part joins) | `PLID→ID`, `PLItem→LineNumber`, `PLQty→Qty`, `PLPartID→ComponentPartID`, `PLListID→ParentPartID`, `PNCurrentCost→CurrentCost`, `PNLastRollupCost→LastRollupCost` |
| `SupplierPart` | `supplier_part` (+ part join) | removed redundant joined `PNID` (identical to `PartID` via the JOIN); query drops `pn.id`, `POLinks` map keys on `PartID` |

## PR 2 - Purchase-order lines (`POL`) - DONE

Branch `feature/693-field-rename-po-lines`.

`models.PurchaseOrderLine` (`po_line`): `POLID→ID`, `POLPOID→POID`, `POLItem→LineNumber`,
`POLPNPartNumber→PartNumberSnapshot`, `POLRev→RevisionSnapshot`, `POLDesc→Description`,
`POLQty→Qty`, `POLCost→UnitCost`, `POLPNID→PartID`. `PurchaseOrder` itself is already clean.
Files: `models/purchase_order.go`, `pos.go`, `parts.go`, `templates/pos/{po_detail,po_print,po_edit}.html`,
`templates/parts/part_orders.html`, plus the `pos_test.go` model literals.

Left untouched (POST keys / local structs, as planned):
- `case "POL*":` switch in `pos.go` and the matching `name="...[POL*]"` / `[name$="[POL*]"]`
  attributes and JS selectors in `po_edit.html` (and the `pol[..][POL*]` map keys in the tests).
- `rfqCell.POLID` / `rfqScanLine.POLID` local structs (`pos.go`, `pos_test.go`, `rfq_compare.html`) -
  their own `POLID` field, deferred as a non-`models` view struct.
- `VendorPN` (`po_line.vendor_part_number`) - not `POL`-prefixed; left to match `SupplierPart.SupplierPN`.

## PR 3 - Contacts (`CN`) - DONE

Branch `feature/693-field-rename-contacts`. Originally planned to also carry the test-record
convention alignment, but that half is collision-heavy (`Locked`/`Active`/`Approved`/`PNID`
overlap many local structs) and was split into its own PR4 for an isolated integration run.

- `models.Contact` (`contact`): `CNID→ID`, `CNSUID→CompanyID`, `CNName→DisplayName`,
  `CNEmail→Email`, `CNPhone1/2→Phone1/2`, `CNFAX→Fax`, `CNAddress→Address`, `CNCity→City`,
  `CNState→State`, `CNZipcode→Zipcode`, `CNCountry→Country`, `CNWeb→Website`,
  `CNUserAccountLink→UserAccountLink`, `CNActive→IsActive`, `CNNotes→Notes`, `CNDateModified→UpdatedAt`.
  (`DisplayName`/`UpdatedAt` match the actual columns `display_name`/`updated_at` - the earlier
  plan's `Name`/`ModifiedDate` were wrong.)
- `models.Supplier` **joined** `CN*` fields → same targets (SU* stay).
- Local view structs folded in (they only carried `CNID`/`CNName`, and uniform rename beats
  receiver disambiguation): `ContactSummary.CNID/CNName` (pos.go), `siblingContact.CNID/CNName`.
- Left untouched: the `CN*` form POST keys (`name=`/`id=`/`for=` in the contact/supplier/po_edit
  templates, matching `fv(r, "CN*")` in `contactFromForm`), and the lowercase-json-tagged
  contacts API DTO (`row` in contacts.go) - no `CN`-prefixed fields, serves the picker JS.

## PR 4 - Test records (`Locked`/`Active`/`Approved` + `PNID`) - TODO — carries `Closes #693`

Test-record domain convention alignment (tables were renamed from `Forms`/`TestRecords`/
`TestResults`): `TestForm.PNID→PartNumberID`, `Locked→IsLocked`, `Active→IsActive`;
`TestRecord.Locked/Approved/Active→IsLocked/IsApproved/IsActive` (note `TestRecord.PartNumberID`
is already correct); `FormEvent`/`RecordEvent` as needed. Fold the deferred records-domain `PNID`
structs (below) in here. Collision-heavy - the generic field names overlap the deferred local
structs, so rename per-receiver, not by blanket `.Field` replace. This PR carries `Closes #693`.

## Deferred / Open questions (not model structs - decide in PR3 or a follow-up)

These carry `PNID`/short names but aren't `models` types mapped to a column, so PR1 left them:

- `supplierPartSummary.PNID` (suppliers.go), `buildCostLine.PNID` (parts.go),
  `bomRow.PNID` (parts.go, a string form value), `BOMPart.PNID` / `formPN.PNID` (records.go),
  request DTOs `PNID int json:"pnid"` (api.go) / `json:"pnId"` (records.go).
- The `row` list-view struct (parts.go) uses its own short names (`PN`, `Rev`, `ReqBy`,
  `Active`) - a separate naming scheme, left untouched.
- `company.sql` `SUWeb` is a documented dead column (drop is tracked in FUTURE_GOALS.md).
