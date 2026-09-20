# #115 + #116 — CI permissions; seed bcrypt hashes

## #115
- `.github/workflows/test.yml`: add top-level `permissions:\n  contents: read` between `on:` and `jobs:`.
- Resolved: no SHA pinning. govulncheck step already present.

## #116 (resolved: not applicable — no code change)
- Seed files already document `admin/admin` and `tester/tester` as intentional public ArxDev dev logins (seed_test_data.sql headers). Nothing to hide; hashes stay.
- Close #116 with an explanatory comment (after user go-ahead); do not list in PR `Closes`.

## Verify
- Only test.yml changes.
