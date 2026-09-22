# #146 Harden the named-query SELECT guard + document least-privilege DB grants

Part of #142 (pre-publication review). Closes the `OPENROWSET`/`BULK` family bypass in
`unsafeKeywordRE` and writes down the DB grants that are the actual control.

## Scope

In scope (this session):
- Issue step 3 — extend `unsafeKeywordRE` in `arx_go/named_query.go`.
- Regression test cases for the new keywords.
- Issue step 2 — a `## Database privileges` section in `SQL/SCHEMA.md`.

Out of scope, deliberately:
- **Issue step 1 (applying the grants).** Never executed here — no DB server is touched from
  this session and `ArxProd` is off-limits repo-wide. The new SCHEMA.md section is the
  hand-off: a human DBA applies and verifies it per environment.
- **`sys.*` / `information_schema` catalog reads.** Not blocked. The issue lists them as
  exposure but its suggested fix (step 3) does not include them, and an Arx admin — the only
  role that can author a named query — already sees every stored query body and the schema
  through the Settings named-query editor. Grants, not the regex, are the control here.
- **`pg_read_binary_file`** and other siblings of the three Postgres functions the issue
  names. The denylist is explicitly non-exhaustive defense-in-depth; enumerating the whole
  family is the kind of completeness the regex is not meant to provide.
- **The user-facing error string** in `arx_go/named_query_settings.go:92`
  ("...no INSERT/UPDATE/DELETE/DDL or semicolons"). Left as-is — it already leads with
  "Query must be a plain SELECT statement", which covers the new rejections.

## Decision: the Postgres function names go in the same denylist

Add `PG_READ_FILE`, `PG_LS_DIR`, `PG_SLEEP` alongside the SQL Server keywords, in one
engine-agnostic regex. Reasoning:

- `isSafeQuery` is the single gate for both engines. `arx_go/handlers.go:87` (`h.dia()`)
  selects the dialect at runtime, `arxlib/db/dialect.go` ships a `postgresDialect`, and
  `SQL/postgres/*` is a maintained target — an engine-conditional denylist would be wrong the
  moment a deployment runs Postgres, and adds a branch for no benefit.
- The three names are inert on SQL Server (no such identifiers exist there).
- Grep-verified no collision: no column, alias, or seeded named-query body in `SQL/azure/*.sql`,
  `SQL/postgres/*.sql`, or `arx_go/*.go` contains any of the seven new tokens at a word
  boundary. The only near-miss is `company.bulk_order_delimiter` / `bulk_order_pn_source`
  (`SQL/postgres/company.sql:22-23`), which `\bBULK\b` does **not** match — `_` is a word
  character, so there is no boundary after `bulk`.

## File changes

### 1. `arx_go/named_query.go` (lines 14-19)

Replace the `unsafeKeywordRE` doc comment and pattern. Current:

```go
// unsafeKeywordRE matches DML/DDL/admin keywords at word boundaries and
// semicolons. INTO blocks SELECT ... INTO (a table-creating write that would
// otherwise slip past a SELECT-prefixed query); WAITFOR blocks a trivial DoS.
// Word boundaries avoid false positives on column names that contain keyword
// substrings (e.g. created_at, updated_at, alternate).
var unsafeKeywordRE = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|EXEC(UTE)?|TRUNCATE|ALTER|CREATE|INTO|MERGE|GRANT|REVOKE|DENY|WAITFOR|DBCC|BACKUP|RESTORE|SHUTDOWN)\b|;`)
```

New:

```go
// unsafeKeywordRE matches DML/DDL/admin keywords at word boundaries and
// semicolons. INTO blocks SELECT ... INTO (a table-creating write that would
// otherwise slip past a SELECT-prefixed query); WAITFOR blocks a trivial DoS.
// OPENROWSET/OPENDATASOURCE/OPENQUERY/BULK block SQL Server's file-read and
// remote-provider surface, which needs neither EXEC nor a semicolon; the PG_*
// entries are the Postgres equivalents (file read, directory list, sleep DoS) —
// one list for both engines, since this guard is dialect-independent (#146).
// Word boundaries avoid false positives on column names that contain keyword
// substrings (e.g. created_at, updated_at, alternate, bulk_order_delimiter).
var unsafeKeywordRE = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|EXEC(UTE)?|TRUNCATE|ALTER|CREATE|INTO|MERGE|GRANT|REVOKE|DENY|WAITFOR|DBCC|BACKUP|RESTORE|SHUTDOWN|OPENROWSET|OPENDATASOURCE|OPENQUERY|BULK|PG_READ_FILE|PG_LS_DIR|PG_SLEEP)\b|;`)
```

The existing `(?i)` flag covers lowercase `pg_read_file(...)` etc. No other line in this file
changes.

### 2. `arx_go/helpers_tr_test.go` — extend `TestIsSafeQuery` (line 29)

The issue says the test lives in `arx_go/named_query_unit_test.go`; it does not. That file
only holds `TestAPINamedQuery_BadSpecPrefix`. The `isSafeQuery` table test is
`TestIsSafeQuery` in `arx_go/helpers_tr_test.go:29-62`. Extend that table rather than
starting a second one — same function, same style.

Append after the last case (`{"SELECT 1 DBCC CHECKDB", false},`, line 55), before the closing
`}` of the slice:

```go
		// file-read / remote-provider surface (#146) — needs neither EXEC nor ';'
		{"SELECT * FROM OPENROWSET(BULK 'secrets.txt', SINGLE_CLOB) AS x", false},
		{"SELECT * FROM OPENDATASOURCE('SQLNCLI', 'Data Source=evil').db.dbo.t", false},
		{"SELECT * FROM OPENQUERY(linked, 'SELECT 1')", false},
		{"SELECT pg_read_file('/etc/passwd')", false},
		{"SELECT pg_ls_dir('/')", false},
		{"SELECT pg_sleep(30)", false},
		// word-boundary: bulk_* column names must still pass
		{"SELECT bulk_order_delimiter FROM company", true},
```

Keep the existing tab indentation of the surrounding cases. No new imports, no new test
function.

### 3. `SQL/SCHEMA.md` — new `## Database privileges` section

Insert between the end of `## Migrations` (after the "Not `app_config.schema_version`" bullet,
line 146) and the `---` on line 148. House style in this file: `##` section, short prose lead,
bullets, fenced `sql` blocks for reference-only SQL.

```markdown
## Database privileges

The app's DB login must be least-privilege. `isSafeQuery` (`arx_go/named_query.go`) is a
keyword **denylist** and defense-in-depth only: an admin-authored named query executes with
whatever rights the app login holds, so file reads (`OPENROWSET(BULK …)`, `pg_read_file()`) and
outbound remote-provider connections are closed by the grants below, not by the regex (#146).

Nothing here is auto-run and no migration applies it — a DBA applies and verifies it per
environment (ArxDev, ArxProd), the same way `SQL/azure/*.sql` is reference DDL.

**Required — and nothing more:**

- `SELECT`, `INSERT`, `UPDATE`, `DELETE` on the application tables listed in "Table reference"
  below.
- The PO-number sequence: SQL Server `GRANT UPDATE ON OBJECT::dbo.PO_Number_Seq` (`NEXT VALUE
  FOR` requires `UPDATE`, and `db_datawriter` does not cover sequences); Postgres
  `GRANT USAGE ON SEQUENCE po_number_seq`. Drawn in `arx_go/pos.go` and `arx_go/rfq_bom.go`.
- Nothing else: the app runs no DDL at runtime, calls no stored procedures, and never reads or
  writes outside these tables. Triggers (see "Triggers") fire under the table owner via
  ownership chaining and need no extra grant. `SET CONTEXT_INFO` needs no grant.

**Must NOT hold — SQL Server / Azure SQL:**

- `ADMINISTER BULK OPERATIONS` (Azure SQL Database: `ADMINISTER DATABASE BULK OPERATIONS`) or
  the `bulkadmin` server role — these enable `BULK INSERT` and `OPENROWSET(BULK …)` file reads
  as the SQL Server service account.
- `db_owner`, `db_ddladmin`, `sysadmin`, or `CONTROL`/`ALTER ANY` on the database.
- The server-level **Ad Hoc Distributed Queries** option should stay disabled (its default) —
  it gates `OPENROWSET`/`OPENDATASOURCE` against remote providers.

**Must NOT hold — Postgres:**

- Membership in `pg_read_server_files`, `pg_write_server_files`, or
  `pg_execute_server_program`.
- `SUPERUSER`, and the login must not own the tables — an owner can `ALTER`/`DROP` them
  regardless of grants.

**Per-environment verification.** Run by hand as the app login against the target database
(never ArxProd from tooling):

```sql
-- SQL Server / Azure SQL
SELECT permission_name FROM fn_my_permissions(NULL, 'DATABASE') ORDER BY permission_name;
SELECT HAS_PERMS_BY_NAME(NULL, NULL, 'ADMINISTER BULK OPERATIONS')          AS bulk_ops;  -- expect 0
SELECT HAS_PERMS_BY_NAME(NULL, NULL, 'ADMINISTER DATABASE BULK OPERATIONS') AS db_bulk_ops; -- expect 0 (Azure SQL DB)
SELECT IS_ROLEMEMBER('db_owner') AS db_owner, IS_ROLEMEMBER('db_ddladmin') AS db_ddladmin;  -- expect 0
```

```sql
-- Postgres
SELECT rolname FROM pg_roles
 WHERE pg_has_role(current_user, oid, 'member')
   AND rolname LIKE 'pg\_%';                       -- expect no pg_read_server_files etc.
SELECT rolsuper FROM pg_roles WHERE rolname = current_user;  -- expect false
```
```

### 4. `CHANGELOG.md`

New top entry above `## [0.7.62]`, following the existing on-`main` patch cadence:

```markdown
## [0.7.63] - 2026-09-22
### Security
- Named-query guard now rejects OPENROWSET/OPENDATASOURCE/OPENQUERY/BULK and the Postgres file-read and sleep functions; required least-privilege DB grants documented in `SQL/SCHEMA.md` ([#146](https://github.com/Jolls/arx/issues/146))
```

Date is the commit date. Tag `v0.7.63` after the version-bump commit, pushed with the branch.
No `RELEASE_NOTES.md` entry — internal hardening, nothing a user acts on.

## Branch

Currently on `main`. Create `feature/146-named-query-guard-hardening` before the first commit.
PR body: `Closes #146`.

## Verification

1. `cd arx_go; go build ./... ; go vet ./... ; go test ./...` — `TestIsSafeQuery` must pass
   with the new cases (the one `true` case guards against a `BULK` false positive).
2. Confirm no stored named query regresses: grep the seeded bodies for the new tokens —
   ```
   grep -rniE "\b(openrowset|opendatasource|openquery|bulk|pg_read_file|pg_ls_dir|pg_sleep)\b" SQL/azure/named_queries.sql SQL/azure/seed_test_data.sql SQL/postgres/
   ```
   Expected: only the unrelated `bulk_order_*` column names and `-- Bulk-order` comments, none
   of which match at a word boundary. Already checked at plan time; re-run after the edit as
   the regression check.
3. No migration, no DDL, no `cfg.*Table()` helper, no schema change — steps 1-2 of the
   pre-commit sequence are all that apply. Live ArxDev integration tests are not required: the
   only Go change is to a pure function fully covered by unit tests.
4. Manual (user): Settings → Named Queries, paste
   `SELECT * FROM OPENROWSET(BULK 'C:\Windows\win.ini', SINGLE_CLOB) AS x` into a query body and
   save — expect the 400 "Query must be a plain SELECT statement" error. Then confirm an
   existing named query still saves unchanged.

## Follow-up for a human (not this session)

Apply and verify the grants in the new SCHEMA.md section against each environment. Until that
is done, the comment on `isSafeQuery` ("the real control is DB-level … until that is confirmed
per-environment") still holds literally — this change narrows the denylist gap but does not
close the class.
