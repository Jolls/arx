# #119 — Mark app_config secrets by `secret_` key prefix

Baseline = option 1 (prefix convention). Key rename: `digikey_client_secret` -> `secret_digikey_client_secret`.
Form field name `digikey_client_secret` (settings.html, `r.FormValue`) is NOT the storage key; unchanged.

## Current state (verified)
- `arx_go/settings.go:686` `const digikeyClientSecretKey = "digikey_client_secret"`; used at `settings.go:297,302` (save; 297 = keep-existing read) and `handlers.go:259` (load into `h.cfg.DigiKeyClientSecret`).
- `arx_go/settings.go:699` `skipAppConfigSecret` matches that one literal (`strings.EqualFold`); wired at `settings.go:676`. Comment at `settings.go:697-698` says "add it here".
- `arx_go/settings_backup_test.go`: `TestSkipAppConfigSecret`, `TestCellTextNormalizesBytes` use the literal.
- No SQL DDL/seed references the key (checked `SQL/`); `SQL/azure/app_config.sql` seeds only `schema_version`, `attachment_categories`. `setting_key` is `VARCHAR(100)`.
- `appConfigGet/Set` are generic (`handlers.go:358,379`); no change needed.

## Changes

1. `arx_go/settings.go`
   - Add `const appConfigSecretPrefix = "secret_"` above `digikeyClientSecretKey`, with comment: any app_config key starting with this is excluded from the backup; new shop-wide credentials must use it.
   - Change `digikeyClientSecretKey` value to `appConfigSecretPrefix + "digikey_client_secret"`; update its doc comment (drop "drift" rationale that is now covered by the prefix; keep note that it is the storage key, not the form field).
   - `skipAppConfigSecret`: `return strings.HasPrefix(strings.ToLower(row["setting_key"]), appConfigSecretPrefix)`. Replace the "add it here" comment with "name new credential keys with `appConfigSecretPrefix`; no edit here needed".
   - No changes at `settings.go:297,302` or `handlers.go:259` (they use the const).

2. `SQL/azure/migrations/<YYYYMMDDHHMMSS>_119_secret_prefix_digikey_client_secret.sql` (timestamp = authoring time)
   - `USE ArxDev;` at top + comment that human changes to ArxProd; single batch, no `GO`.
   - Guarded rename (no dynamic SQL needed, no new columns):
     `UPDATE dbo.app_config SET setting_key = 'secret_digikey_client_secret' WHERE setting_key = 'digikey_client_secret' AND NOT EXISTS (SELECT 1 FROM dbo.app_config WHERE setting_key = 'secret_digikey_client_secret');`
     then delete any leftover old row only if the new key exists: `DELETE FROM dbo.app_config WHERE setting_key = 'digikey_client_secret' AND EXISTS (SELECT 1 FROM dbo.app_config WHERE setting_key = 'secret_digikey_client_secret');`
   - Last step: self-register `IF NOT EXISTS (SELECT 1 FROM dbo.schema_migrations WHERE version_id = <ts>) INSERT INTO dbo.schema_migrations (version_id, is_applied) VALUES (<ts>, 1);` (`TestMigrationsSelfRegister` enforces).
   - Does not bump `app_config.schema_version` (non-breaking for the binary; see Open question 2).

3. `SQL/SCHEMA.md` — add a sentence to the `app_config` mention (line ~95 table row) : keys prefixed `secret_` are credentials and are excluded from the Settings backup. No DDL/seed changes (no new table/column).

4. `arx_go/settings_backup_test.go`
   - `TestSkipAppConfigSecret`: replace literal cases with: `secret_digikey_client_secret` dropped; `secret_anything_new` dropped; `SECRET_Mixed_Case` dropped; `digikey_client_id` kept; `attachment_categories`, `schema_version`, `company_logo`, `something_new`, empty key kept; `mysecret_x` and `digikey_client_secret` (legacy, unprefixed) kept (documents prefix-only match).
   - `TestCellTextNormalizesBytes`: use `"secret_digikey_client_secret"` in the literals/end-to-end row.
   - Add assertion that `strings.HasPrefix(digikeyClientSecretKey, appConfigSecretPrefix)` so the DigiKey key cannot drift out of the prefix.

5. `CHANGELOG.md` — new top entry `## [0.7.54] - <date>` (patch bump) with:
   - `### Changed` — "app_config credentials are now identified by a `secret_` key prefix and excluded from the Settings backup automatically; the DigiKey client secret key was renamed to `secret_digikey_client_secret` (run migration before upgrading) ([#119](https://github.com/Jolls/arx/issues/119))"

6. Verify: `cd arx_go; go build ./... ; go vet ./... ; go test ./...` (via PowerShell). Manual (user): run migration on ArxDev, save DigiKey creds in Settings, export backup, confirm `app_config.csv` has no `secret_*` row and still has other rows.

## Open questions
1. Option 1 vs 2 — recommend option 1 (prefix): no schema change, matches issue's "right ratio", one row exists today. Option 2 (`is_secret` column) needs migration + DDL/seed/SCHEMA.md/Postgres updates + `appConfigSet` signature change; revisit only if credentials proliferate. Confirm option 1.
2. Rename migration is required (a read-fallback alone leaves the deployed legacy `digikey_client_secret` row exported, since it lacks the prefix). Consequence: binary and migration must be applied together — if the new binary runs before the migration, DigiKey reads an empty secret until re-entered in Settings, and the legacy row still exports. Accept, or add a read-fallback in `handlers.go:259` to the legacy key (and also keep excluding the legacy literal in `skipAppConfigSecret`) for the transition?
3. Should the migration also bump `app_config.schema_version` (binary↔DB gate) to force ordering? Plan assumes no (not a breaking change).
4. Postgres: no `SQL/postgres` migration mechanism found for app_config renames; assume none needed (no Postgres deployment holds the key yet). Confirm.
5. Exact new key name: `secret_digikey_client_secret` (plan) vs shorter `secret_digikey_client`.

## Resolved decisions
1. Option 1: `secret_` key prefix; exclusion matches the prefix.
2. Rename migration required, run together with the new binary. No runtime read-fallback, no legacy-literal exclusion.
3. New key name: `secret_digikey_client` (NOT `secret_digikey_client_secret`; override any use of the longer name above).
4. Migration does not bump `app_config.schema_version`.
5. No Postgres counterpart needed.
