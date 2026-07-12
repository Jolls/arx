# Postgres Migration - Phase 0 + Phase 1 Implementation Plan (#625)

> **Status: complete.** Phases 0 and 1 landed (this plan's checkboxes below are
> retroactively checked off to match). Work continued past this plan's scope
> into Phase 2 (Postgres dialect, `SQL/postgres/*.sql`, triggers) and most of
> the app-side gap closure (PR #674). Two portability gaps in `reports.go`
> (4 hardcoded `TOP (@pN)` sites bypassing `Dialect.TopClause`/`LimitClause`,
> and 2 `ISNULL` sites not yet `COALESCE`) were found during a post-merge audit
> and fixed on `feature/625-reports-dialect-gaps`. See
> [docs/plans/625-postgres-migration-design.md](./625-postgres-migration-design.md)
> for overall phase status; issue #625 remains open pending Phase 3 cutover.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a behavior-preserving dialect seam into Arx so the app runs on SQL Server today through a `Dialect` abstraction, setting up the later Postgres port without changing any current behavior.

**Architecture:** A `Dialect` interface in `arxlib/db` with a single `sqlServerDialect` implementation that reproduces today's SQL exactly. Two tiers of translation: Tier 1 is a central textual `Rewrite` (placeholders + `GETDATE()`) applied in the existing `h.queryContext/execContext/queryRowContext/beginTx` wrappers; Tier 2 is a set of thin per-construct helpers (`InsertReturningID`, pagination, `Upsert`, `TryCastInt`, `MonthStartExpr`) that the structural call sites route through. In Phase 0-1 every helper's SQL Server branch emits byte-for-byte today's SQL, so the existing SQL Server integration suite proves the seam changed nothing.

**Tech Stack:** Go 1.x workspace (`arxlib/` + `arx_go/`), `database/sql`, `github.com/microsoft/go-mssqldb` (SQL Server), go-chi. Postgres driver (`github.com/jackc/pgx/v5/stdlib`) arrives in Phase 2, not here.

## Global Constraints

- Design of record: [docs/plans/625-postgres-migration-design.md](./625-postgres-migration-design.md). End state is Postgres-only; SQL Server and go-mssqldb are removed at cutover (Phase 3), not in this plan.
- **Phases 0 and 1 must not change runtime behavior on SQL Server.** The `sqlServerDialect` emits exactly today's SQL. The verification bar for behavior is the existing `//go:build integration` suite against ArxDev, which is **human-run** (see below) - not this plan's automated gate.
- **Do not run the app** (`go run .`, launch `Arx.exe`, curl live routes) and **never touch ArxProd**. Verify with `go build`, `go vet`, `go test`, and targeted `grep`. Any live-DB check is described for the user to run manually against ArxDev.
- Build/test commands run **inside a module dir**, never the repo root (go.work): `cd arx_go && go build ./... && go vet ./... && go test ./...` and `cd arxlib && go test ./...`. `build.bat` is PowerShell-only and out of scope for these tasks.
- Use `cfg.*Table()` helpers for table names; never hardcode. Use the `h.queryContext/execContext/queryRowContext/beginTx` wrappers; never `h.db.*` directly.
- Text style: hyphens only, no em/en dashes, in all files.
- Commit per task with the repo's trailer:
  ```
  Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_012UtYghgWTw6J3BqAnqo5Zc
  ```

## Deviations from the design doc (recorded)

- The design's Phase 0 "LIKE-wildcard concat into Go params" cleanup has **zero app-layer sites** (`grep` of `arx_go/*.go` finds none; it exists only in `SQL/named_queries.sql`, a later surface). It is dropped from this plan.
- The design's Phase 0 "funnel pagination through a helper" is moved to **Phase 1** (Task 1.6): it requires the `Dialect` to exist, and consolidating on SQL Server alone would force ORDER BY changes for `TOP` sites (behavior risk) with no payoff until Postgres exists.

---

## File Structure

**Phase 0 (cleanups, SQL Server only):**
- Modify: `arx_go/pos.go` - `ISNULL` -> `COALESCE` (2 lines).
- Modify: `SQL/schema.md` - document the `SCOPE_IDENTITY`/`OUTPUT INSERTED`-on-triggers gotcha.

**Phase 1 (dialect seam):**
- Modify: `arxlib/config/base.go` - add `Engine` field + `Engine()` normalization.
- Modify: `arxlib/config/config.go` - load `engine` from env / local.json (default `sqlserver`).
- Create: `arxlib/config/base_test.go` (or extend existing) - engine parsing tests.
- Create: `arxlib/db/dialect.go` - `Dialect` interface + `sqlServerDialect`.
- Create: `arxlib/db/dialect_test.go` - pure-function dialect tests.
- Modify: `arxlib/db/db.go` - `Connect` selects driver by engine and returns a `Dialect`.
- Modify: `arx_go/main.go`, `arx_go/settings.go` - updated `Connect` call sites.
- Modify: `arx_go/handlers.go` - `dialect` field on `Handler`; `Rewrite` wired into the query wrappers; `appConfigSet` upsert routed through the dialect.
- Modify (route Tier-2 sites): `arx_go/parts.go`, `arx_go/reports.go`, `arx_go/suppliers.go`, `arx_go/api.go` (pagination); `arx_go/contacts.go`, `arx_go/parts.go`, `arx_go/records.go`, `arx_go/records_history.go`, `arx_go/named_query_settings.go`, `arx_go/suppliers.go`, `arx_go/pos.go` (insert-returning-id); `arx_go/records.go` (TryCastInt); `arx_go/reports.go` (MonthStart).

---

## Phase 0 - Safe cleanups (SQL Server only)

### Task 0.1: Replace `ISNULL` with `COALESCE` in pos.go

**Files:**
- Modify: `arx_go/pos.go:730`, `arx_go/pos.go:2289`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing (behavior-identical on SQL Server; `COALESCE` is ANSI and already used elsewhere in the codebase).

- [x] **Step 1: Confirm the exact current sites**

Run: `grep -n "ISNULL" arx_go/pos.go`
Expected: two lines, `730` and `2289` (2289 contains four `ISNULL(...)` calls).

- [x] **Step 2: Edit line 730**

Change:
```go
		SELECT ISNULL(SUM(pol.qty * pol.unit_cost), 0)
```
to:
```go
		SELECT COALESCE(SUM(pol.qty * pol.unit_cost), 0)
```

- [x] **Step 3: Edit line 2289**

Change:
```go
		SET total_cost = ISNULL(ls.s, 0) + ISNULL(po.tax1, 0) + ISNULL(po.shipping_cost, 0) + ISNULL(po.misc_cost, 0),
```
to:
```go
		SET total_cost = COALESCE(ls.s, 0) + COALESCE(po.tax1, 0) + COALESCE(po.shipping_cost, 0) + COALESCE(po.misc_cost, 0),
```

- [x] **Step 4: Verify no `ISNULL` remains in app code**

Run: `grep -rn "ISNULL" arx_go/*.go`
Expected: no output.

- [x] **Step 5: Build and vet**

Run: `cd arx_go && go build ./... && go vet ./...`
Expected: no errors.

- [x] **Step 6: Commit**

```bash
git add arx_go/pos.go
git commit -m "refactor: ISNULL -> COALESCE in pos.go (#625)

Behavior-identical on SQL Server; removes one T-SQL-only construct ahead
of the Postgres port.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012UtYghgWTw6J3BqAnqo5Zc"
```

**Manual behavior check (user, ArxDev):** open a PO detail page and confirm the PO total and the line-cost rollup still display correctly. (No automated test exercises these SQL strings without a live DB.)

---

### Task 0.2: Document the `SCOPE_IDENTITY` / `OUTPUT INSERTED`-on-triggers gotcha

**Files:**
- Modify: `SQL/schema.md` (near the trigger reference section)

**Interfaces:**
- Consumes: nothing.
- Produces: a documented rule the Phase 1 `InsertReturningID` helper (Task 1.5) relies on.

- [x] **Step 1: Locate the trigger reference section**

Run: `grep -n "trigger\|Trigger\|SCOPE_IDENTITY\|OUTPUT INSERTED" SQL/schema.md`
Expected: find the table-reference / trigger area; if no trigger subsection exists, add the note under the table reference for `purchase_order`/`po_line`.

- [x] **Step 2: Add the note**

Insert this paragraph in the trigger area:
```markdown
> **`OUTPUT INSERTED` vs triggers:** SQL Server blocks the `OUTPUT INSERTED.*`
> clause on any table that has an `AFTER` trigger. `purchase_order` therefore
> inserts and retrieves the new id with a batched `INSERT ...; SELECT CAST(SCOPE_IDENTITY() AS INT)`
> instead (see `arx_go/pos.go`). Any new table that both carries a trigger and
> needs its generated id back must use the same pattern. The Postgres port
> (issue #625) folds this quirk into `db.Dialect.InsertReturningID`.
```

- [x] **Step 3: Verify the note landed**

Run: `grep -n "OUTPUT INSERTED. vs triggers\|SCOPE_IDENTITY" SQL/schema.md`
Expected: the new note is present.

- [x] **Step 4: Commit**

```bash
git add SQL/schema.md
git commit -m "docs(schema): document OUTPUT INSERTED-on-triggers / SCOPE_IDENTITY gotcha (#625)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012UtYghgWTw6J3BqAnqo5Zc"
```

---

## Phase 1 - Dialect seam (SQL Server only, behavior-preserving)

### Task 1.1: Add an `Engine` config field

**Files:**
- Modify: `arxlib/config/base.go` (add field + accessor)
- Modify: `arxlib/config/config.go` (parse `engine` from env / local.json)
- Create/Modify: `arxlib/config/base_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `Base.Engine string` field on the config struct.
  - `func (b *Base) DBEngine() string` - returns the normalized engine id, defaulting to `"sqlserver"` when unset/unknown.

- [x] **Step 1: Write the failing test**

Create `arxlib/config/base_test.go` (or append):
```go
package config

import "testing"

func TestDBEngineDefaultsToSQLServer(t *testing.T) {
	b := &Base{}
	if got := b.DBEngine(); got != "sqlserver" {
		t.Fatalf("empty engine: got %q, want %q", got, "sqlserver")
	}
}

func TestDBEngineNormalizes(t *testing.T) {
	cases := map[string]string{
		"postgres":   "postgres",
		"POSTGRES":   "postgres",
		"sqlserver":  "sqlserver",
		"nonsense":   "sqlserver",
	}
	for in, want := range cases {
		b := &Base{Engine: in}
		if got := b.DBEngine(); got != want {
			t.Errorf("engine %q: got %q, want %q", in, got, want)
		}
	}
}
```

- [x] **Step 2: Run to verify it fails**

Run: `cd arxlib && go test ./config/ -run TestDBEngine -v`
Expected: FAIL - `Engine` field and `DBEngine` method do not exist.

- [x] **Step 3: Add the field and accessor**

In `arxlib/config/base.go`, add to the `Base` struct (near `DBServer`):
```go
	Engine         string // "sqlserver" (default) | "postgres"
```
and add the method:
```go
// DBEngine returns the normalized database engine id, defaulting to
// "sqlserver". Unknown values fall back to "sqlserver" so a typo cannot
// silently select an unbuilt backend.
func (b *Base) DBEngine() string {
	switch strings.ToLower(b.Engine) {
	case "postgres":
		return "postgres"
	default:
		return "sqlserver"
	}
}
```
Add `"strings"` to the imports in `base.go` if not present.

- [x] **Step 4: Run to verify it passes**

Run: `cd arxlib && go test ./config/ -run TestDBEngine -v`
Expected: PASS.

- [x] **Step 5: Wire loading from env / local.json**

In `arxlib/config/config.go`, in the same block that reads other DB fields (near `TestMode: os.Getenv("TEST_MODE") == "true"`), set:
```go
		Engine: os.Getenv("DB_ENGINE"),
```
and in the local-json override section (near where `local.TestMode` is applied), add - matching the existing `local.*` pointer-override style:
```go
		if local.Engine != nil {
			cfg.Engine = *local.Engine
		}
```
Add an `Engine *string` field to the local-json struct alongside the existing `TestMode *bool` (find the struct with `json:"test_mode"` and add `Engine *string \`json:"engine"\``).

- [x] **Step 6: Build + full config tests**

Run: `cd arxlib && go build ./... && go test ./config/`
Expected: PASS.

- [x] **Step 7: Commit**

```bash
git add arxlib/config/base.go arxlib/config/config.go arxlib/config/base_test.go
git commit -m "feat(config): add DB Engine field (default sqlserver) (#625)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012UtYghgWTw6J3BqAnqo5Zc"
```

---

### Task 1.2: Define the `Dialect` interface + `sqlServerDialect`

**Files:**
- Create: `arxlib/db/dialect.go`
- Create: `arxlib/db/dialect_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (relied on by every later Phase 1 task):
  ```go
  type Dialect interface {
      Name() string
      // Rewrite applies engine-specific textual transforms to a query written
      // in the app's canonical SQL Server-flavored form: @pN placeholders and
      // GETDATE(). SQL Server: identity.
      Rewrite(query string) string
      // TryCastInt returns a fragment that casts expr to an integer, yielding
      // NULL when expr is non-numeric (for numeric-sort-on-string columns).
      // SQL Server: "TRY_CAST(<expr> AS INT)".
      TryCastInt(expr string) string
      // MonthStartExpr returns an expression for midnight on the first day of
      // the current month. SQL Server: "DATEFROMPARTS(YEAR(GETDATE()), MONTH(GETDATE()), 1)".
      MonthStartExpr() string
      // UpsertAppConfig returns a full statement upserting a (key,value) pair
      // into the app_config-style table `table`, using @p1=key, @p2=value, and
      // touching updated_at. SQL Server: the current MERGE.
      UpsertAppConfig(table string) string
      // InsertReturningID returns a full statement that inserts one row into
      // `table` (columns `columnList`, values `valuesList`, both already
      // comma-formatted and using @pN placeholders) and returns the new integer
      // id as a single-column, single-row result to Scan. When hasTrigger is
      // true the SQL Server branch uses the INSERT + SCOPE_IDENTITY batch form.
      InsertReturningID(table, columnList, valuesList string, hasTrigger bool) string
      // TopClause / LimitClause implement "first N rows" pagination. Exactly one
      // is non-empty per dialect. SQL Server fills TopClause ("TOP (<ph>) "),
      // Postgres fills LimitClause ("LIMIT <ph>"). Callers place TopClause right
      // after SELECT and LimitClause at the end of the query. ph is a @pN marker.
      TopClause(ph string) string
      LimitClause(ph string) string
  }
  ```
  - `func NewSQLServerDialect() Dialect`

- [x] **Step 1: Write the failing test**

Create `arxlib/db/dialect_test.go`:
```go
package db

import "testing"

func TestSQLServerDialectIsIdentityAndTSQL(t *testing.T) {
	d := NewSQLServerDialect()

	if d.Name() != "sqlserver" {
		t.Errorf("Name: got %q", d.Name())
	}
	// Tier-1 rewrite is identity on SQL Server.
	q := "SELECT id FROM part WHERE x = @p1 AND created = GETDATE()"
	if got := d.Rewrite(q); got != q {
		t.Errorf("Rewrite should be identity on SQL Server:\n got %q\nwant %q", got, q)
	}
	if got := d.TryCastInt("serial_number"); got != "TRY_CAST(serial_number AS INT)" {
		t.Errorf("TryCastInt: got %q", got)
	}
	if got := d.MonthStartExpr(); got != "DATEFROMPARTS(YEAR(GETDATE()), MONTH(GETDATE()), 1)" {
		t.Errorf("MonthStartExpr: got %q", got)
	}
	if got := d.TopClause("@p2"); got != "TOP (@p2) " {
		t.Errorf("TopClause: got %q", got)
	}
	if got := d.LimitClause("@p2"); got != "" {
		t.Errorf("LimitClause should be empty on SQL Server: got %q", got)
	}
}
```

- [x] **Step 2: Run to verify it fails**

Run: `cd arxlib && go test ./db/ -run TestSQLServerDialect -v`
Expected: FAIL - `NewSQLServerDialect` undefined.

- [x] **Step 3: Implement `dialect.go`**

Create `arxlib/db/dialect.go`:
```go
package db

import "fmt"

// Dialect encapsulates the SQL constructs that differ between database engines.
// During the SQL Server -> Postgres migration (issue #625) a single SQL Server
// implementation exists; the Postgres one is added in Phase 2, and the whole
// interface collapses to Postgres-only at cutover.
type Dialect interface {
	Name() string
	Rewrite(query string) string
	TryCastInt(expr string) string
	MonthStartExpr() string
	UpsertAppConfig(table string) string
	InsertReturningID(table, columnList, valuesList string, hasTrigger bool) string
	TopClause(ph string) string
	LimitClause(ph string) string
}

type sqlServerDialect struct{}

// NewSQLServerDialect returns the SQL Server dialect. Every method reproduces
// the SQL the app emits today, so introducing the seam changes no behavior.
func NewSQLServerDialect() Dialect { return sqlServerDialect{} }

func (sqlServerDialect) Name() string { return "sqlserver" }

// Rewrite is identity: SQL Server keeps @pN placeholders and GETDATE().
func (sqlServerDialect) Rewrite(query string) string { return query }

func (sqlServerDialect) TryCastInt(expr string) string {
	return fmt.Sprintf("TRY_CAST(%s AS INT)", expr)
}

func (sqlServerDialect) MonthStartExpr() string {
	return "DATEFROMPARTS(YEAR(GETDATE()), MONTH(GETDATE()), 1)"
}

func (sqlServerDialect) UpsertAppConfig(table string) string {
	return "MERGE INTO " + table + " AS t\n" +
		"USING (SELECT @p1 AS k, @p2 AS v) AS s ON t.setting_key = s.k\n" +
		"WHEN MATCHED THEN UPDATE SET t.setting_value = s.v, t.updated_at = GETDATE()\n" +
		"WHEN NOT MATCHED THEN INSERT (setting_key, setting_value) VALUES (s.k, s.v);"
}

func (sqlServerDialect) InsertReturningID(table, columnList, valuesList string, hasTrigger bool) string {
	if hasTrigger {
		// OUTPUT INSERTED is blocked on tables with triggers; batch INSERT with
		// SCOPE_IDENTITY() so both run in the same scope.
		return fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s);\nSELECT CAST(SCOPE_IDENTITY() AS INT)",
			table, columnList, valuesList)
	}
	return fmt.Sprintf(
		"INSERT INTO %s (%s) OUTPUT INSERTED.id VALUES (%s)",
		table, columnList, valuesList)
}

func (sqlServerDialect) TopClause(ph string) string { return "TOP (" + ph + ") " }
func (sqlServerDialect) LimitClause(string) string  { return "" }
```

- [x] **Step 4: Run to verify it passes**

Run: `cd arxlib && go test ./db/ -run TestSQLServerDialect -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add arxlib/db/dialect.go arxlib/db/dialect_test.go
git commit -m "feat(db): Dialect interface + SQL Server implementation (#625)

Behavior-preserving seam: every method emits today's SQL. Postgres branch
lands in Phase 2.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012UtYghgWTw6J3BqAnqo5Zc"
```

---

### Task 1.3: `Connect` selects driver by engine and returns a `Dialect`

**Files:**
- Modify: `arxlib/db/db.go`
- Modify: `arx_go/main.go:35`, `arx_go/settings.go:402`

**Interfaces:**
- Consumes: `NewSQLServerDialect()` (Task 1.2), `Base.DBEngine()` (Task 1.1).
- Produces: `func Connect(engine, dsn string) (*sql.DB, Dialect, error)`.

- [x] **Step 1: Update `Connect`**

Replace the body of `arxlib/db/db.go`'s `Connect` with an engine switch. New signature and body:
```go
// Connect opens and verifies a database connection for the given engine and
// returns the pool plus the matching Dialect. Only "sqlserver" is wired up in
// Phase 1; "postgres" is added in Phase 2.
func Connect(engine, dsn string) (*sql.DB, Dialect, error) {
	var driver string
	var dialect Dialect
	switch engine {
	case "sqlserver":
		driver, dialect = "sqlserver", NewSQLServerDialect()
	default:
		return nil, nil, fmt.Errorf("unsupported db engine %q", engine)
	}

	database, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open: %w", err)
	}
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("ping: %w", err)
	}
	return database, dialect, nil
}
```
Keep the `_ "github.com/microsoft/go-mssqldb"` blank import.

- [x] **Step 2: Update the `main.go` call site**

In `arx_go/main.go` around line 35, change:
```go
		if conn, err := arxdb.Connect(dsn); err == nil {
			database = conn
```
to:
```go
		if conn, dialect, err := arxdb.Connect(cfg.DBEngine(), dsn); err == nil {
			database = conn
			dbDialect = dialect
```
Declare `var dbDialect arxdb.Dialect` next to `var database *sql.DB`, and pass it into `New(...)` (updated in Task 1.4, Step 2). Until Task 1.4 changes `New`'s signature, this file will not compile - do Task 1.3 and 1.4 together before building.

- [x] **Step 3: Update the `settings.go` call site**

In `arx_go/settings.go` around line 402, change:
```go
		newDB, err := arxdb.Connect(dsn)
```
to:
```go
		newDB, newDialect, err := arxdb.Connect(h.cfg.DBEngine(), dsn)
```
and, in the success branch where `h.db = newDB` is set, also set `h.dialect = newDialect` (the field is added in Task 1.4).

- [x] **Step 4: Defer build to Task 1.4**

`Connect`'s new signature ripples into `Handler` construction, so build after Task 1.4. Proceed to Task 1.4 now; commit both together at the end of 1.4.

---

### Task 1.4: Put the `Dialect` on `Handler` and wire `Rewrite` into the query wrappers

**Files:**
- Modify: `arx_go/handlers.go` (struct field, `New`, `logSQL`/wrappers, `appConfigSet`)
- Modify: `arx_go/main.go` (pass dialect into `New`)

**Interfaces:**
- Consumes: `Connect` (Task 1.3), `Dialect.Rewrite`, `Dialect.UpsertAppConfig` (Task 1.2).
- Produces: `h.dialect arxdb.Dialect`; all four wrappers apply `h.dialect.Rewrite(query)` before executing; `appConfigSet` routes through `h.dialect.UpsertAppConfig`.

- [x] **Step 1: Add the field**

In `arx_go/handlers.go`, add to `type Handler struct`:
```go
	dialect        arxdb.Dialect
```
(Confirm `arxdb` is the import alias for `arxlib/db` in this file; add it if missing.)

- [x] **Step 2: Thread it through `New`**

Change `New`'s signature (note `Dialect` lives in package `db`, alias `arxdb`, not `config`):
```go
func New(db *sql.DB, dialect arxdb.Dialect, cfg *arxbase.Config, tmplFS ioFS.FS, releaseNotes []byte) *Handler {
```
and set it in the returned struct literal:
```go
		db: db, dialect: dialect, cfg: cfg, store: store, tmplFS: tmplFS, releaseNotes: string(releaseNotes),
```
In `arx_go/main.go`, update the call: `h = New(database, dbDialect, cfg, templatesFS, releaseNotesData)`.

- [x] **Step 3: Guard against a nil dialect**

When `database` is nil (no password configured), `dbDialect` is nil too. `Rewrite` is only reached through the wrappers, which are only called when `h.db != nil`, so a nil dialect is never dereferenced on the hot path. To be safe in `New`, default it:
```go
	if dialect == nil {
		dialect = arxdb.NewSQLServerDialect()
	}
```

- [x] **Step 4: Apply `Rewrite` in the wrappers**

In `arx_go/handlers.go`, change each wrapper to rewrite the query once, before logging and executing. For `queryContext`:
```go
func (h *Handler) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	query = h.dialect.Rewrite(query)
	h.logSQL(query, args...)
	return timeQueryErr(ctx, query, func() (*sql.Rows, error) { return h.db.QueryContext(ctx, query, args...) })
}
```
Apply the identical `query = h.dialect.Rewrite(query)` first line to `queryRowContext` and `execContext`. For transactions, rewrite in `beginTx` by capturing the dialect in the `txLogger`:
```go
type txLogger struct {
	*sql.Tx
	logFn   func(string, ...any)
	rewrite func(string) string
}
```
and in each `txLogger` method prepend `query = t.rewrite(query)` (for `ExecContext`, `QueryRowContext`, `QueryContext`). In `beginTx`:
```go
	return &txLogger{Tx: tx, logFn: h.logSQL, rewrite: h.dialect.Rewrite}, nil
```
On SQL Server `Rewrite` is identity, so behavior is unchanged.

- [x] **Step 5: Route `appConfigSet` through the dialect**

Replace the `MERGE` literal in `appConfigSet` (`arx_go/handlers.go:243-248`):
```go
func (h *Handler) appConfigSet(ctx context.Context, key, value string) error {
	_, err := h.execContext(ctx, h.dialect.UpsertAppConfig(h.cfg.AppConfigTable()), key, value)
	return err
}
```

- [x] **Step 6: Build + vet + unit tests**

Run: `cd arx_go && go build ./... && go vet ./...` then `cd ../arxlib && go test ./...`
Expected: no errors; arxlib tests PASS.

- [x] **Step 7: Commit (Tasks 1.3 + 1.4 together)**

```bash
git add arxlib/db/db.go arx_go/main.go arx_go/settings.go arx_go/handlers.go
git commit -m "feat(db): thread Dialect through Connect and Handler; wire Rewrite + appConfig upsert (#625)

Connect now returns a Dialect per engine; Handler holds it; the query
wrappers apply dialect.Rewrite (identity on SQL Server); appConfigSet uses
dialect.UpsertAppConfig. No behavior change on SQL Server.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012UtYghgWTw6J3BqAnqo5Zc"
```

---

### Task 1.5: Route insert-and-get-id through `Dialect.InsertReturningID`

**Files (12 `OUTPUT INSERTED` + 2 `SCOPE_IDENTITY` sites):**
- Modify: `arx_go/contacts.go:215`, `arx_go/parts.go:411`, `arx_go/suppliers.go:242`, `arx_go/records.go:864,1465,1813,2451,2554,2662`, `arx_go/records_history.go:166`, `arx_go/named_query_settings.go:124`, `arx_go/pos.go:~513,~2380` (the two `SCOPE_IDENTITY` inserts, `hasTrigger=true`).

**Interfaces:**
- Consumes: `Dialect.InsertReturningID(table, columnList, valuesList string, hasTrigger bool) string` (Task 1.2).
- Produces: nothing new; every insert-returning-id call site now emits its SQL via the dialect.

**Transformation rule:** at each site, split the current inline SQL into `columnList` (the comma-joined columns), `valuesList` (the comma-joined `@pN` markers, plus any inline literals/`GETDATE()` already present), and replace the literal with `h.dialect.InsertReturningID(table, columnList, valuesList, hasTrigger)`. `hasTrigger` is `false` everywhere except the two `pos.go` PO inserts (the `purchase_order` table has triggers). Leave the surrounding `queryRowContext(...).Scan(&id)` / `tx.QueryRowContext(...).Scan(&id)` untouched - the helper's SQL Server output is byte-identical to today's for both branches.

- [x] **Step 1: Worked example - `parts.go:411` (non-trigger)**

Replace:
```go
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (part_number, revision, title, detail, category,
		                release_status, is_active, requested_by, notes, created_date, modified_date,
		                unit_id, current_cost,
		                user_field_1, user_field_2, user_field_3, user_field_4, user_field_5,
		                user_field_6, user_field_7, user_field_8, user_field_9, user_field_10)
		OUTPUT INSERTED.id
		VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,
		        @p12,@p13,
		        @p14,@p15,@p16,@p17,@p18,@p19,@p20,@p21,@p22,@p23)
	`, h.cfg.PartsTable()),
```
with:
```go
	insertPart := h.dialect.InsertReturningID(h.cfg.PartsTable(),
		`part_number, revision, title, detail, category,
		 release_status, is_active, requested_by, notes, created_date, modified_date,
		 unit_id, current_cost,
		 user_field_1, user_field_2, user_field_3, user_field_4, user_field_5,
		 user_field_6, user_field_7, user_field_8, user_field_9, user_field_10`,
		`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,
		 @p12,@p13,
		 @p14,@p15,@p16,@p17,@p18,@p19,@p20,@p21,@p22,@p23`,
		false)
	err := h.queryRowContext(r.Context(), insertPart,
```
(The trailing arg list stays exactly as-is.)

- [x] **Step 2: Worked example - `pos.go` PO insert (trigger table)**

At `pos.go` (the `INSERT INTO ... ; SELECT CAST(SCOPE_IDENTITY() AS INT)` block near line 513), replace the inline SQL literal with:
```go
	insertPO := h.dialect.InsertReturningID(h.cfg.POTable(),
		`number, status, is_active, orderer, account_id,
		 supplier_id, supplier_name, supplier_contact, supplier_email,
		 supplier_address, supplier_city, supplier_state, supplier_zipcode,
		 supplier_country, supplier_phone_number, supplier_fax_number,
		 receiver_id, receiver_name, receiver_contact, receiver_email,
		 receiver_address, receiver_city, receiver_state, receiver_zipcode,
		 receiver_country, receiver_phone, receiver_fax,
		 tax1, shipping_cost, misc_cost, notes, internal_notes, date_ordered,
		 date_requested, date_closed, date_modified, total_cost,
		 supplier_contact_id, receiver_contact_id`,
		`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16,
		 @p17,@p18,@p19,@p20,@p21,@p22,@p23,@p24,@p25,@p26,@p27,
		 @p28,@p29,@p30,@p31,@p32,@p33,@p34,@p35,@p36,@p37,@p38,@p39`,
		true)
	if err := tx.QueryRowContext(r.Context(), insertPO, ...
```
Keep the `tx.QueryRowContext(..., <args>).Scan(&newID)` and its argument list unchanged. Apply the same pattern to the second `SCOPE_IDENTITY` insert (`pos.go:~2380`, `hasTrigger=true`).

- [x] **Step 3: Apply the rule to the remaining 10 sites**

For each of `contacts.go:215`, `suppliers.go:242`, `records.go:864,1465,1813,2451,2554,2662`, `records_history.go:166`, `named_query_settings.go:124`: extract the column list and values list from the existing literal and route through `h.dialect.InsertReturningID(table, cols, vals, false)`. Note `records.go:2554/2662` and `named_query_settings.go:124` embed the `OUTPUT INSERTED.id` mid-string in a `fmt.Sprintf` template - split those the same way. `records_history.go:166` has `GETDATE()` inside its VALUES list; keep it inside `valuesList` verbatim (it is Tier-1 rewritten later).

- [x] **Step 4: Verify no stray insert-id T-SQL remains outside the dialect**

Run: `grep -rn "OUTPUT INSERTED\|SCOPE_IDENTITY" arx_go/*.go | grep -v _test`
Expected: no matches in handler files (only `arxlib/db/dialect.go` holds these strings). Test files are converted in Phase 2.

- [x] **Step 5: Build + vet**

Run: `cd arx_go && go build ./... && go vet ./...`
Expected: no errors.

- [x] **Step 6: Commit**

```bash
git add arx_go/contacts.go arx_go/parts.go arx_go/suppliers.go arx_go/records.go arx_go/records_history.go arx_go/named_query_settings.go arx_go/pos.go
git commit -m "refactor(db): route insert-and-get-id through Dialect.InsertReturningID (#625)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012UtYghgWTw6J3BqAnqo5Zc"
```

**Manual behavior check (user, ArxDev):** create a part, a supplier, a contact, a PO, and a test record; confirm each saves and the new row opens correctly (exercises both the `OUTPUT INSERTED` and `SCOPE_IDENTITY` branches).

---

### Task 1.6: Route pagination through `TopClause` / `LimitClause`

**Files (5 `TOP (@pN)` sites + 2 `OFFSET/FETCH` sites):**
- Modify: `arx_go/parts.go:1680,1721`, `arx_go/reports.go:79,105`, `arx_go/suppliers.go:151,191`, `arx_go/api.go:36,121`

**Interfaces:**
- Consumes: `Dialect.TopClause(ph)`, `Dialect.LimitClause(ph)` (Task 1.2).
- Produces: nothing new.

**Transformation rule:** for a "first N rows" query, insert `h.dialect.TopClause("@pN")` right after `SELECT ` and append `h.dialect.LimitClause("@pN")` at the very end (after any `ORDER BY`). On SQL Server `TopClause` = `"TOP (@pN) "` and `LimitClause` = `""`, reproducing today's SQL. The `@pN` marker must match the arg already bound for the row count.

- [x] **Step 1: Worked example - `reports.go:79`**

Replace:
```go
		`SELECT TOP (@p1) id, part_number, title, modified_date
```
with a Sprintf that injects the clause:
```go
		fmt.Sprintf(`SELECT %sid, part_number, title, modified_date
```
...and add `h.dialect.TopClause("@p1")` as the first Sprintf arg, plus `+ h.dialect.LimitClause("@p1")` appended to the end of the query string (outside the ORDER BY). Confirm the bound arg for `@p1` is still the limit value.

- [x] **Step 2: Worked example - `api.go:36` (already OFFSET/FETCH)**

`api.go:36,121` use a hardcoded `OFFSET 0 ROWS FETCH NEXT N ROWS ONLY`. These are already ANSI and portable to Postgres unchanged, so leave them as-is in Phase 1 and add a `// #625: portable OFFSET/FETCH, revisited in Phase 2` comment. (They are listed here only so the pagination task accounts for every site; no code change.)

- [x] **Step 3: Apply to the `TOP` sites**

Convert `parts.go:1680,1721`, `reports.go:105`, and `suppliers.go:191` with the Step 1 pattern. `suppliers.go:151` builds a `top = "TOP (@p2) "` variable - replace that assignment with `top = h.dialect.TopClause("@p2")` and add the matching `+ h.dialect.LimitClause("@p2")` at the end of the query it feeds.

- [x] **Step 4: Verify no stray `TOP (` remains outside the dialect**

Run: `grep -rn "TOP (" arx_go/*.go | grep -v _test`
Expected: no matches (the `"TOP (" ...` literal now lives only in `arxlib/db/dialect.go`).

- [x] **Step 5: Build + vet**

Run: `cd arx_go && go build ./... && go vet ./...`
Expected: no errors.

- [x] **Step 6: Commit**

```bash
git add arx_go/parts.go arx_go/reports.go arx_go/suppliers.go arx_go/api.go
git commit -m "refactor(db): route TOP-N pagination through Dialect clauses (#625)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012UtYghgWTw6J3BqAnqo5Zc"
```

**Manual behavior check (user, ArxDev):** load a part's PO history and recent-transactions widgets, the reports dashboard lists, and supplier part search; confirm each still returns the same capped row counts.

---

### Task 1.7: Route `TRY_CAST` through `Dialect.TryCastInt`

**Files (6 sites):**
- Modify: `arx_go/records.go:305,1171,1172,1360,1450,2764`

**Interfaces:**
- Consumes: `Dialect.TryCastInt(expr)` (Task 1.2).
- Produces: nothing new.

**Transformation rule:** replace each `TRY_CAST(<expr> AS INT)` occurrence with `%s` in a `fmt.Sprintf` and pass `h.dialect.TryCastInt("<expr>")`. Where two occurrences share one query (`1171,1172`), pass two args. Preserve the surrounding `ORDER BY ... DESC`, `LAG/LEAD OVER (...)`, and `COALESCE(MAX(...)+1, 1)` context exactly.

- [x] **Step 1: Worked example - `records.go:1360`**

Replace:
```go
	h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT COALESCE(MAX(TRY_CAST(serial_number AS INT)) + 1, 1)
		FROM %s WHERE form_id = @p1`, h.cfg.RecordsTable()), formID).Scan(&nextSN)
```
with:
```go
	h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT COALESCE(MAX(%s) + 1, 1)
		FROM %s WHERE form_id = @p1`, h.dialect.TryCastInt("serial_number"), h.cfg.RecordsTable()), formID).Scan(&nextSN)
```

- [x] **Step 2: Apply to the remaining sites**

Convert `records.go:305,1171,1172,1450,2764` the same way. `2764` casts `trec.serial_number` (keep the table alias inside the expr argument: `h.dialect.TryCastInt("trec.serial_number")`).

- [x] **Step 3: Verify no stray `TRY_CAST` remains outside the dialect**

Run: `grep -rn "TRY_CAST" arx_go/*.go | grep -v _test`
Expected: no matches.

- [x] **Step 4: Build + vet; commit**

Run: `cd arx_go && go build ./... && go vet ./...` (expect no errors), then:
```bash
git add arx_go/records.go
git commit -m "refactor(db): route TRY_CAST through Dialect.TryCastInt (#625)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012UtYghgWTw6J3BqAnqo5Zc"
```

**Manual behavior check (user, ArxDev):** open a form's record list (serial-number numeric sort), the prev/next record nav, and create a new record (next-SN suggestion); confirm ordering and the suggested serial number are unchanged.

---

### Task 1.8: Route `DATEFROMPARTS` through `Dialect.MonthStartExpr`

**Files (1 site):**
- Modify: `arx_go/reports.go:67`

**Interfaces:**
- Consumes: `Dialect.MonthStartExpr()` (Task 1.2).
- Produces: nothing new.

- [x] **Step 1: Edit the site**

Replace:
```go
		`SELECT COUNT(DISTINCT po_id) FROM %s WHERE date_received >= DATEFROMPARTS(YEAR(GETDATE()), MONTH(GETDATE()), 1)`,
```
with:
```go
		fmt.Sprintf(`SELECT COUNT(DISTINCT po_id) FROM %%s WHERE date_received >= %s`, h.dialect.MonthStartExpr()),
```
Adjust to the existing `fmt.Sprintf` structure at this call (the table name is already a `%s` arg - keep it as the trailing arg, and note the doubled `%%s` if nesting Sprintf; otherwise build the month-start fragment into a local `monthStart := h.dialect.MonthStartExpr()` and interpolate both in a single `fmt.Sprintf`). The SQL Server output is identical to today.

- [x] **Step 2: Verify + build + commit**

Run: `grep -rn "DATEFROMPARTS" arx_go/*.go | grep -v _test` (expect no matches), then `cd arx_go && go build ./... && go vet ./...` (expect no errors), then:
```bash
git add arx_go/reports.go
git commit -m "refactor(db): route DATEFROMPARTS through Dialect.MonthStartExpr (#625)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012UtYghgWTw6J3BqAnqo5Zc"
```

**Manual behavior check (user, ArxDev):** load the reports dashboard and confirm the "POs received this month" count is unchanged.

---

### Task 1.9: Phase 1 gate - full sweep + integration hand-off

**Files:** none (verification only).

- [x] **Step 1: Unit + build sweep**

Run:
```bash
cd arxlib && go test ./... && cd ../arx_go && go build ./... && go vet ./... && go test ./...
```
Expected: all PASS, no vet errors.

- [x] **Step 2: Confirm the seam is complete**

Run: `grep -rn "OUTPUT INSERTED\|SCOPE_IDENTITY\|TRY_CAST\|DATEFROMPARTS\|MERGE INTO\|TOP (" arx_go/*.go | grep -v _test`
Expected: no matches (all structural T-SQL now lives behind the dialect). `GETDATE()` and `@pN` intentionally remain in call sites - they are handled by the Tier-1 `Rewrite`, which is identity on SQL Server.

- [x] **Step 3: Integration suite (user-run, ArxDev)**

Hand off to the user to run the live-DB gate (this environment must not touch any DB):
```powershell
$env:ARX_TEST_DSN="sqlserver://<user>:<pass>@<server>?database=ArxDev&encrypt=true"
go test -tags integration ./arx_go/...
```
Expected: PASS - proving the dialect seam changed no SQL Server behavior. This is the real Phase 1 gate.

- [x] **Step 4: Update the design doc phase status**

Mark Phase 0 and Phase 1 complete in `docs/plans/625-postgres-migration-design.md` (a one-line status note under each phase), commit, and open/refresh the PR.

---

## Self-Review

**Spec coverage (design doc Phases 0-1):**
- Phase 0 `ISNULL`->`COALESCE` -> Task 0.1. Document `SCOPE_IDENTITY` gotcha -> Task 0.2. LIKE-concat -> dropped (no app sites, recorded above). Pagination consolidation -> moved to Task 1.6 (recorded above).
- Phase 1 `Engine` config + driver selection -> Tasks 1.1, 1.3. `Dialect` interface + SQL Server impl -> Task 1.2. Tier-1 rewriter in wrappers -> Task 1.4. Tier-2 categories routed: insert-id -> 1.5, pagination -> 1.6, upsert -> 1.4 Step 5, TryCast -> 1.7, MonthStart -> 1.8. Behavior-preserving gate -> Task 1.9.
- Out of scope confirmed absent: no Postgres driver/DDL/triggers, no data migration, no SQLite - all deferred to Phase 2+.

**Type consistency:** `Dialect` method names (`Rewrite`, `TryCastInt`, `MonthStartExpr`, `UpsertAppConfig`, `InsertReturningID`, `TopClause`, `LimitClause`, `Name`) are defined in Task 1.2 and used with those exact names/signatures in Tasks 1.4-1.8. `Connect(engine, dsn) (*sql.DB, Dialect, error)` (Task 1.3) matches its call sites in Tasks 1.3-1.4. `Base.DBEngine()` (Task 1.1) matches its uses in Task 1.3.

**Known follow-ups for Phase 2 (not this plan):** convert `integration_test.go`'s 7 `OUTPUT INSERTED` + `TRY_CAST` sites; add the Postgres branch to every dialect method; `SQL/postgres/*.sql`; trigger review + rewrite; `named_queries.sql` dialect handling; the `api.go` OFFSET/FETCH revisit.
