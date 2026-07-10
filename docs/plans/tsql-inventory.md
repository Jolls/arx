# T-SQL construct inventory

Scope: application-layer Go queries (`arx_go/*.go`), schema files (`SQL/*.sql`, excluding `SQL/migrations/`), and `SQL/named_queries.sql`. Migration scripts, `SQL/ad_hoc/`, and `SQL/queries/` are intentionally excluded — they're one-shot/reference, not steady-state surface.

Purpose: catalog every SQL Server-specific construct so a future portability effort (or just "how coupled are we to SQL Server") has a concrete list instead of a vibe.

---

## 1. Schema files (`SQL/*.sql`, table DDL + `triggers.sql`)

Every one of the 24 table-definition files uses the same handful of T-SQL idioms. Rather than list all 24 line-by-line, here's the pattern and what's unusual per file.

### Constructs present in nearly every file
| Construct | Example | ANSI/portable equivalent |
|---|---|---|
| `IF OBJECT_ID('dbo.x','U') IS NOT NULL DROP TABLE x;` | [part.sql:23](../../SQL/part.sql#L23) | `DROP TABLE IF EXISTS x;` |
| `INT PRIMARY KEY IDENTITY` | [part.sql:26](../../SQL/part.sql#L26) | `SERIAL`/`GENERATED ALWAYS AS IDENTITY` |
| `VARCHAR(MAX)` | [part.sql:35](../../SQL/part.sql#L35) | `TEXT` |
| `BIT` | [part.sql:52](../../SQL/part.sql#L52) | `BOOLEAN` |
| `DEFAULT GETDATE()` | [part.sql:46](../../SQL/part.sql#L46) | `DEFAULT CURRENT_TIMESTAMP` |
| `dbo.` schema prefix | throughout | schema-qualification differs per engine |

This alone touches all 24 non-trigger schema files (`app_config`, `bom`, `company`, `company_attachment`, `contact`, `form`, `form_events`, `inventory_transaction`, `logs`, `mfg_part`, `part`, `part_attachment`, `po_line`, `price`, `purchase_order`, `record_event_results`, `record_events`, `release_notes`, `supplier_part`, `test_definition`, `test_definition_history`, `test_record`, `test_result`, `unit`, `users`).

### Notable one-offs
- **`purchase_order.sql`** — uses a T-SQL `SEQUENCE` object for PO numbering, not identity/autoincrement:
  ```sql
  CREATE SEQUENCE dbo.PO_Number_Seq AS INT START WITH 1 INCREMENT BY 1 NO CYCLE NO CACHE;
  -- consumed via: SELECT NEXT VALUE FOR dbo.PO_Number_Seq
  ```
  ([purchase_order.sql:7-13](../../SQL/purchase_order.sql#L7-L13)) — Postgres has `CREATE SEQUENCE`/`nextval()` too, but the syntax and `NO CACHE` option are T-SQL-specific spelling. MySQL has no sequence object at all pre-8.0/MariaDB.
- **`triggers.sql`** — the least portable file in the repo. Every trigger uses:
  - `CREATE OR ALTER TRIGGER ... AFTER INSERT, UPDATE, DELETE` ([triggers.sql:21-23](../../SQL/triggers.sql#L21-L23))
  - `inserted` / `deleted` pseudo-tables ([triggers.sql:28,30](../../SQL/triggers.sql#L28))
  - `SET NOCOUNT ON` ([triggers.sql:26](../../SQL/triggers.sql#L26))
  - **`UPDATE ... FROM ... JOIN`** — T-SQL's proprietary UPDATE-FROM-JOIN syntax, no ANSI equivalent ([triggers.sql:32-35](../../SQL/triggers.sql#L32-L35))
  - `GO` batch separators between each trigger ([triggers.sql:37](../../SQL/triggers.sql#L37))
  - `CONTEXT_INFO()` + `CONVERT(VARCHAR(128), ...)` + `CHAR(0)` to recover the app-set username in the audit trigger ([triggers.sql:130](../../SQL/triggers.sql#L130)) — no portable equivalent; Postgres would need `current_setting()` + a session GUC.
  
  None of the 5 triggers would run unmodified on Postgres or MySQL — they'd need to be rewritten as row-level triggers (`NEW`/`OLD`, `FOR EACH ROW`, PL/pgSQL functions).

**Verdict:** the DDL *shape* (columns, constraints, FKs, CHECK, UNIQUE) is portable. The wrapper syntax around every file is not. `triggers.sql` requires a full rewrite, not a search-and-replace.

---

## 2. Named queries (`SQL/named_queries.sql`)

These are stored as data (rows in the `named_queries` table) and interpreted by the app for `spec_nom` auto-fill — but they're still SQL Server dialect SQL and would break unmodified on another engine.

| Construct | Location | Notes |
|---|---|---|
| `TOP 1` / `TOP 20` | [named_queries.sql:70,82,92](../../SQL/named_queries.sql#L70) | T-SQL only; Postgres/MySQL use `LIMIT` |
| `TRY_CAST(... AS INT)` | [named_queries.sql:92,105](../../SQL/named_queries.sql#L92) | T-SQL only; Postgres has no direct equivalent (needs a wrapper function or regex guard) |
| `CONVERT(DATE, @record_date, 101)` | [named_queries.sql:105](../../SQL/named_queries.sql#L105) | style-code date parsing, pure T-SQL |
| `'%' + @pn + '%'` string concat with `+` | [named_queries.sql:114,54](../../SQL/named_queries.sql#L114) | T-SQL operator overload; ANSI/Postgres/MySQL use `||` or `CONCAT()` |
| `@param` named parameters | throughout | `go-mssqldb` convention, not itself portable SQL syntax (Postgres uses `$1`, MySQL driver-dependent) |
| `COALESCE(...)` | [named_queries.sql:70,82](../../SQL/named_queries.sql#L70) | portable — ANSI standard, no change needed |
| `GETDATE()` (in seed INSERTs, not query bodies) | [named_queries.sql:39,48,56...](../../SQL/named_queries.sql#L39) | T-SQL only |

7 of 8 stored named queries use at least one T-SQL-only construct (`TOP`, `TRY_CAST`, or `+` concatenation). Only the JOIN/WHERE logic underneath is generic.

---

## 3. Application-layer queries (`arx_go/*.go`)

This is where the volume actually lives — ~110+ inline SQL strings across handlers. Grouped by construct:

### Pagination — `TOP (@n)` (T-SQL-only, no ANSI equivalent)
- [api.go:161](../../arx_go/api.go#L161), [parts.go:1680](../../arx_go/parts.go#L1680), [parts.go:1721](../../arx_go/parts.go#L1721), [reports.go:79](../../arx_go/reports.go#L79), [reports.go:105](../../arx_go/reports.go#L105), [suppliers.go:191](../../arx_go/suppliers.go#L191)

### Pagination — `OFFSET ... ROWS FETCH NEXT ... ROWS ONLY` (ANSI SQL:2008 standard — portable, works on Postgres too)
- [api.go:36](../../arx_go/api.go#L36), [api.go:121](../../arx_go/api.go#L121)

Two different pagination idioms are in active use side by side — worth consolidating (see suggestions below).

### Identity/RETURNING-id pattern — `OUTPUT INSERTED.id` (T-SQL only; Postgres uses `RETURNING`, MySQL uses `LastInsertId()`)
Used pervasively for every INSERT that needs the new row's ID back — at least 18 call sites, including [contacts.go:215](../../arx_go/contacts.go#L215), [parts.go:411](../../arx_go/parts.go#L411), [suppliers.go:242](../../arx_go/suppliers.go#L242), [records.go:864](../../arx_go/records.go#L864), [records.go:1465](../../arx_go/records.go#L1465), [records.go:1813](../../arx_go/records.go#L1813), [records.go:2451](../../arx_go/records.go#L2451), [records.go:2554](../../arx_go/records.go#L2554), [records.go:2662](../../arx_go/records.go#L2662), [records_history.go:166](../../arx_go/records_history.go#L166), [named_query_settings.go:124](../../arx_go/named_query_settings.go#L124), plus 7 in `integration_test.go`.

**Notable exception:** [pos.go:503,526](../../arx_go/pos.go#L503-L526) and [pos.go:2393](../../arx_go/pos.go#L2393) explicitly *avoid* `OUTPUT INSERTED.id` and use `SCOPE_IDENTITY()` instead, because SQL Server blocks `OUTPUT INSERTED` on tables with triggers — this is a T-SQL engine quirk baked directly into app logic, not just query syntax.

### Upsert — `MERGE`
- [handlers.go:244-246](../../arx_go/handlers.go#L244-L246) — `MERGE INTO ... USING ... WHEN MATCHED THEN UPDATE` for `app_config` key/value upsert. T-SQL/ANSI SQL:2003 MERGE syntax; Postgres would use `INSERT ... ON CONFLICT DO UPDATE`, MySQL `INSERT ... ON DUPLICATE KEY UPDATE`. Only one call site, but it's a distinct enough pattern to flag.

### Window functions — `LAG`/`LEAD` `OVER (ORDER BY ...)`
- [records.go:1171-1172](../../arx_go/records.go#L1171-L1172) — for prev/next record navigation. This is **ANSI-standard** window function syntax, portable to Postgres/MySQL 8+ as-is. Flagged here only because it's adjacent to `TRY_CAST` in the same query.

### Date/time functions
| Construct | Location | Portable? |
|---|---|---|
| `GETDATE()` | ~30 call sites across `auth.go`, `pos.go`, `records.go`, `records_history.go` | No — `CURRENT_TIMESTAMP` is the ANSI equivalent |
| `CAST(x AS DATE)` | [pos.go:1400](../../arx_go/pos.go#L1400), [records.go:469,472,558](../../arx_go/records.go#L469) | Yes — standard `CAST` |
| `DATEFROMPARTS(YEAR(GETDATE()), MONTH(GETDATE()), 1)` | [reports.go:67](../../arx_go/reports.go#L67) | No — T-SQL only; Postgres needs `date_trunc('month', now())` |

### Null-coalescing / type-safe casts
| Construct | Location | Portable? |
|---|---|---|
| `ISNULL(x, y)` | [pos.go:730,2289](../../arx_go/pos.go#L730) (4 uses) | No — ANSI equivalent is `COALESCE(x, y)`, which the codebase also uses elsewhere inconsistently |
| `TRY_CAST(x AS INT)` | [records.go:305,1171,1172,1360,1450,2764](../../arx_go/records.go#L305), [integration_test.go:267](../../arx_go/integration_test.go#L267) | No — T-SQL only. Used repeatedly for numeric-sort-on-string-column (serial numbers). Postgres equivalent needs a regex guard + explicit `CAST` |

### Named parameters `@p1, @p2, ...`
Used throughout every parameterized query in the app (hundreds of occurrences) — this is a `go-mssqldb` driver convention (positional `@pN` placeholders), not portable SQL syntax. A driver swap (e.g. to `lib/pq`/`pgx` for Postgres) would require converting every placeholder to `$1, $2, ...` or named-to-positional remapping.

---

## Summary table

| Category | Portable (ANSI) | SQL Server-only |
|---|---|---|
| Schema DDL wrappers | column/constraint shape | `IDENTITY`, `VARCHAR(MAX)`, `BIT`, `GETDATE()`, `OBJECT_ID()` drop-guard, `dbo.` prefix — all 24 files |
| `purchase_order.sql` sequence | concept portable | `NEXT VALUE FOR` spelling, `NO CACHE` |
| `triggers.sql` | none | 100% — `inserted`/`deleted`, `UPDATE...FROM...JOIN`, `CONTEXT_INFO()`, `GO` batches |
| `named_queries.sql` | JOIN/WHERE bodies, `COALESCE` | `TOP`, `TRY_CAST`, `CONVERT(...,101)`, `+` concat |
| App pagination | `OFFSET/FETCH` (2 sites) | `TOP (@n)` (6 sites) |
| App insert-id pattern | none in use | `OUTPUT INSERTED.id` (18+ sites), `SCOPE_IDENTITY()` (2 sites, trigger workaround) |
| App upsert | none in use | `MERGE` (1 site) |
| App window functions | `LAG/LEAD OVER` (portable) | — |
| App date handling | `CAST(x AS DATE)` | `GETDATE()` (~30 sites), `DATEFROMPARTS` (1 site) |
| App null/cast helpers | `COALESCE` (used inconsistently) | `ISNULL` (4 sites), `TRY_CAST` (7 sites) |
| Parameter syntax | none — driver-specific either way | `@p1, @p2...` (hundreds of sites) |

---

## Suggestions

1. **Not worth pursuing multi-engine portability as a goal.** The app is architecturally SQL Server-only already (triggers doing denormalized-count maintenance, `SCOPE_IDENTITY()` worked around specifically because of trigger interaction, `go-mssqldb` driver). Porting would mean rewriting all 5 triggers as engine-native equivalents, converting every `OUTPUT INSERTED.id` call site, and replacing the parameter marker style throughout — this is a rewrite, not a refactor. Treat this inventory as documentation, not a to-do list, unless a specific driver for portability comes up.

2. **`ISNULL` vs `COALESCE` inconsistency is a cheap, low-risk cleanup** if you want one, independent of any porting goal — `COALESCE` is already used in several places ([named_queries.sql:70](../../SQL/named_queries.sql#L70), [records.go:1360](../../arx_go/records.go#L1360)) and is a drop-in replacement for the 4 `ISNULL` call sites in `pos.go`. Same behavior on SQL Server, no functional risk, one less dialect quirk to remember.

3. **Two pagination idioms coexist** (`TOP (@n)` in 6 places vs. `OFFSET/FETCH` in 2). If either was chosen recently as the "new" pattern, consider standardizing new code on it — not urgent, but worth a note in `CLAUDE.md`'s SQL conventions if there's a preference.

4. **The `pos.go` `SCOPE_IDENTITY()` comment is worth preserving/highlighting** — it documents a real trigger interaction gotcha (`OUTPUT INSERTED` is blocked by SQL Server on tables with triggers) that isn't obvious and would bite anyone adding a new trigger to a table that also uses `OUTPUT INSERTED.id` elsewhere. Might be worth a one-line callout in `SQL/schema.md` near the trigger documentation, cross-referencing this so it's not just a comment buried in `pos.go`.
