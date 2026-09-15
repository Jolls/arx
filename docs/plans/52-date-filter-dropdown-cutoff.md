# #52 — Date filter dropdown cut off by short table

## Root cause

`arx_go/static/app.js` already works around this exact class of bug for
row-action ("kebab") dropdowns:

```js
// lines 563-573
// Dropdown menus (e.g. row-action kebabs) default to Popper's 'absolute'
// strategy, which is clipped by any scrollable ancestor — .table-wrapper's
// overflow-x:auto clips rows near the bottom of a table. 'fixed' strategy
// positions relative to the viewport instead, escaping that clipping.
document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('[data-bs-toggle="dropdown"]').forEach(el => {
        bootstrap.Dropdown.getOrCreateInstance(el, {
            popperConfig: defaultConfig => Object.assign({}, defaultConfig, { strategy: 'fixed' }),
        });
    });
});
```

`.table-wrapper { overflow-x: auto; }` (`arx_go/static/app.css:133`) computes
`overflow-y: auto` too (CSS overflow spec: a non-`visible` value on one axis
forces `auto` on the other), so it clips Popper's default `absolute`-strategy
popovers vertically — worst when the table is short (e.g. a filter that
matches zero/few rows).

The `fixed`-strategy fix above only reaches dropdowns that exist in the DOM
**at the moment its `DOMContentLoaded` listener runs**. But the date-filter
dropdown markup is generated later, by `initDateFilters()`
(`arx_go/static/shared/app.js:339-364`), which is invoked from a *second*,
later-registered `DOMContentLoaded` listener (line 576). Listener registration
order = execution order, so by the time `initDateFilters()` injects
`.date-filter-btn` dropdown toggles into the `th` elements, the earlier
listener's `querySelectorAll('[data-bs-toggle="dropdown"]')` snapshot has
already run and missed them. Result: date-filter dropdowns keep Popper's
default `absolute` strategy and get clipped by `.table-wrapper`'s computed
`overflow-y: auto` — exactly the bug in #52.

This is timing-dependent, not markup-dependent: `initDateFilters()` is shared
by all 7 templates using `data-filter-type="date"` (`parts/index.html`,
`records/records_index.html`, `parts/part_lot_trace.html`,
`parts/part_unit_trace.html`, `parts/part_records.html`, `pos/pos.html`,
`contacts/contacts.html`), so fixing it once in `app.js` fixes all of them.

## Fix

In `arx_go/static/shared/app.js`:

1. Extract the body of the existing fixed-strategy loop (lines 567-572) into
   a small named function, e.g.:
   ```js
   function useFixedDropdownStrategy(root = document) {
       root.querySelectorAll('[data-bs-toggle="dropdown"]').forEach(el => {
           bootstrap.Dropdown.getOrCreateInstance(el, {
               popperConfig: defaultConfig => Object.assign({}, defaultConfig, { strategy: 'fixed' }),
           });
       });
   }
   ```
2. Keep the existing `DOMContentLoaded` listener (lines 567-573) calling
   `useFixedDropdownStrategy()` for whatever dropdowns already exist at that
   point (row-action kebabs).
3. At the end of `initDateFilters()` (after the `forEach` that builds each
   `.date-filter` dropdown, i.e. right after line 363's closing of the
   `forEach`), call `useFixedDropdownStrategy()` again so the freshly-created
   `.date-filter-btn` toggles get the same `fixed` popper strategy.

This keeps the fix in one place (shared `app.js`), doesn't touch any
template, doesn't reorder unrelated code, and isn't sensitive to future
reordering of the two `DOMContentLoaded` listeners (unlike an alternative
fix of just moving `initDateFilters()` earlier in the file, which would be
more fragile — see Open question).

No CSS change needed — `.table-wrapper`'s `overflow-x: auto` is intentional
(horizontal scroll for wide tables) and `fixed`-strategy popovers already
escape it correctly for the kebab-menu case; the date-filter menu just needs
the same treatment applied at the right time.

## Verification

- `go build ./...` / `go vet ./...` from `arx_go/` (JS isn't covered by Go
  tests; this is a static-asset change with no Go code touched).
- Manual check (describe for user, don't run app): open `/parts`, apply a
  date filter that yields zero or one result rows so the table is short,
  open the date-filter dropdown on a column header — it should render fully
  visible instead of being cut off by the table's bottom edge. Repeat on one
  other affected page (e.g. `/records`) to confirm the shared fix applies
  there too.

## Resolved decision

Option A confirmed with user: extract `useFixedDropdownStrategy()` and call
it a second time at the end of `initDateFilters()`, as described in "Fix"
above.
