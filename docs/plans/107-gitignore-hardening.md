# 107 — Close the .gitignore gaps before publication

Issue: [#107](https://github.com/Jolls/arx/issues/107). Part of the pre-publication
review, [#102](https://github.com/Jolls/arx/issues/102).

## Problem

`.gitignore` covered the exact filenames in use but not near-miss variants. Verified
with `git check-ignore` before the change:

- `.env` ignored, but `.env.local`, `.env.production`, `.env.prod` **not** — `.env` was
  matched by exact name, and `*.local.*` requires a trailing segment so `.env.local`
  fell through. These are the conventional names, so the protection did not cover the
  likeliest accident.
- `arx_go/Arx.exe` ignored, bare `Arx.exe` at the repo root **not**.
- Database exports (`arx-backup-<date>.zip`, `*.dump`, `*.sql.gz`, `*.bak`) **not**
  ignored at all. Settings → Data Backup produces exactly the first of these: a full
  CSV dump of every table. A browser saving one into the repo folder would put the
  entire dataset one `git add .` from a public repo.

Low probability, but the cost of the failure changes completely once the repo is
public: a private repo forgives an accidental commit, a public one does not.

## Changes — `.gitignore` only

Rewritten with the existing entries preserved and grouped under short section comments,
plus:

```
.env*
!.env.example
Arx.exe
Arx.exe~
arx-backup-*.zip
*.dump
*.sql.gz
*.bak
```

`Arx.exe` replaces `arx_go/Arx.exe` — the unanchored pattern matches at any depth, so it
covers the old path and the root case together.

The `!.env.example` negation **must stay after** `.env*`; git applies the last matching
pattern, so reordering them would untrack the example file. Called out in a comment in
the file itself, since it is easy to break during a later tidy-up.

Deliberately **not** adding `*.sql` — the repo tracks its DDL and migrations under
`SQL/`, and that pattern would swallow them.

## Verification (run, passed)

`git check-ignore -q` over each path:

| Path | Result |
|---|---|
| `.env`, `.env.local`, `.env.production`, `.env.prod`, `arx_go/.env` | ignored |
| `.env.example` | **not** ignored (still tracked) |
| `config/local.json`, `arx_go/config/local.json` | ignored |
| `arx-backup-2026-09-18.zip`, `data.dump`, `db.sql.gz`, `notes.bak` | ignored |
| `Arx.exe`, `arx_go/Arx.exe` | ignored |
| `CLAUDE.local.md` | ignored |

Also confirmed `git ls-files -i -c --exclude-standard` returns empty — no
currently-tracked file matches a new pattern, so nothing became accidentally ignored.

## Not done here

The issue also floated a pre-commit hook or CI step failing on a staged file matching
these patterns. Left out: it is a separate mechanism with its own failure modes, and
the ignore rules are the cheap 90%. Worth a follow-up issue if wanted.
