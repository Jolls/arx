# Record List Advanced Filters Implementation Plan (#247)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add server-side Status / Type / Date-range filters to the per-form test-record list (`GET /forms/{id}/records`), persisted as query params so filtered views are shareable.

**Architecture:** A small pure helper parses the query string into a `recordFilters` value and emits parameterized SQL `WHERE` fragments + args. `RecordsList` appends those to its existing query, adds a `DISTINCT comments` query to populate a Type datalist, and passes the parsed filters to the template. The template replaces the WIP toggle with a Bootstrap GET filter bar. No DB change.

**Tech Stack:** Go 1.x (`package main` in `arx_go/`), go-chi router, go-mssqldb (SQL Server, `@pN` positional params), `html/template`, Bootstrap 5.3.

## Global Constraints

- **No schema change** — pure binary swap; do not add columns or tables.
- **Never hardcode table names** — use `h.cfg.RecordsTable()` (= `test_record`), `h.cfg.ResultsTable()` (= `test_result`), `h.cfg.FormsTable()`, `h.cfg.PartsTable()`.
- **Always use handler wrappers**, never `h.db.*` directly: `h.queryContext(ctx, q, args...)`, `h.queryRowContext`, `h.execContext`.
- **Never interpolate user values into SQL** — only `@pN` placeholders with positional args. `formID` is always `@p1`; filter placeholders start at `@p2`.
- **Bootstrap classes over custom CSS.** Buttons always pair base + variant (`btn btn-sm btn-primary`).
- **CRLF repo, not gofmt-clean** — edit via tools; do **not** run `gofmt -w`.
- **Status default is `wip`** — missing/unrecognized `status` must behave exactly like today's default WIP-only view.
- **Build/test from inside the module:** `cd arx_go; go test ./...` (the repo root is not a module). Run `.bat` files (e.g. `build.bat`) with the PowerShell tool, not Bash.

---

### Task 1: Filter parsing + SQL clause builder (pure, unit-tested)

A self-contained, DB-free unit: parse the query string and emit WHERE fragments + args. This is the whole testable core of the feature.

**Files:**
- Create: `arx_go/records_filters.go`
- Test: `arx_go/records_filters_test.go`

**Interfaces:**
- Produces:
  - `type recordFilters struct { Status, Type string; From, To time.Time; FromStr, ToStr string }`
  - `func parseRecordFilters(q url.Values) recordFilters`
  - `func (f recordFilters) whereClauses(startArg int) (string, []any)`
  - `func (f recordFilters) StatusWIP() bool`

- [ ] **Step 1: Write the failing tests**

Create `arx_go/records_filters_test.go`:

```go
package main

import (
	"net/url"
	"testing"
	"time"
)

func TestParseRecordFilters_DefaultsToWIP(t *testing.T) {
	for _, in := range []string{"", "status=", "status=bogus"} {
		q, _ := url.ParseQuery(in)
		if got := parseRecordFilters(q).Status; got != "wip" {
			t.Errorf("parseRecordFilters(%q).Status = %q, want \"wip\"", in, got)
		}
	}
}

func TestParseRecordFilters_KeepsValidStatus(t *testing.T) {
	for _, s := range []string{"wip", "complete", "approved", "all"} {
		q := url.Values{"status": {s}}
		if got := parseRecordFilters(q).Status; got != s {
			t.Errorf("status %q parsed to %q", s, got)
		}
	}
}

func TestParseRecordFilters_DropsUnparseableDates(t *testing.T) {
	q := url.Values{"from": {"not-a-date"}, "to": {"2026-13-99"}}
	f := parseRecordFilters(q)
	if !f.From.IsZero() || f.FromStr != "" {
		t.Errorf("bad from kept: From=%v FromStr=%q", f.From, f.FromStr)
	}
	if !f.To.IsZero() || f.ToStr != "" {
		t.Errorf("bad to kept: To=%v ToStr=%q", f.To, f.ToStr)
	}
}

func TestParseRecordFilters_ParsesGoodDates(t *testing.T) {
	q := url.Values{"from": {"2026-01-02"}, "to": {"2026-03-04"}}
	f := parseRecordFilters(q)
	if f.From.IsZero() || f.FromStr != "2026-01-02" {
		t.Errorf("from not parsed: %v / %q", f.From, f.FromStr)
	}
	if f.To.IsZero() || f.ToStr != "2026-03-04" {
		t.Errorf("to not parsed: %v / %q", f.To, f.ToStr)
	}
}

func TestWhereClauses_WIPNoExtras(t *testing.T) {
	f := parseRecordFilters(url.Values{}) // status=wip, nothing else
	sql, args := f.whereClauses(2)
	if sql != " AND is_locked = 0" {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want none", args)
	}
}

func TestWhereClauses_StatusVariants(t *testing.T) {
	cases := map[string]string{
		"complete": " AND is_locked = 1 AND is_approved = 0",
		"approved": " AND is_approved = 1",
		"all":      "",
	}
	for status, want := range cases {
		f := parseRecordFilters(url.Values{"status": {status}})
		sql, _ := f.whereClauses(2)
		if sql != want {
			t.Errorf("status %q: sql = %q, want %q", status, sql, want)
		}
	}
}

func TestWhereClauses_AllFiltersNumberedFromStart(t *testing.T) {
	q := url.Values{
		"status": {"all"},
		"type":   {"Re-Test"},
		"from":   {"2026-01-02"},
		"to":     {"2026-03-04"},
	}
	f := parseRecordFilters(q)
	sql, args := f.whereClauses(2)
	want := " AND comments = @p2 AND record_date >= @p3 AND record_date < DATEADD(day, 1, @p4)"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(args) != 3 {
		t.Fatalf("args len = %d, want 3", len(args))
	}
	if args[0] != "Re-Test" {
		t.Errorf("args[0] = %v, want Re-Test", args[0])
	}
	if _, ok := args[1].(time.Time); !ok {
		t.Errorf("args[1] = %T, want time.Time", args[1])
	}
}

func TestStatusWIP(t *testing.T) {
	if !parseRecordFilters(url.Values{}).StatusWIP() {
		t.Error("default should report StatusWIP() true")
	}
	if parseRecordFilters(url.Values{"status": {"all"}}).StatusWIP() {
		t.Error("status=all should report StatusWIP() false")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd arx_go; go test ./... -run TestParseRecordFilters -v`
Expected: FAIL — compile error `undefined: parseRecordFilters` (file not created yet).

- [ ] **Step 3: Create the implementation**

Create `arx_go/records_filters.go`:

```go
package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// recordFilters holds the parsed, validated filter selections from the
// records-list query string. The zero value with Status defaulted to "wip"
// (as parseRecordFilters always sets) is the historic default WIP-only view.
type recordFilters struct {
	Status  string    // "wip" | "complete" | "approved" | "all"
	Type    string    // exact match on test_record.comments; "" = no filter
	From    time.Time // record_date lower bound; zero = no filter
	To      time.Time // record_date upper bound (inclusive day); zero = no filter
	FromStr string    // original YYYY-MM-DD input, for repopulating the form
	ToStr   string    // original YYYY-MM-DD input, for repopulating the form
}

const recordFilterDateLayout = "2006-01-02"

// parseRecordFilters reads the filter params from a records-list query string.
// Missing or unrecognized status falls back to "wip" (preserving the historic
// default view). Unparseable dates are ignored (and their *Str cleared).
func parseRecordFilters(q url.Values) recordFilters {
	f := recordFilters{
		Status: q.Get("status"),
		Type:   q.Get("type"),
	}
	switch f.Status {
	case "wip", "complete", "approved", "all":
		// valid as-is
	default:
		f.Status = "wip"
	}

	f.FromStr = q.Get("from")
	if t, err := time.Parse(recordFilterDateLayout, f.FromStr); err == nil {
		f.From = t
	} else {
		f.FromStr = ""
	}

	f.ToStr = q.Get("to")
	if t, err := time.Parse(recordFilterDateLayout, f.ToStr); err == nil {
		f.To = t
	} else {
		f.ToStr = ""
	}

	return f
}

// StatusWIP reports whether the WIP status filter is active. The records list
// only shows the row-select column and bulk-lock toolbar in this mode.
func (f recordFilters) StatusWIP() bool { return f.Status == "wip" }

// whereClauses builds the SQL WHERE fragments and positional args implied by the
// filters. Each fragment begins with " AND " so the caller can concatenate it
// onto an existing WHERE. Placeholders are numbered starting at startArg; the
// caller is responsible for @p1..@p(startArg-1) (formID is @p1, so pass 2).
func (f recordFilters) whereClauses(startArg int) (string, []any) {
	var sb strings.Builder
	var args []any
	n := startArg

	switch f.Status {
	case "wip":
		sb.WriteString(" AND is_locked = 0")
	case "complete":
		sb.WriteString(" AND is_locked = 1 AND is_approved = 0")
	case "approved":
		sb.WriteString(" AND is_approved = 1")
	case "all":
		// no clause
	}

	if f.Type != "" {
		fmt.Fprintf(&sb, " AND comments = @p%d", n)
		args = append(args, f.Type)
		n++
	}
	if !f.From.IsZero() {
		fmt.Fprintf(&sb, " AND record_date >= @p%d", n)
		args = append(args, f.From)
		n++
	}
	if !f.To.IsZero() {
		fmt.Fprintf(&sb, " AND record_date < DATEADD(day, 1, @p%d)", n)
		args = append(args, f.To)
		n++
	}

	return sb.String(), args
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd arx_go; go test ./... -run 'TestParseRecordFilters|TestWhereClauses|TestStatusWIP' -v`
Expected: PASS — all eight tests green.

- [ ] **Step 5: Run the full module test sweep (no regressions)**

Run: `cd arx_go; go test ./...`
Expected: `ok` for all packages.

- [ ] **Step 6: Commit**

```bash
git add arx_go/records_filters.go arx_go/records_filters_test.go
git commit -m "feat(tr): record-list filter parser + SQL clause builder (#247)"
```

---

### Task 2: Wire filters into the `RecordsList` handler

**Files:**
- Modify: `arx_go/records.go` — `RecordsList` (currently lines ~223–293)

**Interfaces:**
- Consumes: `parseRecordFilters`, `recordFilters.whereClauses`, `recordFilters.StatusWIP` (Task 1).
- Produces: template data keys `Filters` (`recordFilters`), `TypeOptions` (`[]string`) on `records_index.html`. Removes the old `WIPOnly` key.

- [ ] **Step 1: Replace the WIP-only logic and query assembly**

In `RecordsList`, replace this block (the current `wipOnly` default through the `h.queryContext` call):

```go
	// Default to WIP-only; ?wip=false shows all.
	wipOnly := r.URL.Query().Get("wip") != "false"

	query := fmt.Sprintf(`
		SELECT id, form_id, COALESCE(part_number_id,0), serial_number, serial_number_pn, serial_number_pn_desc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_approved, is_active, test_order
		FROM %s
		WHERE form_id = @p1 AND is_active = 1`, h.cfg.RecordsTable())
	if wipOnly {
		query += " AND is_locked = 0"
	}
	// serial_number + 0 forces numeric sort (same trick as Ruby Arel version)
	query += " ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC"

	rows, err := h.queryContext(r.Context(), query, formID)
```

with:

```go
	filters := parseRecordFilters(r.URL.Query())
	clauses, filterArgs := filters.whereClauses(2)

	query := fmt.Sprintf(`
		SELECT id, form_id, COALESCE(part_number_id,0), serial_number, serial_number_pn, serial_number_pn_desc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_approved, is_active, test_order
		FROM %s
		WHERE form_id = @p1 AND is_active = 1`, h.cfg.RecordsTable())
	query += clauses
	// serial_number + 0 forces numeric sort (same trick as Ruby Arel version)
	query += " ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC"

	args := append([]any{formID}, filterArgs...)
	rows, err := h.queryContext(r.Context(), query, args...)
```

- [ ] **Step 2: Add the Type datalist query before the render call**

Immediately after the `for rows.Next()` scan loop closes (after `rows.Close()` via the existing `defer`, and after `lockedCount` is computed), add:

```go
	// Distinct Type (comments) values for this form, to populate the filter datalist.
	var typeOptions []string
	typeRows, terr := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT DISTINCT comments FROM %s
		WHERE form_id = @p1 AND is_active = 1 AND comments <> ''
		ORDER BY comments`, h.cfg.RecordsTable()), formID)
	if terr == nil {
		defer typeRows.Close()
		for typeRows.Next() {
			var c string
			if typeRows.Scan(&c) == nil {
				typeOptions = append(typeOptions, c)
			}
		}
	}
```

- [ ] **Step 3: Update the render data map**

Replace the `h.renderTR(w, r, "records_index.html", map[string]any{...})` call's map with:

```go
	h.renderTR(w, r, "records_index.html", map[string]any{
		"Form":        form,
		"Records":     records,
		"Filters":     filters,
		"TypeOptions": typeOptions,
		"LockedCount": lockedCount,
		"CSRFToken":   h.csrfToken(w, r),
		"ActiveTab":   "records",
		"TestMode":    h.cfg.TestMode,
	})
```

(`WIPOnly` is removed; the template now reads `.Filters.StatusWIP` and `.Filters.Status`.)

- [ ] **Step 4: Verify it compiles (template still references WIPOnly — that's fixed in Task 3)**

Run: `cd arx_go; go build ./...`
Expected: builds cleanly. (Go does not type-check template field access, so this passes even before Task 3.)

- [ ] **Step 5: Run the module tests**

Run: `cd arx_go; go test ./...`
Expected: `ok` — no Go test references the removed `WIPOnly` key.

- [ ] **Step 6: Commit**

```bash
git add arx_go/records.go
git commit -m "feat(tr): apply status/type/date filters in RecordsList (#247)"
```

---

### Task 3: Filter bar UI + StatusWIP gating in the template

**Files:**
- Modify: `arx_go/templates/tr/records_index.html`

**Interfaces:**
- Consumes: `.Filters` (`recordFilters` with exported fields `Status`, `Type`, `FromStr`, `ToStr` and method `StatusWIP`), `.TypeOptions` (`[]string`), `.Form.ID`.

- [ ] **Step 1: Remove the old WIP toggle buttons**

Delete this block (the `{{if .WIPOnly}} … {{end}}` toggle inside the header button group):

```html
    {{if .WIPOnly}}
    <a href="?wip=false" class="btn btn-sm btn-outline-secondary">
      <i class="bi bi-eye"></i> Show All (incl. Locked)
    </a>
    {{else}}
    <a href="?" class="btn btn-sm btn-outline-secondary">
      <i class="bi bi-funnel"></i> WIP Only
    </a>
    {{end}}
```

- [ ] **Step 2: Update the Columns dropdown "Select" gate**

Change the one line that conditionally lists the Select column toggle from `{{if .WIPOnly}}` to `{{if .Filters.StatusWIP}}`:

```html
        {{if .Filters.StatusWIP}}<li><label class="dropdown-item d-flex gap-2 py-1"><input type="checkbox" data-toggle-col="col-select" checked> Select</label></li>{{end}}
```

- [ ] **Step 3: Add the filter bar after the header `</div>`**

Immediately after the `<div class="d-flex justify-content-between align-items-center mb-3"> … </div>` header block closes (before the `{{if .WIPOnly}}` bulk-actions block), insert:

```html
<form method="GET" class="row g-2 align-items-end mb-3">
  <div class="col-auto">
    <label class="form-label mb-1 small text-muted">Status</label>
    <select name="status" class="form-select form-select-sm">
      <option value="wip"      {{if eq .Filters.Status "wip"}}selected{{end}}>WIP</option>
      <option value="complete" {{if eq .Filters.Status "complete"}}selected{{end}}>Complete</option>
      <option value="approved" {{if eq .Filters.Status "approved"}}selected{{end}}>Approved</option>
      <option value="all"      {{if eq .Filters.Status "all"}}selected{{end}}>All</option>
    </select>
  </div>
  <div class="col-auto">
    <label class="form-label mb-1 small text-muted">Type</label>
    <input type="text" name="type" list="type-options" class="form-control form-control-sm"
           value="{{.Filters.Type}}" placeholder="Any" autocomplete="off">
    <datalist id="type-options">
      {{range .TypeOptions}}<option value="{{.}}"></option>{{end}}
    </datalist>
  </div>
  <div class="col-auto">
    <label class="form-label mb-1 small text-muted">From</label>
    <input type="date" name="from" class="form-control form-control-sm" value="{{.Filters.FromStr}}">
  </div>
  <div class="col-auto">
    <label class="form-label mb-1 small text-muted">To</label>
    <input type="date" name="to" class="form-control form-control-sm" value="{{.Filters.ToStr}}">
  </div>
  <div class="col-auto">
    <button type="submit" class="btn btn-sm btn-primary"><i class="bi bi-funnel"></i> Apply</button>
    <a href="/forms/{{.Form.ID}}/records" class="btn btn-sm btn-outline-secondary">Clear</a>
  </div>
</form>
```

- [ ] **Step 4: Update the remaining `WIPOnly` references**

Change every remaining `{{if .WIPOnly}}` / `{{if $.WIPOnly}}` in this file to use `.Filters.StatusWIP` / `$.Filters.StatusWIP`. There are four:
- the bulk-actions `<div id="bulk-actions">` wrapper: `{{if .Filters.StatusWIP}}`
- the `<th data-col="col-select">` header: `{{if .Filters.StatusWIP}}`
- the empty-state `colspan`: `colspan="{{if .Filters.StatusWIP}}7{{else}}6{{end}}"`
- the per-row select `<td>`: `{{if $.Filters.StatusWIP}}`

Leave the JS block (`#select-all` guard) unchanged — it already no-ops when the select column is absent.

- [ ] **Step 5: Build the binary and smoke-test manually**

Run (PowerShell tool): `cd arx_go; .\build.bat`
Expected: tests pass, `Arx.exe` is produced.

Then start the app (`arx_go\start.ps1` or run `Arx.exe` from `arx_go\`), open a form's records page, and verify:
- The filter bar shows Status/Type/From/To + Apply/Clear.
- Default view is WIP-only (Status = WIP) and the Select column + bulk-lock appear.
- Selecting Complete / Approved / All filters the rows and the URL gains `?status=…`.
- Typing/selecting a Type value and a date range filters and is reflected in the URL; reloading the URL keeps the filter state.
- Clear returns to the bare records URL (default WIP view).

- [ ] **Step 6: Commit**

```bash
git add arx_go/templates/tr/records_index.html
git commit -m "feat(tr): record-list filter bar UI; replace WIP toggle (#247)"
```

---

### Task 4 (optional): Integration test against ArxDev

Validates that the builder's SQL fragments actually run on SQL Server and filter correctly — covers the `DATEADD` / implicit date-conversion path the unit test can't. Build-tagged, ArxDev only, excluded from `build.bat`. Only do this task if live-DB coverage is wanted.

**Files:**
- Modify: `arx_go/integration_test.go`

**Interfaces:**
- Consumes: `liveHandler` (existing helper), `recordFilters.whereClauses`, `h.cfg.RecordsTable()`, `h.cfg.FormsTable()`.

- [ ] **Step 1: Add the test function**

Append to `arx_go/integration_test.go`:

```go
func TestIntegration_RecordFilters(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	// Seed one throwaway form (part_number_id is NOT NULL but unconstrained).
	var formID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (0, '', 0, 1)`, h.cfg.FormsTable()),
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE form_id=@p1`, h.cfg.RecordsTable()), formID)
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
	}()

	// Seed four records: WIP/Complete/Approved + a type/date spread.
	type seed struct {
		sn          string
		comments    string
		date        string
		locked, app int
	}
	seeds := []seed{
		{"101", "New Release", "2026-01-10", 0, 0}, // WIP
		{"102", "Re-Test", "2026-02-10", 1, 0},     // Complete
		{"103", "Re-Test", "2026-03-10", 1, 1},     // Approved
		{"104", "New Release", "2026-04-10", 0, 0}, // WIP
	}
	for _, s := range seeds {
		if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (form_id, record_date, serial_number, comments, is_locked, is_approved, is_active)
			 VALUES (@p1, @p2, @p3, @p4, @p5, @p6, 1)`, h.cfg.RecordsTable()),
			formID, s.date, s.sn, s.comments, s.locked, s.app); err != nil {
			t.Fatalf("seed record %s: %v", s.sn, err)
		}
	}

	// run applies a filter's clauses to the form's records and returns matching SNs.
	run := func(q url.Values) []string {
		f := parseRecordFilters(q)
		clauses, fargs := f.whereClauses(2)
		query := fmt.Sprintf(
			`SELECT serial_number FROM %s WHERE form_id = @p1 AND is_active = 1%s
			 ORDER BY TRY_CAST(serial_number AS INT)`, h.cfg.RecordsTable(), clauses)
		rows, err := h.queryContext(ctx, query, append([]any{formID}, fargs...)...)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var sn string
			if rows.Scan(&sn) == nil {
				out = append(out, sn)
			}
		}
		return out
	}

	eq := func(name string, got, want []string) {
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}

	eq("wip", run(url.Values{}), []string{"101", "104"})
	eq("complete", run(url.Values{"status": {"complete"}}), []string{"102"})
	eq("approved", run(url.Values{"status": {"approved"}}), []string{"103"})
	eq("all", run(url.Values{"status": {"all"}}), []string{"101", "102", "103", "104"})
	eq("type", run(url.Values{"status": {"all"}, "type": {"Re-Test"}}), []string{"102", "103"})
	eq("daterange", run(url.Values{"status": {"all"}, "from": {"2026-02-01"}, "to": {"2026-03-31"}}),
		[]string{"102", "103"})
}
```

- [ ] **Step 2: Run the integration test against ArxDev**

Run (PowerShell tool):

```powershell
$env:ARX_TEST_DSN="sqlserver://user:pass@server?database=ArxDev&encrypt=true"
cd arx_go; go test -tags integration -run TestIntegration_RecordFilters -v ./...
```

Expected: PASS (or SKIP if `ARX_TEST_DSN` is unset). The `to=2026-03-31` range includes `103` (2026-03-10) via the `DATEADD(day,1,…)` upper bound.

- [ ] **Step 3: Confirm the default sweep still excludes it**

Run: `cd arx_go; go test ./...`
Expected: `ok` — the build-tagged test does not run without `-tags integration`.

- [ ] **Step 4: Commit**

```bash
git add arx_go/integration_test.go
git commit -m "test(tr): integration coverage for record-list filters (#247)"
```

---

### Final: Changelog

After the feature tasks, add a single changelog entry (one per PR, per repo convention).

- [ ] **Step 1: Add the entry**

In `CHANGELOG.md`, under a new version heading (next patch above the current top entry), add:

```
- Advanced filters (status, type, date range) on the test-record list, persisted as shareable query params ([#247](https://github.com/Jolls/arx-legacy/issues/247))
```

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: changelog for record-list advanced filters (#247)"
```

---

## Self-Review

**Spec coverage**
- Status filter replacing WIP toggle → Task 1 (`whereClauses` status cases) + Task 3 (Status select, StatusWIP gating). ✓
- Type combobox (datalist, distinct comments, free-typeable) → Task 2 (datalist query) + Task 3 (input + datalist). ✓
- Date range (`from`/`to`, inclusive day via `DATEADD`) → Task 1 + Task 3. ✓
- Persist via query params (GET form) → Task 3 (`method="GET"`), repopulation from `.Filters`. ✓
- Pass/fail descoped → no task; intentionally absent. ✓
- No DB change → confirmed; only reads. ✓
- Verification → Task 1 unit tests (required) + Task 4 integration (optional). ✓
- Knock-on: select column / bulk-lock gating, post-bulk-lock redirect unaffected, `?wip` dropped → Task 2/3. ✓

**Placeholder scan:** No TBD/TODO/"handle edge cases"/"similar to". Every code step shows full code. ✓

**Type consistency:** `recordFilters` fields and methods (`Status`, `Type`, `From`/`To`, `FromStr`/`ToStr`, `whereClauses`, `StatusWIP`) are referenced identically across Tasks 1–4 and the template. Placeholder numbering contract (`formID=@p1`, filters start at `@p2`) is consistent between `whereClauses(2)` calls in Tasks 2 and 4. ✓
