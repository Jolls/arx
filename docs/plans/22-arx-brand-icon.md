# #22 — Fixed Arx brand icon in the header home link

## Resolved decisions
- The mark is a gear/cog (user's choice). It must not look like the Parts tab gear (`parts.svg`) or the Settings `bi-gear-fill` next to it. So: a cream (`#F1E9D7`) gear outline with a gold (`#C79A55`) letter "A" in the hub. Same palette as the `*-inv.svg` tab icons, so it reads on every `--header-bg` and on the test-mode orange header.
- The white chip goes: the icon sits directly on the header.
- `<link rel="icon">` keeps using `{{.Favicon}}`, so the browser tab icon still changes per section.

## File changes
1. **New** `arx_go/static/shared/icons/arx-brand.svg`: `viewBox="0 0 24 24"`, a gear ring (8 teeth, stroke `#F1E9D7`, width 2, no fill) and an "A" path in the centre (stroke `#C79A55`, width 2, round caps). No text element and no fonts.
2. `arx_go/templates/shared/layout.html:15`: change `<img src="{{.Favicon}}" ...>` to `<img src="/static/shared/icons/arx-brand.svg" alt="" class="app-header-icon">`.
3. `arx_go/static/app.css:74`: `.app-header-icon { width: 20px; height: 20px; margin-right: 8px; }` (drop background/padding/border-radius/box-sizing).

## Test plan
1. Coverage audit: `TestCoreTemplatesParse` and `TestParseTemplates` (`arx_go/templates_parse_test.go`) parse `layout.html`. Nothing asserts the header icon.
2. Characterization: `TestLayout_TabFaviconIsPerSection` asserts the layout source contains `<link rel="icon" type="{{.FaviconType}}" href="{{.Favicon}}">`. Passes today.
3. Red: `TestLayout_HeaderBrandIconIsFixed` asserts the `app-header-home` anchor's `<img src` is `/static/shared/icons/arx-brand.svg` and doesn't contain `{{.Favicon}}`, and that `static/shared/icons/arx-brand.svg` exists in the embedded static FS. Fails today because the anchor uses `{{.Favicon}}` and the file is missing.
4. Manual only:
   - The icon is legible on every accent theme's header and on the test-mode header.
   - It stays the same across tabs.
   - The browser tab icon still changes per section.
