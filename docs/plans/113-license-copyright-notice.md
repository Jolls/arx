# #113 — LICENSE copyright holder + README licence

## Findings
- README.md already has a `## License` section (line 95) naming AGPL-3.0, "Copyright (C) 2026 Jolls", linking LICENSE. Fix step 2 already done — no README change.
- LICENSE line 633 is still the unfilled template (`Copyright (C) <year>  <name of author>`).

## Changes
1. `LICENSE`: prepend a 2-line notice above the (byte-identical) licence text: `Copyright (C) 2026 Jolls` + blank line. Do not touch any other byte (GitHub detection).
2. Per-file headers: decision = not wanted (single-author project); no code change.

## Verify
- `git diff LICENSE` shows only added lines at top.
