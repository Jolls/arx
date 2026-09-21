# Plan #20: Resizable table columns (per-table, persisted)

Assumes #126 (test report on shared engine) is applied first. Files touched: `arx_go/static/shared/app.js`, `arx_go/static/app.css`, `arx_go/templates/records/records_index.html`, `CHANGELOG.md`. No Go or template markup changes; the `col:nth-child` percent rules stay as the default widths.

## Findings that shape the design
- **Colgroups:** parts, suppliers, contacts and POs have a `<colgroup>`. `initColumnOrder` (app.js ~673) freezes each `<col>` to an inline `%` width and tags it with `data-ci`. records, part_records, part_unit_trace, part_lot_trace and the test report have no colgroup.
- **Hidden columns and cols:** `.col-hidden` (`display:none`) is applied only to `th`/`td` carrying `data-col`. The `<col>` elements have no `data-col`, so hiding a column today leaves its `<col>` in place and later columns can pick up the wrong widths. Fixing this is needed for "hide/show doesn't misapply widths".
- **Records select column:** `#records-table [data-col="col-select"]` in app.css (lines 406-407) toggles `display` via `.show-select`. `updateSelectColumn` in `records_index.html` (~line 133) sets that class dynamically.
- **Table keys:** suppliers, contacts and POs tables have no `id`. `colOrderKey` already uses `table.id || window.location.pathname`, and the widths key reuses that.
- **Dropdown Reset:** the Columns dropdown (`data-col-table`, `data-col-reset`) exists only on parts and records among the listed tables.
- **Sort and drag hooks:** header click sort is bound in `loadListRows` (~line 528). Drag-reorder sets `th.draggable = true` at ~line 745.
- **Layout:** `table[data-rows-url] { table-layout: fixed }` and `.table-wrapper { overflow-x: auto }` already exist.

## Layout spec
- The table stays `table-layout: fixed`.
- **Default mode:** no saved widths. Nothing changes from today: percent `<col>` widths, table at 100%.
- **Px mode:** any saved width exists. Every `<col>` gets an inline px width and `table.style.width` is set to the sum of the visible cols' px widths.
  - The table grows or shrinks and `.table-wrapper` scrolls horizontally.
  - Resized columns use their saved px. Others use `dataset.defaultPx`, measured at init.
  - The pinned `col-select` col always uses its fixed px.
- **Reset:** restores each `<col>`'s `dataset.defaultWidth` (the original percent) and clears `table.style.width`.
- **Hiding:** hiding a column hides its `<col>` too, so it drops out of the grid. `syncColWidths` recomputes the sum over cols whose computed `display` is not `none`.
- **Minimum width:** the constant `COL_MIN_W = 40` px.

## app.js changes

### 1. New state, next to the `COL_PINNED` block (~line 626)
- `const COL_MIN_W = 40;`
- `let colWidths = {};`, `let colWidthsKey = '';`, `let colTable = null;`.

### 2. `initColumnOrder` (~line 673)
- Compute `const tableKey = table.id || window.location.pathname;`. Use it for `colOrderKey`, and set `colWidthsKey = 'arx.colw.' + tableKey`.
- If a colgroup exists with a mismatched count, remove it as now. If no colgroup exists, create `<colgroup>` with `keys.length` empty `<col>` and insert it as the table's first child.
- Replace the freeze loop so it runs for every col `c` at index `i`. `th` is the first-row header with `data-ci == i`. Width `w` is `th.getBoundingClientRect().width`, and `tw` is measured before any change.
  - Set `c.dataset.ci = i`, `c.dataset.col = keys[i]`, `c.dataset.defaultPx = Math.round(w)`.
  - If `keys[i] === COL_PINNED`: use `pinnedPx = parseFloat(th.style.width) || w`. Set `c.dataset.defaultPx = pinnedPx` and `c.dataset.defaultWidth = pinnedPx + 'px'`. (The records select `th` has an inline `width:36px` and can measure 0 while hidden.)
  - Otherwise: `c.dataset.defaultWidth = (w / tw * 100).toFixed(2) + '%'`.
  - Finally `c.style.width = c.dataset.defaultWidth`.
  - Setting `data-col` on the cols makes the existing `applyState` in the visibility code (`[data-col]` selectors) toggle `col-hidden` on them automatically.
- Call a new `initColumnResize(table)` after the drag-reorder `heads.forEach` block and before `applyColumnOrder()`.

### 3. New `initColumnResize(table)` and helpers, after `initColumnOrder`
- **Load and sync**
  - Set `colTable = table`.
  - Read and JSON-parse `colWidthsKey`, keeping only entries where the key is in `colKeys`, is not `COL_PINNED`, and the value is a finite number >= `COL_MIN_W`.
  - Call `syncColWidths()`.
  - Set `window.syncColWidths = syncColWidths` and `window.resetColWidths = function () { colWidths = {}; save(); syncColWidths(); }`.
  - `save()` calls `localStorage.removeItem(colWidthsKey)` when `colWidths` is empty, else `setItem(colWidthsKey, JSON.stringify(colWidths))`. Both are wrapped in try/catch like the existing order code.
- **`syncColWidths()`**
  - If `colWidths` is empty, set every `col.style.width = col.dataset.defaultWidth`, set `colTable.style.width = ''`, and return.
  - Otherwise, for each col: `px = key === COL_PINNED ? +dataset.defaultPx : (colWidths[key] ?? +dataset.defaultPx)`. Set `style.width = px + 'px'` and add `px` to the sum if `getComputedStyle(col).display !== 'none'`.
  - Then set `colTable.style.width = sum + 'px'`.
- **Handles**
  - For each `thead tr:first-child th` whose key is not `COL_PINNED`, add the class `position-relative` to the `th`.
  - Append `<span class="col-resize-handle position-absolute top-0 bottom-0 end-0">`.
- **Handle listeners**
  - `pointerdown`:
    - Ignore if `e.pointerType === 'mouse' && e.button !== 0`.
    - Call `e.preventDefault()` and `e.stopPropagation()`.
    - Set `th.draggable = false`.
    - Call `handle.setPointerCapture(e.pointerId)`.
    - Record `startX = e.clientX`, `startW = th.getBoundingClientRect().width`, `moved = false`.
    - If `colWidths` is empty, refresh `dataset.defaultPx` for each visible (non-`display:none`) col from its th's measured width. This avoids a jump when entering px mode.
  - `pointermove` (only while captured):
    - `dx = e.clientX - startX`. Do nothing if `dx === 0` and `!moved`.
    - Set `moved = true` and `colWidths[key] = Math.max(COL_MIN_W, Math.round(startW + dx))`.
    - Call `syncColWidths()`.
  - `pointerup` and `pointercancel`:
    - Release the pointer capture.
    - Set `th.draggable = true`.
    - If `moved`, call `save()`.
  - `click`: `e.stopPropagation()`. This stops the header-sort listener.
  - `dblclick`: `e.stopPropagation()`, `delete colWidths[key]`, then `save()` and `syncColWidths()`.

### 4. Column-visibility block (~line 774+)
- End of `applyState`: `if (table === colTable) syncColWidths()`.
- In the `resetBtn` click handler, after `applyState()`: `if (table === colTable && window.resetColWidths) window.resetColWidths()`.
- `hidden` state and `arx.cols.<tableId>` handling stay unchanged.

## app.css changes
- Add next to the `th.col-drag-over` rule (~line 393): `.col-resize-handle { width: 8px; cursor: col-resize; touch-action: none; }` and `.col-resize-handle:hover { background-color: rgba(255,255,255,0.3); }`.
- Lines 406-407: scope the selectors to cells so the new `<col data-col="col-select">` never gets `display: table-cell`. Change them to `#records-table th[data-col="col-select"], #records-table td[data-col="col-select"] { display: none; }` and `#records-table.show-select th[data-col="col-select"], #records-table.show-select td[data-col="col-select"] { display: table-cell; }`. Add a matching rule `#records-table col[data-col="col-select"] { display: none; }` plus `#records-table.show-select col[data-col="col-select"] { display: table-column; }` so the col follows the same show/hide.

## records_index.html
In `updateSelectColumn` (~line 133), after `table.classList.toggle('show-select', !!mode);` add `if (window.syncColWidths) window.syncColWidths();`.

## CHANGELOG.md
Add a new top entry that bumps the patch version (currently v0.7.55), matching the existing entry format. It should say: resizable columns, widths persisted per table under `arx.colw.<tableId>`, hidden columns no longer misalign widths. Refs #20.

## Manual verification (do not run the app)
Run `go build` and `go vet` in `arx_go`. Then check the following by hand on parts, suppliers, contacts, POs, records, test report, part_records, part_unit_trace and part_lot_trace:
- Drag a header edge; only that column changes and the wrapper scrolls horizontally.
- Widths survive a reload and are independent per table.
- A resized column keeps its width after being reordered.
- Hiding and showing columns (parts, records) keeps widths aligned.
- Double-click a handle resets that column, and the parts/records dropdown Reset clears all widths.
- Clicking or dragging a handle triggers neither sort nor reorder.
- Sort and filter URLs are unchanged.
- `arx.cols.*` and `arx.colorder.*` are unaffected.
- The records select column has no handle and keeps 36px.

## Resolved decisions
1. **Reset-all control:** add a separate "Reset column widths" button on every resizable table (not folded into "Reset column order", which stays order-only). Spec:
   - New `let colWidthsBar = null;` beside `colResetBar`.
   - In `initColumnOrder`, right after `colResetBar` is created, create `colWidthsBar` the same way: `button`, `type="button"`, `className = 'btn btn-sm btn-outline-secondary d-none'`, `innerHTML = '<i class="bi bi-arrow-counterclockwise"></i> Reset column widths'` (same icon as the order button), click handler calls `window.resetColWidths()`.
   - Placement: in the toolbar branch, `group.prepend(colWidthsBar)` immediately after `group.prepend(colResetBar)`; in the fallback `bar` branch, `bar.appendChild(colWidthsBar)` after `bar.appendChild(colResetBar)` and give `colWidthsBar` the extra class `ms-2` in that branch only.
   - `syncColWidths()` toggles it: `colWidthsBar.classList.toggle('d-none', Object.keys(colWidths).length === 0)` (run on every call, including the early-return empty branch).
   - The Columns dropdown Reset (parts, records) still calls `window.resetColWidths()` as specified above.
2. **`COL_MIN_W`:** 40 px.

## Open questions
None.
