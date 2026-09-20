# 104 — Keep the DigiKey client secret out of the backup export

Issue: [#104](https://github.com/Jolls/arx/issues/104). Part of the pre-publication
review, [#102](https://github.com/Jolls/arx/issues/102).

## Problem

`SettingsBackup` (`arx_go/settings.go:634`) is careful with the users table — that one
call passes `"password_hash"` to `writeTableCSV`'s `excludeCols` so hashes never leave
the app. `app_config` gets no such treatment: it is exported wholesale in the generic
table loop, and it is where the DigiKey OAuth client secret lives
(`handlers.go:259` reads `digikey_client_secret` from it; `SettingsDigiKeySave` writes
it there).

So the backup ZIP carries a live third-party API credential in plaintext, while the
Settings UI itself never renders that value back — `settings.html:285` only receives a
`DigiKeyClientSecretSet` boolean.

## Decision: drop the row, don't relocate the credential

The issue floated moving DigiKey credentials into the per-user secrets store. **Not
doing that.** `handlers.go:255-257` records that these are deliberately shop-wide,
shared across every user unlike a DB password (#60). Moving them per-user would reverse
a deliberate product decision and force every user to hold their own DigiKey
registration. Out of scope here.

Dropping the row matches how `password_hash` is already handled — omitted, not
redacted — and the value is re-enterable in Settings, so a backup losing it costs
nothing.

## Obstacle

`writeTableCSV` excludes **columns**. `app_config` is key/value
(`setting_key` PK, `setting_value`, `updated_at`), so the secret is a **row**. Column
exclusion cannot express it, and excluding `setting_value` outright would gut the whole
table.

## Changes — all in `arx_go/settings.go`

### 1. Name the secret-bearing keys

```go
// appConfigSecretKeys are app_config rows holding credentials rather than shop
// configuration. The backup omits them the same way it omits users.password_hash:
// a backup is a copy of shop data, not a credential store, and these are
// re-enterable in Settings (#104). Keys are compared lowercased.
var appConfigSecretKeys = map[string]bool{"digikey_client_secret": true}
```

### 2. Give `writeTableCSV` an optional row filter

Add a `rowFilter` type and a `skipRow` parameter before the variadic `excludeCols`:

```go
// rowFilter reports whether a row should be omitted from the export. It receives
// the row's columns keyed by lowercased column name.
type rowFilter func(row map[string]string) bool
```

Signature becomes:

```go
func (h *Handler) writeTableCSV(r *http.Request, zw *zip.Writer, table string, skipRow rowFilter, excludeCols ...string) error
```

Inside the scan loop, only when `skipRow != nil`, build the lowercased-key map from
the full (pre-exclusion) column set and `continue` when it returns true. Building the
map is gated on the predicate being present so the other ~27 tables pay nothing.

Lowercasing the map keys mirrors the existing case-insensitive column matching already
in that function — Postgres folds unquoted names, SQL Server preserves them.

### 3. Update the three call sites

- Generic loop (line 656): `h.writeTableCSV(r, zw, tbl, nil)`.
- Users (line 660): `h.writeTableCSV(r, zw, h.cfg.UsersTable(), nil, "password_hash")`.
- `app_config`: remove `h.cfg.AppConfigTable()` from the `tables` slice and export it
  with the predicate:

```go
if err := h.writeTableCSV(r, zw, h.cfg.AppConfigTable(), func(row map[string]string) bool {
    return appConfigSecretKeys[strings.ToLower(row["setting_key"])]
}); err != nil {
    log.Printf("backup: error exporting %s: %v", h.cfg.AppConfigTable(), err)
}
```

Not touching `company_logo` — it bloats the export as a large base64 blob but is not a
secret, and trimming it is a separate concern.

## Out of scope

Admin-gating `GET /settings/backup` is [#106](https://github.com/Jolls/arx/issues/106),
implemented next on this branch.

## Success criteria

- `app_config.csv` in the backup ZIP contains no `digikey_client_secret` row.
- Every other `app_config` row (`schema_version`, `attachment_categories`,
  `part_categories`, `part_numbering`, `company_logo`) is still present.
- `users.csv` still omits `password_hash`; all other tables unchanged.
- `go build ./...`, `go vet ./...`, `go test ./...` pass in both modules.
