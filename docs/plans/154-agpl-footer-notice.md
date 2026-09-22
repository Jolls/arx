# #154 — AGPL licence notice in footer

## File changed
`arx_go/templates/shared/layout.html` only. This is the single shared layout partial — nav, `whats-new-banner`, and `</body>` all live directly in this file (no further include/partial split), so no other file needs changes.

## Change
Insert a footer block immediately after the `container-fluid` content div closes (current line 46, `    </div>`) and before the script block (current line 48, `    <script src="https://cdn.jsdelivr.net/...">`).

Current lines 44-48:
```html
    <div class="container-fluid px-4">
        {{template "content" .}}
    </div>

    <script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js" integrity="sha384-YvpcrYf0tY3lHB60NNkmXc5s9fDVZLESaAA55NDzOxhy9GkcIdslK1eN7N6jIeHz" crossorigin="anonymous"></script>
```

New content to insert between the `</div>` (line 46) and the blank line 47:

```html
    <footer class="text-center text-muted small py-3">
        Arx {{.AppVersion}} &mdash; AGPL-3.0. <a href="https://github.com/Jolls/arx">Source</a>
    </footer>
```

Resulting lines 44-51 after the change:
```html
    <div class="container-fluid px-4">
        {{template "content" .}}
    </div>

    <footer class="text-center text-muted small py-3">
        Arx {{.AppVersion}} &mdash; AGPL-3.0. <a href="https://github.com/Jolls/arx">Source</a>
    </footer>

    <script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js" integrity="sha384-YvpcrYf0tY3lHB60NNkmXc5s9fDVZLESaAA55NDzOxhy9GkcIdslK1eN7N6jIeHz" crossorigin="anonymous"></script>
    ...
```

## Notes on the snippet
- Uses existing Bootstrap 5.3 utility classes only (`text-center text-muted small py-3`), no inline styles, no custom CSS — matches CLAUDE.md frontend guidance.
- `{{.AppVersion}}` is already populated in template context (confirmed in use elsewhere in this file, e.g. `?v={{.AppVersion}}`, `dismissWhatsNew('{{.AppVersion}}')`).
- `&mdash;` used for consistency with existing em-dash usage in this file (line 15 `&mdash; TEST MODE`, line 41 `&mdash; <a href="/whats-new">`).
- Link text is "Source" per the issue's suggested wording, pointing at `https://github.com/Jolls/arx` (no target/rel attributes used elsewhere in this file for external links, e.g. the CDN `<link>`/`<script>` tags don't set `rel="noopener"` either — matching existing convention of not adding extra attributes).
- Not linking to the existing `/whats-new` release-notes route from this footer text — the issue's suggested copy is version + licence + source link only; `/whats-new` is already surfaced separately via the `whats-new-banner` div (line 40-43) and isn't part of the AGPL source-offer requirement.

## Verification
- `cd arx_go; .\build.bat` (via PowerShell tool) — build + `go test ./...`.
- Manual (for user, not to be run by the agent): load any page, confirm footer renders below content with version, "AGPL-3.0", and working "Source" link to `https://github.com/Jolls/arx`.

## Open questions
None — file, location, and exact snippet are fully specified above.
