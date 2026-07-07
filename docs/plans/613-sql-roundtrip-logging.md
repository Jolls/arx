# Plan — SQL round-trip counting & per-request timing (issue #613)

## Context

Issue [#613](https://github.com/Jolls/arx-legacy/issues/613) asks for logs that show **how many SQL round
trips** a request makes and **how long it takes**, for troubleshooting and efficiency testing
("fewer round trips is better"). Today the app logs each query individually (`[SQL] …` when
`DEBUG_MODE` is on) but gives no per-request rollup — you can't tell at a glance that a page fired
40 queries or spent 800ms in the DB. This plan adds a per-request summary line plus a slow-query
flag, gated by the existing `DEBUG_MODE`.

Scope decisions (confirmed with user):
- **Output:** per-request summary log line + flag individual slow queries.
- **Gating:** reuse `DEBUG_MODE` (no new config flag / Settings plumbing).
- **Function timing:** per-request duration + total DB time only; no separate function-level
  instrumentation.
- **Profiling harness:** add a curated, GET-only integration test that seeds the counter and calls
  representative page handlers directly against ArxDev, logging round trips + timing per route
  (build-tagged `integration`, excluded from the default test sweep).

## Key architecture facts (from exploration)

- **Every SQL call funnels through the handler wrappers** in `arx_go/handlers.go:46-90`
  (`queryContext`, `queryRowContext`, `execContext`) and the `txLogger` methods (`handlers.go:66-79`).
  These are the single choke point. All of them receive a `ctx`, so a request-scoped counter pulled
  from the context works everywhere.
- `logSQL` (`handlers.go:39`) fires *before* the DB call, so it can only count, not time. Timing
  must wrap the actual `h.db.*Context(...)` call inside each wrapper.
- **Request-scoped values use an established pattern**: `auth.go:17-20` defines
  `type contextKey int` / `const ctxUserKey contextKey = 1`, seeded via `context.WithValue` in
  `withUser`. The counter reuses this exact mechanism.
- `h` is a package-level singleton (`main.go:24`), so the counter **cannot** live on `h` — it must
  be per-request in the context.
- Middleware stack is in `buildRouter` (`main.go:106-110`): `middleware.Logger`,
  `middleware.Recoverer`, `h.RequireCsrfOnPost`. The new profiling middleware slots in here.
- `DebugMode` lives on `h.cfg.DebugMode` (`arxbase.Config` / `base.go:26`); already toggleable at
  runtime via Settings (`settings.go:254-266`). No config changes needed.
- Debug output already routes to a Windows debug console (`console_windows.go`) when DebugMode is on,
  via stdlib `log`. New lines use the same `log.Printf`, so they appear there automatically.

## Implementation

### 1. `arx_go/auth.go` — add a context key
Add alongside the existing key:
```go
const ctxSQLStatsKey contextKey = 2
```

### 2. `arx_go/handlers.go` — counter struct, helper, threshold, and wrapper edits

Add near the top of the file:
```go
const slowQueryThreshold = 100 * time.Millisecond

// sqlStats accumulates per-request SQL round-trip count and total DB time.
// One goroutine handles a request and its queries run sequentially, so no lock.
type sqlStats struct {
	count int
	total time.Duration
}

// recordRoundTrip attributes one DB round trip to the request's sqlStats (if the
// profiling middleware seeded one) and flags queries slower than the threshold.
// A no-op when no sqlStats is in ctx (i.e. DebugMode off), which gates all of this.
func recordRoundTrip(ctx context.Context, query string, elapsed time.Duration) {
	st, ok := ctx.Value(ctxSQLStatsKey).(*sqlStats)
	if !ok {
		return
	}
	st.count++
	st.total += elapsed
	if elapsed >= slowQueryThreshold {
		log.Printf("[SLOW SQL] %s | %s", elapsed.Round(time.Millisecond),
			strings.Join(strings.Fields(query), " "))
	}
}
```

Wrap the DB call in each of the three wrappers (`queryContext`, `queryRowContext`, `execContext`)
and the three `txLogger` methods (`ExecContext`, `QueryContext`, `QueryRowContext`). Pattern
(shown for `queryContext`):
```go
func (h *Handler) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	h.logSQL(query, args...)
	start := time.Now()
	rows, err := h.db.QueryContext(ctx, query, args...)
	recordRoundTrip(ctx, query, time.Since(start))
	return rows, err
}
```
`queryRowContext` returns `*sql.Row` (no error) — same shape, capture the return then record.
The `txLogger` methods have `ctx` already, so they call `recordRoundTrip(ctx, query, elapsed)` the
same way. `Commit`/`Rollback` take no ctx and are cheap — leave them uncounted.

Note: for `QueryContext`, the timed span covers issuing the query and getting the first result set,
not row iteration/scanning (which happens in the caller). This matches the "round trip" framing.

### 3. `arx_go/handlers.go` (or `main.go`) — profiling middleware
```go
func (h *Handler) profileRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.cfg.DebugMode {
			next.ServeHTTP(w, r)
			return
		}
		st := &sqlStats{}
		start := time.Now()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxSQLStatsKey, st)))
		log.Printf("[PERF] %s %s | %d round trips | %s in DB | %s total",
			r.Method, r.URL.Path, st.count,
			st.total.Round(time.Millisecond), time.Since(start).Round(time.Millisecond))
	})
}
```

### 4. `arx_go/main.go` — register the middleware
Add in `buildRouter` after the existing middleware (line ~110):
```go
r.Use(h.profileRequest)
```
Placed after `middleware.Logger`/`Recoverer` so it wraps all app routes. Static-asset requests make
no DB calls, so they'll simply log `0 round trips` (harmless) — acceptable; can early-return on the
`/static/` prefix if the noise is unwanted.

### 5. `arx_go/integration_test.go` — curated round-trip profiling harness

Add a `//go:build integration` test `TestIntegration_RouteRoundTrips` (same file / build tag as the
existing live-DB suite, so it stays out of the default `go test` sweep and `build.bat`). It reuses
the existing scaffolding: `liveHandler(t)` (opens ArxDev via `ARX_TEST_DSN`, already hard-fails
unless the DSN contains `arxdev`) and `withID` (`integration_test.go:26,53`).

Because it calls handlers **directly** (bypassing `buildRouter`/`RequireAuth`/CSRF — exactly as the
existing `TestIntegration_*` tests do, so no login/session/CSRF plumbing is needed), the profiling
middleware never runs — so the test seeds the counter into the request context itself:

```go
func TestIntegration_RouteRoundTrips(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	// Fixed seed IDs from SQL/seed_test_data.sql (same ones the other integration tests use).
	const seedPartID, seedSupplierID, seedPOID = /* … */, /* … */, /* … */

	cases := []struct {
		name    string
		method  http.Handler // or a func(w, r)
		target  string
		id      int          // 0 = no {id} param
	}{
		{"parts list", h.PartsList, "/", 0},
		{"part detail", h.PartDetail, "/part/{id}", seedPartID},
		{"part BOM", h.PartBOM, "/part/{id}/bom", seedPartID},
		{"part build-cost", h.PartBuildCost, "/part/{id}/build-cost", seedPartID},
		{"part price-history", h.PartPriceHistory, "/part/{id}/price-history", seedPartID},
		{"part orders", h.PartOrders, "/part/{id}/orders", seedPartID},
		{"suppliers list", h.SuppliersList, "/suppliers", 0},
		{"supplier detail", h.SupplierDetail, "/supplier/{id}", seedSupplierID},
		{"PO list", h.POList, "/pos", 0},
		{"PO detail", h.PODetail, "/po/{id}", seedPOID},
		{"contacts list", h.ContactsList, "/contacts", 0},
		{"records/forms list", h.FormsList, "/records", 0},
	}

	for _, c := range cases {
		st := &sqlStats{}
		req := httptest.NewRequest(http.MethodGet, c.target, nil)
		if c.id != 0 {
			req = withID(req, c.id)
		}
		req = req.WithContext(context.WithValue(req.Context(), ctxSQLStatsKey, st))
		rec := httptest.NewRecorder()
		start := time.Now()
		c.method.ServeHTTP(rec, req) // adapt to however handlers are typed (http.HandlerFunc)
		t.Logf("[PROFILE] %-22s %-28s %3d round trips  %s (status %d)",
			c.name, c.target, st.count, time.Since(start).Round(time.Millisecond), rec.Code)
	}
}
```

Scope notes:
- **GET/read routes only.** POST routes mutate ArxDev and need CSRF + form bodies — out of scope
  (and undesirable for a profiling harness). The list/detail pages above are where round-trip count
  actually matters.
- **Curated, not auto-discovered.** A maintained list of representative pages, not every registered
  route. Chosen for reliability (real seeded IDs, param-free page handlers). Auto-walking the mux via
  `chi.Walk` is a possible future upgrade but needs login + generic param-filling.
- Run with `t.Logf` + `-v` so counts print without failing the test; optionally assert a soft upper
  bound per route later to catch N+1 regressions.
- Seed IDs must come from `SQL/seed_test_data.sql`; if a route returns a non-200 the line still logs
  (status shown) so a stale/missing seed row is visible rather than a hard failure.

Run manually (per CLAUDE.md integration-test section):
```powershell
$env:ARX_TEST_DSN="sqlserver://user:pass@server?database=ArxDev&encrypt=true"
go test -tags integration -run TestIntegration_RouteRoundTrips -v ./arx_go/...
```

## Verification

- `cd arx_go && go build ./... && go vet ./... && go test ./...` — must pass (per CLAUDE.md build rules; run via PowerShell, not Bash).
- Profiling harness (user, needs live ArxDev): `go test -tags integration -run
  TestIntegration_RouteRoundTrips -v ./arx_go/...` with `ARX_TEST_DSN` set to ArxDev — prints a
  `[PROFILE] … N round trips …` line per curated route. This is the primary deliverable for the
  "which pages fire the most round trips" goal.
- Manual (user, since we don't run the app): with `DEBUG_MODE=true`, load the Parts list / a part
  detail page and confirm the debug console shows a `[PERF] GET /parts | N round trips | Xms in DB |
  Yms total` line, and that any query over 100ms produces a `[SLOW SQL]` line. Confirm that with
  `DEBUG_MODE=false` no `[PERF]`/`[SLOW SQL]` lines appear and there's no measurable overhead.

## Suggested test (optional, per CLAUDE.md #5)
A small unit test for `recordRoundTrip` is worthwhile: (a) with a `*sqlStats` in ctx it increments
`count` and accumulates `total`; (b) with no stats in ctx it's a no-op (guards the DebugMode-off
path). This locks in the gating behavior, which is the easiest thing to silently break. Will add
only if you agree.

## Changelog
One line under a new version entry in `CHANGELOG.md` on merge:
`- Add per-request SQL round-trip count + timing to debug logs ([#613](https://github.com/Jolls/arx-legacy/issues/613))`
