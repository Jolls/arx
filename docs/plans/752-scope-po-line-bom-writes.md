# #752 — Scope PO-line and BOM writes to their parent record

Reference pattern confirmed in codebase:
- `arx_go/mfg_parts.go:163-165` (`MfgPartDelete`): `UPDATE %s SET is_active=%s WHERE id=@p1 AND part_id=@p2`, args `mid, id`.
- `arx_go/parts.go:2246-2248` / `2262-2264` (`PriceDeactivate`/`PriceActivate`): `UPDATE %s SET is_active = %s WHERE id = @p1 AND part_id = @p2`, args `priceID, partID`.

Both add the parent-scope column as an extra `AND` clause with the already-resolved parent id appended as the last arg. Apply the same shape below (allowlist-by-id via query, as in `RFQCompareSave`, is not needed here since the parent id is already resolved and a single `AND` guard is sufficient and matches the sibling handlers).

## 1. `arx_go/pos.go` — `POUpdate`

`poID` (int) is already resolved at line 674 (`tx.QueryRowContext(...).Scan(&poID, &priorApproval)`), before both loops below. Add `AND po_id=@pN` to each statement, passing `poID` as the extra trailing arg.

### DELETE — lines 684-690
```go
for _, idStr := range r.Form["delete_pol[]"] {
    if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
        `DELETE FROM %s WHERE id=@p1 AND po_id=@p2`, h.cfg.POLineTable(),
    ), idStr, poID); err != nil {
        h.renderError(w, r, "Error deleting PO line: "+err.Error())
        return
    }
}
```

### UPDATE — lines 705-712
Current statement has placeholders @p1..@p9 (last one, @p9, is `polID`). Add `AND po_id=@p10` and append `poID` as the final arg:
```go
if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
    UPDATE %s SET line_number=@p1, part_number_snapshot=@p2, revision_snapshot=@p3, description=@p4,
                  qty=@p5, unit_cost=@p6, vendor_part_number=@p7, part_id=@p8
    WHERE id=@p9 AND po_id=@p10
`, h.cfg.POLineTable()), item, row.PartNumber, rev, row.Desc, qty, cost, row.VendorPN, pnid, polID, poID); err != nil {
    h.renderError(w, r, "Error updating PO line: "+err.Error())
    return
}
```

No other changes needed in this function — `poID` is already in scope for both loops.

## 2. `arx_go/parts.go` — `PartBOMSave`

Parent id: `id := chi.URLParam(r, "id")` is resolved at line 972, before both loops (delete loop starts at 996, update loop at 1006) — so it's already in scope, no reordering needed. Note `id` is a **string** here (chi URL param), same as how `mfg_parts.go`'s `MfgPartDelete` passes its string `id` URL param directly as the `part_id` arg — follow that, don't convert to int.

Do **not** reuse the `parentID` int variable declared later at line 1032 (`parentID, _ := strconv.Atoi(id)`) for this guard — it's declared after both loops and is only used for the INSERT block; leave it as is.

Column name: BOM rows are scoped by `parent_part_id` (confirmed by the existing INSERT at line 1052: `INSERT INTO %s (parent_part_id, component_part_id, line_number, qty) ...`).

### DELETE — lines 998-1000
```go
for _, plidStr := range r.Form["delete_pl[]"] {
    deleteSet[plidStr] = true
    if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
        `DELETE FROM %s WHERE id=@p1 AND parent_part_id=@p2`, pl,
    ), plidStr, id); err != nil {
        h.renderError(w, r, "Error deleting BOM row: "+err.Error())
        return
    }
}
```

### UPDATE — lines 1024-1026
```go
if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
    UPDATE %s SET line_number=@p1, qty=@p2, component_part_id=@p3 WHERE id=@p4 AND parent_part_id=@p5
`, pl), item, qty, pnid, plidStr, id); err != nil {
    h.renderError(w, r, "Error updating BOM row: "+err.Error())
    return
}
```

## Verification
- `go vet`/`go build` (via `build.bat`) after edits.
- Manual test (user, per CLAUDE.md — do not run the app): edit PO A, submit a `delete_pol[]`/line-id belonging to PO B (e.g. via crafted form/devtools) — confirm PO B's line is untouched and PO A's own delete/update still works normally. Same for BOM: edit part X's BOM, submit a row id belonging to part Y — confirm part Y's BOM row is untouched.
- Suggest a regression test per CLAUDE.md rule 5 (non-obvious cross-tenant scoping bug) — confirm with user before writing: an integration test that creates two POs (or two parts' BOMs), submits an edit to PO A/part X's edit route naming a row id that belongs to PO B/part Y, and asserts PO B/part Y's row is unchanged and no error hides the mismatch.

## Open questions
None — column names (`po_id`, `parent_part_id`) and parent-id variables (`poID` int in pos.go, `id` string in parts.go) are all confirmed directly from existing code in the same functions/sibling handlers, so no guessing was required.
