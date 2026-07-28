# #816 — Test coverage for ReportsDashboard handler

## Open questions
None — investigation resolved all ambiguity (see "Auth/router setup" below).

## 1. Test file
Extend `arx_go/integration_test.go` (build tag `integration`). This is where the
three existing per-card dashboard tests already live:
- `TestIntegration_DashboardStaleWIPRecords` (line ~1444)
- `TestIntegration_DashboardPendingApprovalPOs` (line ~1479)
- `TestIntegration_DashboardBelowReorderParts` (line ~1507)

Add the new test immediately after `TestIntegration_DashboardBelowReorderParts`
(after its closing brace, before `TestIntegration_TestDefinitionHistoryAudit`).

## 2. New test case

```go
func TestIntegration_ReportsDashboard_RendersAllCards(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.ReportsDashboard(rec, httptest.NewRequest(http.MethodGet, "/reports", nil))

	assertStatus(t, "ReportsDashboard", rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{
		"Open POs",
		"Received This Month",
		"Recent Activity",
		"Top Failure Modes",
		"Lowest Yield",
		"Stale WIP",
		"Pending Approval",
		"Below Reorder",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ReportsDashboard: body missing %q", want)
		}
	}
}
```

Notes:
- Use `assertStatus` (already defined in `integration_test.go`, line ~111) for
  the 200 check, matching the file's existing convention.
- The card-heading strings above are placeholders for "some literal text
  unique to each card section" — before finalizing, grep
  `arx_go/templates/reports/dashboard.html` for the actual heading/label text
  rendered for each of the 8 cards (Open PO Count, POs Received This Month,
  Recent Activity, Top Failure Modes, Lowest Yield Forms, Stale WIP Records,
  Pending Approval POs, Below Reorder Parts) and substitute the exact strings
  found there. This keeps the test at "renders without template error and
  each card's section is present," not per-row data assertions (those belong
  to the existing three per-card tests plus the untested five, which are out
  of scope for #816 per the issue body — only the handler wiring is being
  covered here).
- No new seed data or DB rows needed — this is read-only against existing
  ArxDev seed, consistent with the three existing dashboard tests (all marked
  "Read-only — no cleanup").
- No `defer` cleanup beyond `liveHandler`'s existing `cleanup()`.

## 3. Router/auth setup

None needed. Confirmed by reading the existing handler-level integration
tests in the same file (e.g. `TestIntegration_FormDef_RendersStepsAndArchivedToggle`,
line 3713): they call the `*Handler` method directly —
`h.FormDef(rec, withID(httptest.NewRequest(...), 6001))` — never going through
chi's router or the `RequireAuth` middleware chain. `RequireAuth` only runs
when requests are dispatched through the mounted router; invoking the method
directly bypasses it entirely, so no session cookie, CSRF token, or mock auth
state is required. The new `ReportsDashboard` test follows the identical
pattern: build a `httptest.NewRequest`, call `h.ReportsDashboard(rec, req)`
directly, inspect the `httptest.ResponseRecorder`.

No chi route params are needed either (`ReportsDashboard` takes no `{id}`-style
path param), so no `withID`-style wrapper is required — plain
`httptest.NewRequest(http.MethodGet, "/reports", nil)` suffices.
