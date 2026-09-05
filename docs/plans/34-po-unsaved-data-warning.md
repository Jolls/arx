# #34 — Warning when navigating off the PO edit form with unsaved data

## Investigation summary

- `arx_go/templates/pos/po_edit.html` is a plain server-rendered form (`<form id="po-form" method="post">`), used for all 5 render sites in `arx_go/pos.go` (lines 463, 668, 928, 2025, 2074): New PO, Edit PO, New RFQ, RFQ Add-Supplier-Quote, Duplicate PO. One template, one inline `<script>` block (lines 341–603) — no separate JS file for this page.
- The whole app (`arx_go/templates/shared/layout.html`, `arx_go/static/shared/app.js`) is a classic multi-page app: every nav element (top tabs in `layout.html` lines 27–34, the `po_tabs` sub-tabs in `templates/shared/partials.html` lines 92–114, the breadcrumb, the Cancel link) is a plain `<a href>`. `app.js` was read in full — it does list-row rendering, filters, BOM expand/collapse, tooltips; there is **no client-side router or link-click interception** anywhere in the codebase (confirmed zero `beforeunload` hits repo-wide, and no `pushState`/fetch-based nav).
- **Consequence: this resolves the "browser close/refresh vs. in-app nav" ambiguity by itself.** Because there is no SPA routing, clicking any link on this page (top nav tab, `po_tabs` sub-tab, breadcrumb, Cancel button) *is* a real full-page navigation — the same `beforeunload` event that fires on tab-close/refresh/typed-URL/back-button also fires for every one of those link clicks. A single `window.beforeunload` listener, gated on a dirty flag, covers every case in the task description with no extra wiring per link.
- RFQ variant: needs no separate work. `IsRFQ`/`RFQAddFrom`/`IsDuplicate` all render the same `po_edit.html` and share the same inline script, so the guard applies automatically to New RFQ and Add-Supplier-Quote too.

## Dirty-state tracking approach

Flag-based, not full-form-serialization (simplest option that covers every mutation path already present in this file — no need to diff serialized form data).

1. **Declare the flag.** In the existing inline `<script>` block, immediately after the existing line
   `let newLineIdx = {{if .POItems}}{{len .POItems}}{{else}}0{{end}};` (currently line 342), add:
   ```js
   let formDirty = false;
   ```

2. **Set it on any field edit.** Immediately after the `formDirty` declaration (script runs at the bottom of `<body>`, after the `</form>`, so `getElementById` works synchronously here — same pattern already used at the existing top-level line `document.getElementById('line-items-tbody').addEventListener('input', ...)`, currently lines 383–385), add:
   ```js
   const poForm = document.getElementById('po-form');
   poForm.addEventListener('input', () => { formDirty = true; });
   poForm.addEventListener('change', () => { formDirty = true; });
   ```
   `input` covers every text/number/date/textarea field and the supplier/receiver typeahead boxes as the user types. `change` covers the two `<select>` contact dropdowns (`supplier_contact`, `receiver_contact`), whose `onchange` already calls `fillContactFields` — the native `change` event still fires and bubbles to the form regardless of that inline handler.

3. **Set it on line-item structural changes** (these don't fire `input`/`change` on the form, since they add/remove DOM nodes or append a hidden field programmatically):
   - In `addNewLine()` (currently lines 353–367), add `formDirty = true;` right after `tbody.appendChild(clone);` (line 361).
   - In `removeExistingLine(btn, polid)` (currently lines 388–397), add `formDirty = true;` as the first line of the function body (before `const hidden = ...`).
   - Two existing inline handlers remove a *newly-added, not-yet-saved* row directly: the `new-line-tpl` template's remove button (currently line 336, `onclick="this.closest('tr').remove()"`) and the `DuplicateItems` loop's remove button (currently line 286, same `onclick`). Change both occurrences to:
     ```html
     onclick="this.closest('tr').remove(); formDirty = true;"
     ```
     (Two separate `Edit` calls — the two lines are not textually identical since they sit in different surrounding markup, so `replace_all` isn't applicable; edit each occurrence individually.)

4. **Add the `beforeunload` listener.** Anywhere at the top level of the script (e.g. directly after the block added in step 2), add:
   ```js
   window.addEventListener('beforeunload', (e) => {
       if (!formDirty) return;
       e.preventDefault();
       e.returnValue = '';
   });
   ```
   Note: all modern browsers ignore any custom string here and show their own fixed generic message ("Leave site? Changes you made may not be saved.") — `e.returnValue = ''` is the standard cross-browser way to trigger that native prompt; there is no way to customize the wording, so don't try.

5. **Don't warn on the user's own intentional Save.** The existing submit handler (currently lines 576–595) already has exactly one code path where it lets the browser proceed with the real POST — the early `if (!invalid.length) return;` (line 583). Change that line to:
   ```js
   if (!invalid.length) { formDirty = false; return; }
   ```
   This must NOT be applied to the failure path (the `e.preventDefault()` branch below it) — that path doesn't navigate, so the flag must stay as-is (dirty) so the warning still protects the user if they abandon the page after a failed validation instead of fixing it.

No other file changes are needed — this is entirely contained in `arx_go/templates/pos/po_edit.html`.

## New PO vs. Edit PO vs. RFQ

- **New PO / New RFQ** (`.IsNew`): form starts with all fields empty and zero rows (or duplicate-seeded rows via `.DuplicateItems`, which per step 3 above already count as a real change once removed, and — being pre-populated with real values via the duplicate-source data — should also be treated as dirty from page load, see Open Question 2 note below... *actually not needed*: duplicate rows are inert until touched, consistent with the rest of the form; no special-case required).
- **Edit PO**: existing `.POItems` rows are rendered plain (no dirty flag from initial render); only user edits trigger `input`/`change` per step 2, and remove/add per step 3.
- **RFQ Add-Supplier-Quote** (`.RFQAddFrom`): same template/script, guard applies with no extra code.
- **Duplicate PO** (`.IsDuplicate`): same template/script, guard applies with no extra code.

## Open questions

1. **Should navigating to another `po_tabs` sub-tab of the *same* PO (Details / Print-PDF / Folder) while the Edit form is dirty also trigger the warning, or should only navigating fully away (top nav tabs, browser back/close, typed URL) warn?** Mechanically these are indistinguishable to a global `beforeunload` listener unless we special-case the `po_tabs` links.
   - **Option A:** Warn on any navigation away from the Edit form, including the PO's own Details/Print/Folder sub-tabs. Zero extra code — the plan above already does this.
   - **Option B:** Exempt the 3 `po_tabs` sub-tab links (Details/Print/Folder) from the warning by adding an `onclick="formDirty = false;"` (or similar) to those 3 anchors in `templates/shared/partials.html`, so only navigation genuinely leaving the PO record prompts.

2. **Should the "Cancel" button on the edit form itself bypass the warning** (since clicking Cancel already signals "I want to discard my changes"), or should it also trigger the browser's leave-page confirmation like any other navigation?
   - **Option A:** Cancel also warns — simplest, no special-casing, and arguably correct since Cancel is exactly the action that would lose the data the issue is about.
   - **Option B:** Cancel bypasses the warning — add `onclick="formDirty = false;"` to the Cancel `<a>` (currently `templates/pos/po_edit.html` lines 319–320), treating the click as explicit user consent to discard.

The plan above implements Option A for both (do nothing extra) as the default/simplest path; flip to Option B for either by adding the one-line `onclick` noted.

## Resolved decisions

- **Sub-tab navigation (Q1): Option A.** Warn on any navigation away, including the PO's own Details/Print/Folder sub-tabs. No extra code beyond the plan above.
- **Cancel button (Q2): Option A.** Cancel also warns like any other nav. No extra code beyond the plan above.

## Verification / manual test plan

No automated test suite covers client-side JS in this codebase (per project conventions) — manual browser testing only. `go build`/`go vet` still confirm the Go side (templates/routes) wasn't broken; no Go code changes are expected here so this is a pure template edit.

1. **New PO, no edits, cancel:** `/pos` → New PO → immediately click Cancel. No warning (nothing was touched).
2. **New PO, dirty, browser back:** New PO → type a supplier name or add a line item → click browser Back. Confirm the native "leave site" prompt appears; choosing "Stay" keeps you on the page with your data intact; choosing "Leave" discards it.
3. **New PO, dirty, top nav tab:** New PO → edit a field → click "Parts" in the top nav. Confirm the prompt appears.
4. **New PO, dirty, tab close/refresh:** New PO → edit a field → refresh (F5) or close the tab. Confirm the browser's native prompt appears.
5. **New PO, dirty, then Save:** New PO → fill required fields (supplier + receiver via typeahead, at least one line item) → click "Create PO". Confirm **no** warning fires and the save completes normally (this exercises the `formDirty = false` reset on successful validation).
6. **New PO, dirty, failed client validation:** New PO → type free text into the Supplier field without picking a dropdown result → click "Create PO". Confirm the existing red validation banner still appears (unrelated to this change) and — separately — that if you then try to navigate away without fixing it, the unsaved-data warning still fires (validation failure must not have cleared the dirty flag).
7. **Edit PO, no edits, navigate away:** Open an existing PO's Edit tab → click Details/Print/Folder/Cancel without touching anything. No warning.
8. **Edit PO, edit a field, navigate away:** Open Edit tab → change a value (e.g. Notes) → click any nav link/tab. Confirm the warning appears.
9. **Edit PO, add a line item, navigate away:** Open Edit tab → "+ Add Line Item" → navigate away without saving. Confirm the warning appears (covers the `addNewLine` dirty-flag path).
10. **Edit PO, remove an existing line item, navigate away:** Open Edit tab → click the remove (×) button on an existing line → navigate away without saving. Confirm the warning appears (covers the `removeExistingLine` dirty-flag path).
11. **Edit PO, add then remove a new (unsaved) line item, navigate away:** Open Edit tab → "+ Add Line Item" → click that new row's own × to remove it again → navigate away. Confirm the warning still appears (the add already set the flag; also directly exercises the modified `onclick` on the new-line-tpl remove button).
12. **Edit PO, save changes:** Open Edit tab → change a value → click "Save Changes". Confirm no warning fires and the save completes.
13. **RFQ New:** `/pos` → New RFQ → repeat test 2 or 3 to confirm the guard applies there too.
14. **RFQ Add Supplier Quote:** From an open RFQ, "Add Supplier Quote" → edit a field → navigate away. Confirm the warning appears.
15. **Duplicate PO:** From an existing PO, "Duplicate PO" → edit a field (or remove one of the pre-populated duplicate line items) → navigate away. Confirm the warning appears.

If the team resolves either Open Question toward "Option B," add corresponding manual tests: clicking the exempted link(s) while dirty should NOT prompt.
