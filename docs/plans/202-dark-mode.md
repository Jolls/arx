# #202 — Dark mode (OS default + manual override)

## Resolved decisions
- Uses Bootstrap 5.3's `data-bs-theme` on `<html>`.
- The toggle has three states: Auto, then Light, then Dark, then back to Auto. Auto removes the saved choice and follows `prefers-color-scheme`, including live OS changes.
- The choice is saved in `localStorage` under `arx.theme` (`light`/`dark`; absent means auto). Every access is wrapped in try/catch, the same pattern as `app.js`.
- The login page follows the theme but has no toggle. The print pages (`po_print.html`, `record_print.html`) stay light and are not touched.
- The header stays dark in both themes, since it's already dark. The accent themes are unchanged.

## File changes
1. **New** `arx_go/static/shared/theme.js`, loaded synchronously in `<head>` (not deferred) so there's no flash of the wrong theme:
   - `pref()` reads `arx.theme`. `apply()` sets `document.documentElement.dataset.bsTheme` to the saved value, or else to `matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'`.
   - Calls `apply()` straight away, and again on the media query's `change` event when there's no saved value.
   - On `DOMContentLoaded`, wires every `[data-theme-toggle]` button: a click cycles auto → light → dark → auto, saves the value (auto = `removeItem`), calls `apply()`, then updates the button's icon and title. Icons are `bi-circle-half`/`bi-sun-fill`/`bi-moon-fill`; titles are "Theme: Auto"/"Theme: Light"/"Theme: Dark".
2. `arx_go/templates/shared/layout.html`:
   - In `<head>`, before the Bootstrap CSS: `<script src="/static/shared/theme.js?v={{.AppVersion}}"></script>`.
   - In `.header-actions`, before the settings link: `<button type="button" class="header-theme-toggle" data-theme-toggle title="Theme: Auto"><i class="bi bi-circle-half"></i></button>`.
3. `arx_go/templates/shared/login.html`: add the same `<head>` script tag (no toggle).
4. `arx_go/static/app.css`:
   - `.header-theme-toggle`: styled like `.header-settings-link` (`background:none; border:0; padding:0;`).
   - Swap the hardcoded light colours for Bootstrap variables that adapt to the theme:
     - Light backgrounds (`#fff`, `#fafafa`, `#f9f9f9`, `#f8f9fa`, `#f8f8f8`, `#f0f0f0`, `#f1f3f5`, `#f0f4ff`) become `var(--bs-body-bg)` or `var(--bs-tertiary-bg)` (hover states: `var(--bs-secondary-bg)`).
     - Borders (`#ddd`, `#e0e0e0`, `#dee2e6`, `#c7d2fe`, `#f3f4f6`) become `var(--bs-border-color)`.
     - Text (`#333`, `#444`, `#555`) becomes `var(--bs-body-color)`; `#666`, `#888`, `#999`, `#bbb`, `#6c757d` become `var(--bs-secondary-color)`.
     - Highlights: `#fff3cd`/`#fff8e1` become `var(--bs-warning-bg-subtle)`, `#e8f4fd` becomes `var(--bs-info-bg-subtle)` (text `var(--bs-info-text-emphasis)`), `#cfe2ff` becomes `var(--bs-primary-bg-subtle)`, `.ph-grid` becomes `var(--bs-border-color-translucent)`, and `.ph-point`'s stroke becomes `var(--bs-body-bg)`.
     - `.row-heading-3` becomes `var(--bs-secondary-bg)` with `var(--bs-body-color)`.
     - Leave these alone: accent/status colours, the header rules, the swatches, the fixed badges, `.row-heading-1/2`, and `.ph-tooltip`.
   - New rules:
     - `[data-bs-theme=dark] .tab .tab-icon-default { display:none }` and `[data-bs-theme=dark] .tab .tab-icon-active { display:inline }`, so inactive tabs use the light icons.
     - `[data-bs-theme=dark] .table-light { --bs-table-bg: var(--bs-tertiary-bg); --bs-table-color: var(--bs-body-color); --bs-table-border-color: var(--bs-border-color); }`.
5. Templates (non-print). Replace the inline light colours:
   - `style="...color:#666|#888|#999|#aaa..."`: drop the `color:` declaration and add class `text-body-secondary`. `not_found.html`'s `#ddd` gets the same treatment.
   - `color:#333`: drop the declaration (inherit).
   - `background:#f5f5f5|#fafafa|#f8f9fa|#f8f8f8`: drop it and add `bg-body-tertiary`.
   - `po_detail.html:281`: `background:#f0f4ff; border-top:1px solid #c7d2fe` becomes class `bg-primary-subtle`, with `border-top:1px solid var(--bs-border-color)`.
   - `bg-light` becomes `bg-body-tertiary` (at `records_show.html:242,343,357`, `partials.html:161` and `rfq_compare.html:26`), and `text-dark` next to it becomes `text-body`.
   - Files: `contacts.html`, `parts/{index,mfg_parts,part_build,part_lot_trace,part_records,part_transactions,part_unit_trace}.html`, `pos/{pos,po_detail,po_edit,rfq_compare}.html`, `records/{index,records_index,records_show,test_report}.html`, `settings/{settings,utilities,whats_new}.html`, `shared/{local_dir,login,not_found,partials}.html`, `suppliers/{suppliers,supplier_edit,supplier_parts}.html`.
   - Leave alone: `#c00`/`#c0392b` (error red), `#f5a623` (star), `#198754` (best-price green), and `part_sourcing.html` (already uses a variable).

## Test plan
1. Coverage audit: the template parse tests in `arx_go/templates_parse_test.go`. Nothing covers theming.
2. Characterization: none needed. The CSS and template swaps change no Go-visible behaviour, and the parse tests catch template breakage.
3. Red:
   - `TestLayout_ThemeScriptInHeadBeforeCSS`: `layout.html` and `login.html` load `/static/shared/theme.js` inside `<head>`, before `bootstrap.min.css`, and the script has no `defer`/`async`. Fails today because there's no script.
   - `TestLayout_HasThemeToggle`: `layout.html` contains `data-theme-toggle`. Fails today.
   - `TestTemplates_NoHardcodedLightColors`: for every non-print template, rejects `bg-light` and inline `style` colours `#333|#444|#555|#666|#888|#999|#aaa|#ddd|#f5f5f5|#fafafa|#f8f9fa|#f8f8f8|#f0f4ff`. This regression guard fails today on the sites listed above.
4. Manual only:
   - Visual pass of every tab, the detail sub-pages, Settings, Login and What's New in dark mode.
   - The toggle cycles through its three states, the choice persists across reloads, and Auto follows a live OS change.
   - No flash of the wrong theme on load.
   - Charts (price history), hover previews, dropdowns, the records timeline, and the highlighted rows all look right.
