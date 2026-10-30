# #399 — Migrate list pagination to Bootstrap component

## Target markup (replaces the 10 `.pagination-controls` blocks)

```html
<nav aria-label="Pagination" class="d-flex justify-content-between align-items-center mt-3 pt-3 border-top">
    <span class="pagination-info text-body-secondary"></span>
    <ul class="pagination mb-0">
        <li class="page-item"><button type="button" class="page-link page-nav prev" onclick="prevPage()">&#8592; Previous</button></li>
        <li class="page-item"><button type="button" class="page-link page-nav next" onclick="nextPage()">Next &#8594;</button></li>
    </ul>
</nav>
```

Keep each file's existing indentation. `pagination-info` and `page-nav prev|next` stay as **JS hooks only** (no CSS rules).

## File changes

1. **Templates (10 files, one block each):** contacts/contacts.html:49, parts/index.html:75, parts/part_lot_trace.html:155, parts/part_records.html:47, parts/part_unit_trace.html:148, pos/pos.html:55, records/index.html:33, records/records_index.html:100, records/test_report.html:48, suppliers/suppliers.html:41. Replace the `<div class="pagination-controls">…</div>` block with the target markup.
   (Issue text said 12 templates; there are 10 blocks in 10 files.)
2. **static/shared/app.js (~L543-546):** after setting `prev.disabled` / `next.disabled`, also `prev.parentElement.classList.toggle('disabled', prev.disabled)` (same for next). Bootstrap styles `.page-item.disabled`, not `:disabled`.
3. **static/records/app.js:**
   - L66 and L101: `controls.classList.contains('pagination-controls')` → `controls.matches('nav[aria-label="Pagination"]')`.
   - L92-93: same `parentElement` `disabled` class toggle as item 2.
   - `controls.style.display = 'none'/''` stays (works on `<nav>`).
4. **static/app.css L227-235:** delete the `.pagination-controls`, `.pagination-buttons`, `.page-nav` rules (keep `.pagination-info`? No: it is now styled via `text-body-secondary`; delete it too). Keep the `/* --- Pagination --- */` header only if rules remain; remove it with them.

## Test plan

No tests: markup/JS only; no Go tests reference pagination (grep confirmed).

Manual-only:
- Each of the 10 lists: Previous disabled on page 1, Next disabled on last page, "Showing X–Y of N" updates, lists with <=30 rows hide the control (records/app.js tables).
- Light and dark theme: buttons readable, disabled state visibly dimmed.
- Layout: info left, buttons right, border-top spacing similar to before.
