# 879 — "Receive All" button on PO detail

## Summary

Add a "Receive All" button next to the existing "Receive" button on the PO
detail page's receive form. Clicking it fills every line's `recv[<ID>]` input
with that line's remaining quantity (`Qty - ReceivedQty`), skipping lines
already fully received, then submits the form — reusing the existing
`POReceive` handler and `parseReceiveDeltas` (`arx_go/pos.go`) unchanged. No
backend/Go changes. Pure template + inline JS change.

## Why no backend change

`parseReceiveDeltas` (arx_go/pos.go:1489-1510) already reads whatever is
posted in each `recv[<ID>]` field and ignores blank/zero entries. `POReceive`
(arx_go/pos.go:1517-1627) doesn't care whether the deltas came from a human
typing values or from JS pre-filling them. So "receive all" is entirely a
client-side convenience for populating the same fields the manual flow uses.

## File changes

### `arx_go/templates/pos/po_detail.html`

1. **Line 247** — add `data-qty` and `data-received` attributes to the
   per-line `recv[...]` input so JS can compute the remaining quantity without
   a new template helper:

   ```html
   {{if $canReceive}}<input type="number" step="any" min="0" name="recv[{{.ID}}]"
       class="form-control form-control-sm mt-1 text-end" placeholder="+ qty"
       data-qty="{{.Qty}}" data-received="{{.ReceivedQty}}">
   ```

2. **Line 279-287** (the receipt-date / submit-button row) — add a second
   button, "Receive All", before or after the existing "Receive" button:

   ```html
   {{if $canReceive}}
   <div class="d-flex align-items-end gap-2 mt-2">
       <div>
           <label for="recv-date" class="form-label mb-0">Receipt date</label>
           <input type="date" id="recv-date" name="txn_date" value="{{.Today}}" class="form-control form-control-sm">
       </div>
       <button type="button" class="btn btn-sm btn-outline-primary" onclick="fillReceiveAll(this)">
           <i class="bi bi-box-arrow-in-down"></i> Receive All
       </button>
       <button type="submit" class="btn btn-sm btn-primary"><i class="bi bi-box-arrow-in-down"></i> Receive</button>
   </div>
   </form>{{end}}
   ```

   The new button is `type="button"` so it never submits by itself — see
   Open Question 1 below for what it does after filling the fields.

3. Add a small inline `<script>` block (near the existing scripts at the
   bottom of the file, alongside `openImportModal` — check for a `{{block
   "scripts"}}` or existing `<script>` section and follow that convention)
   defining `fillReceiveAll`:

   ```html
   <script>
   function fillReceiveAll(btn) {
       const form = btn.closest('form');
       form.querySelectorAll('input[name^="recv["]').forEach(function (input) {
           const qty = parseFloat(input.dataset.qty);
           const received = parseFloat(input.dataset.received);
           const remaining = qty - received;
           input.value = remaining > 0 ? remaining : '';
       });
   }
   </script>
   ```

   Adjust exact placement/style to match how other inline scripts are already
   declared in this template (read the file's existing `<script>` block(s)
   before inserting, to match indentation/pattern rather than introducing a
   second unrelated style).

No changes to `arx_go/pos.go`, `arx_go/pos_test.go`, or
`arx_go/integration_test.go` — behavior is exercised through the same
`POReceive`/`parseReceiveDeltas` path already covered by
`TestIntegration_POReceive_PartialThenFull` and
`TestIntegration_POReceive_CreatesLotForLotTrackedPart` (see
`docs/plans/803-po-receive-status-approval-tests.md`).

## Verification

- `cd arx_go; .\build.bat` (build + `go test ./...`) — no Go code touched, so
  this just confirms the template still embeds/parses.
- Manual verification (user performs, per CLAUDE.md — do not `go run`/curl
  routes): open a `sent` or `partially_received` PO with multiple lines
  (some already partially received, one fully received), click "Receive All",
  confirm each open line's box fills with its exact remaining quantity, the
  fully-received line's box stays blank, then click "Receive" and confirm the
  PO updates exactly as a manually-typed full receipt would (status derives
  to `closed`/`partially_received` correctly, inventory ledger rows post).

## Resolved decisions

1. **"Receive All" always fills and submits in one click, unconditionally.**
   No lot-tracked special-casing. `fillReceiveAll` fills every open line's
   `recv[<ID>]` with its remaining quantity, then calls `form.submit()`
   immediately — same one-click interaction model as the existing "Receive"
   button. Vendor-lot number (`vlot[<ID>]`) stays whatever it already was
   (blank by default) — this is already a fully supported, valid state:
   `createLot` (arx_go/pos.go:1588-1596) accepts a blank `VendorLot` and
   `lot_number` auto-defaults to the new lot's own ID (#687). No new
   validation is added, on either button.

2. **Keep "Receive" as-is; add a separate "Receive All" button.** No
   relabeling to "Receive Partial", no dynamic label-swapping. Simplest —
   matches the plan's original file changes below unchanged.

This makes the plan's step 3 script simpler than originally drafted — update
`fillReceiveAll` to submit immediately:

```html
<script>
function fillReceiveAll(btn) {
    const form = btn.closest('form');
    form.querySelectorAll('input[name^="recv["]').forEach(function (input) {
        const qty = parseFloat(input.dataset.qty);
        const received = parseFloat(input.dataset.received);
        const remaining = qty - received;
        input.value = remaining > 0 ? remaining : '';
    });
    form.submit();
}
</script>
```
