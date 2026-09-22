# Plan: #149 — CLAUDE.md stale line-ending guidance + dangling import

Single file change: `CLAUDE.md`. No other files touched.

## Change 1 — line 36 (line-endings paragraph under "## Tooling gotchas (Windows + go.work)")

**Before (exact current text, line 36):**
```
- No bulk file rewrites (`gofmt -w`, `sed -i`). Repo is NOT gofmt-clean and the working tree is mixed CRLF/LF (`core.autocrlf=true`, no `.gitattributes`). Whole-file rewrites reflow unrelated code and/or flip CRLF→LF — noisy diffs that violate surgical-change discipline. Edit via the Edit tool (`replace_all` per file for bulk renames); it preserves line endings. `gofmt -l` flags CRLF files as "unformatted" but the pending diff is just line-ending churn, not real formatting — don't chase it. Verify builds with `build.bat`, not gofmt.
```

**After (exact replacement text):**
```
- No bulk file rewrites (`gofmt -w`, `sed -i`). Repo is NOT gofmt-clean. `.gitattributes` normalizes source files to LF in-repo (`* text=auto eol=lf`, plus explicit `eol=lf` for `.go`/`.sql`/`.md`/etc., `eol=crlf` for `.bat`/`.ps1`/`.cmd`), but a whole-file rewrite still reflows unrelated code and produces a noisy diff that violates surgical-change discipline — edit via the Edit tool instead (`replace_all` per file for bulk renames). Verify builds with `build.bat`, not gofmt.
```

**What changed and why:**
- Removed the false premise "the working tree is mixed CRLF/LF (`core.autocrlf=true`, no `.gitattributes`)" — a `.gitattributes` exists and normalizes to LF.
- Replaced it with a factual statement that `.gitattributes` normalizes source files to LF in-repo.
- Dropped "and/or flip CRLF→LF" from the whole-file-rewrite consequence, since normalization now prevents that failure mode — kept "reflow unrelated code" as the remaining, still-valid reason to avoid whole-file rewrites.
- Dropped the sentence "`gofmt -l` flags CRLF files as 'unformatted' but the pending diff is just line-ending churn, not real formatting — don't chase it." entirely — this only applied to the mixed-CRLF world and no longer has a live premise.
- Kept "No bulk file rewrites... Edit via the Edit tool" as the operative rule, since noisy diffs from whole-file reflow are still a valid concern independent of line endings.
- Kept "Verify builds with `build.bat`, not gofmt." unchanged (still valid, unrelated to the false premise).

## Change 2 — line 16 (`@CLAUDE.local.md` import)

**Before (exact current text, lines 15–16):**
```
# Arx Parts Master
@CLAUDE.local.md
```

**After (exact replacement text):**
```
# Arx Parts Master
@CLAUDE.local.md
(`CLAUDE.local.md` is a maintainer-local file, gitignored and not committed — this import only resolves for the maintainer; it is not present in the public repo.)
```

Insert this as a new line immediately after line 16 (the `@CLAUDE.local.md` line), before the blank line that currently follows it (current line 17).

## Verification

- Diff should touch only `CLAUDE.md`, only the two spans above.
- No build/test impact (docs-only change) — no build/vet/test run needed.
- Re-read the two edited spans after applying to confirm exact text matches above and surrounding lines are untouched.

## Open questions

- None blocking. (Out of scope, per issue: whether CLAUDE.md should remain the public architecture reference vs. CONTRIBUTING.md taking that role — not addressed here.)
