# ArxProd data migration: Azure SQL Server -> Postgres (#216)

One-time, human-run. The tool (`arx_go/cmd/migrate_data`, logic in `internal/datamigrate`) reads a CSV
export of the Azure SQL ArxProd database (schema_version 10, the 0.7 line) and loads it into a Postgres
database built from `SQL`. The same tool and steps serve the ArxDev rehearsal and the ArxProd
cutover; only the target database differs.

**Agents never touch the source or the new ArxProd.** Everything in steps 1 and 4 below that names them is run
by you.

## What the tool does

- Reads `<table>.csv` for every target table, plus `manifest.csv` (server-computed row counts and sums).
- Maps source columns to target columns: `company.SUNotes/SUNumOfLNKs/SUNumOfPOs/SUSupplierCode` are renamed,
  `part.is_lot_tracked` is dropped (migration 31). Any other source column without a target column is an
  error, so nothing is dropped silently. `logs`, `release_notes` and `schema_migrations` are not loaded.
- Converts every `datetime` that becomes `timestamptz` per the table below. Numerics are copied as text, so
  digits are exact. Bits are `0/1`, NULL is `\N`.
- Refuses to start when the CSVs disagree with the manifest (truncated or mangled export), or when any child row
  points at a parent that is not in the export (all orphans are listed in one pass, nothing is changed).
- Then, in **one transaction**: `TRUNCATE` every data table (`RESTART IDENTITY`), delete `app_config` rows except
  the target's `schema_version`, load in FK order (cyclic FK columns are loaded NULL and set at the end), run
  migration 194's and 28's SQL (categories from `app_config` into `part_category`/`attachment_category`;
  Postgres text for the canonical `named_queries`), reset every identity sequence and `po_number_seq` past the
  loaded data, then compare row counts and every numeric column's sum with the CSV. Anything wrong rolls
  the whole load back.
- `app_config` rows whose key contains `secret`, `password`, `token`, `api_key`, `apikey`, `credential`,
  `private_key` or `client_id` are not carried over, and `schema_version` stays the target's. The run prints
  both the skipped and the carried keys (never values): read the carried list and stop if a credential slipped through.
- It loads version-10 exports only (read from the export's `schema_version` row), and refuses a manifest table the
  target doesn't have, so nothing is left behind silently.
- Sequences are restored after a dry run or a failed load (`setval` is not transactional).
- Multi-line text (CRLF) keeps its line endings; a value that is literally `\N` is rejected by the export script.

## Timezone table (#279)

The source stores zoneless `datetime`. Two clocks wrote them. The load reads each as below so the instant is
right, which is what migration 192 did. Both zones are flags (`--server-zone`, default `UTC`: Azure SQL's clock;
`--desktop-zone`, default `America/Los_Angeles`: the shop desktops).

| Clock | Columns |
|---|---|
| Server (`GETDATE()`, UTC) | `app_config.updated_at`, `form_events.event_date`, `form_record.created_at/updated_at`, `form_row.created_at/updated_at`, `form_row_history.changed_at`, `record_events.event_date`, `result.updated_at`, `unit.created_at`, `users.created_at/updated_at` |
| Desktop (Go `time.Now()`, Pacific) | `build.created_at`, `company.date_modified`, `contact.updated_at`, `inventory_transaction.created_at`, `lot.created_at`, `named_queries.created_at/updated_at`, `part.last_rollup_at`, `purchase_order.date_modified`, `purchase_order_history.changed_at` |
| Not converted | `form_record.record_date` (user-typed, stays a zoneless `TIMESTAMP`); all `DATE` columns |

Known imprecision, **not corrected** by this load and left as is (#279; cutoffs in `SQL/SCHEMA.md`,
"Timestamp and date provenance"): `purchase_order.date_modified` is also written with `GETDATE()` (UTC) at
one path, so those rows are 7-8 hours off; `DATE` columns set from `GETDATE()` can be a day off for evening
Pacific writes.

Also not corrected: a desktop-clock time inside the one-hour daylight-saving overlap (or gap) resolves to one
of the two possible instants, so a handful of rows can be off by an hour. The 10 canonical `named_queries` rows get
`updated_at` = load time, because migration 28's SQL (Postgres text) is run after the load.

A `timestamptz` column that is not in this table makes the load fail (`spec.go`, `timestampClocks`).

## 1. Export (you, against Azure SQL ArxProd)

1. Freeze writes: tell users to stop (or stop the app) until the export finishes.
2. From `docs/216-data-migration/`, in Windows PowerShell 5.1:

   ```powershell
   .\export_source.ps1 -Server <name>.database.windows.net -Database ArxProd -OutDir C:\arx-export -User <sql login>
   # or, with Entra (after `winget install Microsoft.AzureCLI` and `az login`): replace `-User ...` with `-Entra`
   ```

   A SQL login is prompted for its password (never put it on the command line). `-Entra` takes a token from the
   Azure CLI, because Windows PowerShell's built-in client can't sign in to Entra itself. It only runs `SELECT`s. It writes
   one UTF-8 CSV per table and `manifest.csv`, and warns if the server's count differs from what it wrote.
3. The CSVs contain real data and password hashes: keep them out of the repo and delete them afterward.

## 2. Load (you, from the repo root)

`ARX_MIGRATE_DSN` is a DSN for a DDL-capable login (TRUNCATE, dropping a constraint), set only in your own terminal
session, never in a file. The app's login is not used.

```powershell
# Rehearsal: runs everything, verifies, then rolls back. Nothing changes.
go run ./arx_go/cmd/migrate_data --csv-dir C:\arx-export --target-db <DB> --confirm-truncate <DB> --dry-run

# For real
go run ./arx_go/cmd/migrate_data --csv-dir C:\arx-export --target-db <DB> --confirm-truncate <DB>
```

- `--confirm-truncate` must repeat `--target-db`, and both must equal the connected database's name.
- A name containing `arxprod` also needs `--allow-prod` (the real cutover only).
- Re-run any time: it truncates and reloads, nothing is left over.
- Output lists each table's rows, the sequences, and what was skipped. `Committed.` means done.

## 3. Order of work

1. **ArxDev (Azure Postgres `arxprod2...`), rehearsal:** steps 1 and 2 with `<DB>` = `ArxDev`. ArxDev now holds real
   data, replacing the seed. Do not run the integration suite against it in this state (it refuses: seed sentinel
   part id 3005 is gone).
2. **ArxProd (same server):** you create the database `ArxProd` and two logins (DDL role for the load and
   migrations, app role with DML only; see `SQL/SCHEMA.md`, "Database privileges"). Build the schema:
   `bash SQL/build_schema.sh | psql "<DDL DSN to ArxProd>" -q -v ON_ERROR_STOP=1` (it also seeds
   test rows and baselines the ledger; the load truncates the seed). Then steps 1 and 2 with `<DB>` = `ArxProd`
   and `--allow-prod`. Take a fresh export after the write freeze for the real run.
3. **Point `Arx.exe` at ArxProd:** Settings > database on each desktop (server, name, app login). Users
   re-enter nothing else: logins and password hashes, preferences, PO defaults and history carry over. The
   session secret is per user and not migrated, so each user may need to log in once more.
4. **Reseed ArxDev** with `SQL/seed_test_data.sql` (human action), then rerun the integration suite
   (`ARX_TEST_FROM_CONFIG=1 go test -tags integration ./arx_go/...`, about 30 minutes).

## 4. Checks after the real load

- The tool already compared row counts and every numeric column's sum against the CSVs, and the CSVs against the
  server's own manifest.
- Smoke test in the app: log in with an existing password; open a recent PO, a part with attachments, a test
  record with history; create a PO and confirm its number is one past the highest existing.
- Dates: a PO and a test record created last week show the times they did in 0.7 (desktop-clock columns were
  read as Pacific, server-clock as UTC).

## 5. Rollback

The load is all-or-nothing, so a failed run leaves the target as it was. A committed load that turns out wrong:
rerun the tool with a corrected export (it truncates and reloads). Until users are pointed at the new database the
Azure SQL source is untouched and remains the system of record.

## 6. Provisioning notes (Azure Database for PostgreSQL Flexible Server)

- Public access with firewall rules for the shop and remote users' IPs; `sslmode=require` is always set by the app.
- Keep the DDL-capable login (`ARX_MIGRATE_DSN`) separate from the app login (#189). The app login needs
  `SELECT, INSERT, UPDATE, DELETE` on all tables and `USAGE` on the sequences, including `po_number_seq`.
- Entra logins cannot be used with a password; Arx and the tests need password roles.
- The Flexible Server parameter `TimeZone` is irrelevant to this load (every instant is converted explicitly).
