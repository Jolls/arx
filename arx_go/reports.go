package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

	"arx/internal/reports"
)

// dashboardActivityItem is one row in the Reports dashboard's recent-activity
// feed — either a part modification or a PO history event (issue #282).
type dashboardActivityItem struct {
	Label     string
	URL       string
	Detail    string
	When      string
	Timestamp time.Time
}

// dashboardFailureModeItem is one row in the Reports dashboard's top-failing-
// steps summary — a single test step on a single form (issue #245).
type dashboardFailureModeItem = reports.FailureMode

// dashboardYieldItem is one row in the Reports dashboard's lowest-yield
// summary — a single form's all-time first-pass yield (issue #244).
type dashboardYieldItem = reports.FormYield

// dashboardStaleWIPItem is one row in the Reports dashboard's Stale WIP
// Records summary — a test record left unlocked (in progress) longer than
// staleWIPThresholdDays (issue #658, RPT-7).
type dashboardStaleWIPItem struct {
	RecordID   int
	FormID     int
	PartNumber string
	AgeDays    int
}

// dashboardPendingApprovalItem is one row in the Reports dashboard's POs
// Pending Approval summary (issue #658, RPT-7).
type dashboardPendingApprovalItem struct {
	Number  string
	AgeDays int
}

// dashboardBelowReorderItem is one row in the Reports dashboard's Below Reorder
// Point card — a part whose on-hand stock has fallen below its reorder minimum
// (issue #273, INV-2).
type dashboardBelowReorderItem = reports.BelowReorder

// staleWIPThresholdDays is the age (in days since creation) past which an
// unlocked test record is flagged as stale on the Reports dashboard.
const staleWIPThresholdDays = 14

// ageDays returns the number of whole days between t and now.
func ageDays(t time.Time) int {
	return int(time.Since(t).Hours() / 24)
}

// ReportsDashboard is the Reports tab landing page (issue #282, RPT-1).
func (h *Handler) ReportsDashboard(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "dashboard"}
	if h.database() == nil {
		h.render(w, r, "reports/dashboard.html", data)
		return
	}

	openPOs, err := h.dashboardOpenPOCount(r.Context())
	if err != nil {
		h.renderError(w, r, "Error loading dashboard: "+err.Error())
		return
	}
	receivedThisMonth, err := h.dashboardPOsReceivedThisMonth(r.Context())
	if err != nil {
		h.renderError(w, r, "Error loading dashboard: "+err.Error())
		return
	}
	activity, err := h.dashboardRecentActivity(r.Context(), 10)
	if err != nil {
		h.renderError(w, r, "Error loading dashboard: "+err.Error())
		return
	}
	topFailureModes, err := h.dashboardTopFailureModes(r.Context(), 5)
	if err != nil {
		h.renderError(w, r, "Error loading dashboard: "+err.Error())
		return
	}
	lowestYieldForms, err := h.dashboardLowestYieldForms(r.Context(), 5)
	if err != nil {
		h.renderError(w, r, "Error loading dashboard: "+err.Error())
		return
	}
	staleWIPRecords, err := h.dashboardStaleWIPRecords(r.Context(), 5)
	if err != nil {
		h.renderError(w, r, "Error loading dashboard: "+err.Error())
		return
	}
	pendingApprovalPOs, err := h.dashboardPendingApprovalPOs(r.Context(), 5)
	if err != nil {
		h.renderError(w, r, "Error loading dashboard: "+err.Error())
		return
	}
	belowReorderParts, err := h.dashboardBelowReorderParts(r.Context(), 5)
	if err != nil {
		h.renderError(w, r, "Error loading dashboard: "+err.Error())
		return
	}

	data["OpenPOCount"] = openPOs
	data["POsReceivedThisMonth"] = receivedThisMonth
	data["RecentActivity"] = activity
	data["TopFailureModes"] = topFailureModes
	data["LowestYieldForms"] = lowestYieldForms
	data["StaleWIPRecords"] = staleWIPRecords
	data["PendingApprovalPOs"] = pendingApprovalPOs
	data["BelowReorderParts"] = belowReorderParts
	h.render(w, r, "reports/dashboard.html", data)
}

// dashboardOpenPOCount counts POs in the 'open' lifecycle status (#271),
// matching the filter offered on the PO list's Status column.
func (h *Handler) dashboardOpenPOCount(ctx context.Context) (int, error) {
	return h.reports().OpenPOCount(ctx)
}

func (h *Handler) reports() *reports.Service { return reports.New(handlerDB{h}) }

// dashboardPOsReceivedThisMonth counts distinct POs with at least one line
// received since the first of the current calendar month.
func (h *Handler) dashboardPOsReceivedThisMonth(ctx context.Context) (int, error) {
	return h.reports().POsReceivedThisMonth(ctx)
}

// dashboardTopFailureModes lists the top failing test steps across all forms,
// all-time, ranked by failure count descending — a dashboard-level summary of
// the per-form Failure Modes report (arx_go/records_failure_modes.go, issue
// #245).
func (h *Handler) dashboardTopFailureModes(ctx context.Context, limit int) ([]dashboardFailureModeItem, error) {
	return h.reports().TopFailureModes(ctx, limit)
}

// dashboardLowestYieldForms lists the forms with the lowest all-time
// first-pass yield, worst first — a dashboard-level summary of the per-form
// Yield Summary report (arx_go/records_yield.go, issue #244). Forms with no
// records are excluded. Per-record pass/fail is aggregated in Go since a
// record's outcome depends on all of its result rows (any failure fails the
// record), matching computeYieldBuckets in records_yield.go.
func (h *Handler) dashboardLowestYieldForms(ctx context.Context, limit int) ([]dashboardYieldItem, error) {
	return h.reports().LowestYieldForms(ctx, limit)
}

// dashboardStaleWIPRecords lists unlocked test records older than
// staleWIPThresholdDays, oldest first, so forgotten/abandoned test runs
// surface on the Reports dashboard (issue #658, RPT-7).
func (h *Handler) dashboardStaleWIPRecords(ctx context.Context, limit int) ([]dashboardStaleWIPItem, error) {
	rows, err := h.reports().StaleWIPRecords(ctx, limit, staleWIPThresholdDays)
	if err != nil {
		return nil, err
	}
	var items []dashboardStaleWIPItem
	for _, r := range rows {
		items = append(items, dashboardStaleWIPItem{
			RecordID: r.RecordID, FormID: r.FormID, PartNumber: r.PartNumber, AgeDays: ageDays(r.CreatedAt),
		})
	}
	return items, nil
}

// dashboardPendingApprovalPOs lists POs awaiting approval, oldest first, as a
// bottleneck indicator on the Reports dashboard (issue #658, RPT-7). Age is
// measured from the most recent 'submitted' approval event on each PO.
func (h *Handler) dashboardPendingApprovalPOs(ctx context.Context, limit int) ([]dashboardPendingApprovalItem, error) {
	rows, err := h.reports().PendingApprovalPOs(ctx, limit)
	if err != nil {
		return nil, err
	}
	var items []dashboardPendingApprovalItem
	for _, r := range rows {
		item := dashboardPendingApprovalItem{Number: r.Number}
		if r.SubmittedAt != nil {
			item.AgeDays = ageDays(*r.SubmittedAt)
		}
		items = append(items, item)
	}
	return items, nil
}

// dashboardBelowReorderParts lists parts whose on-hand stock has fallen below
// their reorder minimum, most-depleted first (issue #273, INV-2). Parts with no
// reorder point set (reorder_min IS NULL) are excluded.
func (h *Handler) dashboardBelowReorderParts(ctx context.Context, limit int) ([]dashboardBelowReorderItem, error) {
	return h.reports().BelowReorderParts(ctx, limit)
}

// dashboardRecentActivity merges the most recently modified parts with the
// most recent PO history events into one newest-first feed, capped at limit.
func (h *Handler) dashboardRecentActivity(ctx context.Context, limit int) ([]dashboardActivityItem, error) {
	var items []dashboardActivityItem

	partRows, err := h.reports().RecentModifiedParts(ctx, limit)
	if err != nil {
		return nil, err
	}
	loc := h.userLocationCtx(ctx)
	for _, p := range partRows {
		modified := p.Modified
		label := p.PartNumber
		if p.Description != "" {
			label += " — " + p.Description
		}
		items = append(items, dashboardActivityItem{
			Label: label, URL: fmt.Sprintf("/part/%d", p.ID),
			// modified_date is a DATE: place it at midnight in the user's zone so it
			// sorts correctly against PO changed_at instants (#192).
			Detail: "modified", When: formatDate(&modified),
			Timestamp: time.Date(modified.Year(), modified.Month(), modified.Day(), 0, 0, 0, 0, loc),
		})
	}

	poRows, err := h.reports().RecentPOEvents(ctx, limit)
	if err != nil {
		return nil, err
	}
	for _, e := range poRows {
		detail := e.EventType
		switch {
		case e.EventType == "status" && e.ToStatus != "":
			detail = "→ " + e.ToStatus
		case e.EventType == "approval" && e.Action != "":
			detail = e.Action
		}
		local := e.ChangedAt.In(loc)
		items = append(items, dashboardActivityItem{
			Label: "PO " + e.Number, URL: fmt.Sprintf("/po/%d", e.POID),
			Detail: detail, When: formatDate(&local), Timestamp: e.ChangedAt,
		})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Timestamp.After(items[j].Timestamp) })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// ── Spend Analysis (issue #283, RPT-2) ──────────────────────────────────────

const spendDateLayout = "2006-01-02"

// reportDateRange is the resolved date-range selection shared by all
// date-range-filterable reports (Spend Analysis #283, On-Time Delivery and
// PO Cycle Time #659/RPT-8). From/To follow the same inclusive-day convention
// as recordFilters (records_filters.go): zero value means no bound, and an
// inclusive To is applied as an exclusive bound on the following day.
type reportDateRange struct {
	Preset  string // "this_month" | "this_quarter" | "ytd" | "custom"
	From    time.Time
	To      time.Time
	FromStr string // YYYY-MM-DD, for repopulating the custom date inputs
	ToStr   string
}

// resolveSpendDateRange resolves the query string's range selection into
// concrete date bounds relative to now. Missing/unrecognized presets default
// to "this_month". Unparseable custom dates are dropped (no bound), not
// silently replaced with the default preset's bounds.
func resolveSpendDateRange(q url.Values, now time.Time) reportDateRange {
	preset := q.Get("range")

	var rng reportDateRange
	switch preset {
	case "this_quarter":
		rng.Preset = "this_quarter"
		qStartMonth := ((int(now.Month())-1)/3)*3 + 1
		rng.From = time.Date(now.Year(), time.Month(qStartMonth), 1, 0, 0, 0, 0, now.Location())
		rng.To = rng.From.AddDate(0, 3, -1)
	case "ytd":
		rng.Preset = "ytd"
		rng.From = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
		rng.To = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "custom":
		rng.Preset = "custom"
		rng.FromStr = q.Get("from")
		if t, err := time.ParseInLocation(spendDateLayout, rng.FromStr, now.Location()); err == nil {
			rng.From = t
		} else {
			rng.FromStr = ""
		}
		rng.ToStr = q.Get("to")
		if t, err := time.ParseInLocation(spendDateLayout, rng.ToStr, now.Location()); err == nil {
			rng.To = t
		} else {
			rng.ToStr = ""
		}
		return rng
	default:
		rng.Preset = "this_month"
		rng.From = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		rng.To = rng.From.AddDate(0, 1, -1)
	}

	rng.FromStr = rng.From.Format(spendDateLayout)
	rng.ToStr = rng.To.Format(spendDateLayout)
	return rng
}

// serviceRange is the reports service's view of the date-range filter.
func (rng reportDateRange) serviceRange() reports.DateRange {
	return reports.DateRange{From: rng.From, To: rng.To}
}

type spendSupplierRow = reports.SupplierSpend

type spendPartRow = reports.PartSpend

// querySpendBySupplier totals PO line spend (qty * unit_cost) by supplier
// name over the given date range, sorted by total spend descending.
func (h *Handler) querySpendBySupplier(ctx context.Context, rng reportDateRange) ([]spendSupplierRow, error) {
	return h.reports().SpendBySupplier(ctx, rng.serviceRange())
}

// querySpendByPart totals PO line spend (qty * unit_cost) by part over the
// given date range, sorted by total spend descending. Lines with no linked
// part_id (freeform PO lines) are grouped by their part_number_snapshot
// text so their spend stays accounted for.
func (h *Handler) querySpendByPart(ctx context.Context, rng reportDateRange) ([]spendPartRow, error) {
	return h.reports().SpendByPart(ctx, rng.serviceRange())
}

// ReportsSpend is the Spend Analysis report page — GET /reports/spend.
func (h *Handler) ReportsSpend(w http.ResponseWriter, r *http.Request) {
	rng := resolveSpendDateRange(r.URL.Query(), time.Now())
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "spend", "Range": rng, "DateRangeAction": "/reports/spend"}
	if h.database() == nil {
		h.render(w, r, "reports/spend.html", data)
		return
	}

	bySupplier, err := h.querySpendBySupplier(r.Context(), rng)
	if err != nil {
		h.renderError(w, r, "Error loading spend by supplier: "+err.Error())
		return
	}
	byPart, err := h.querySpendByPart(r.Context(), rng)
	if err != nil {
		h.renderError(w, r, "Error loading spend by part: "+err.Error())
		return
	}
	data["BySupplier"] = bySupplier
	data["ByPart"] = byPart
	h.render(w, r, "reports/spend.html", data)
}

// ── Supplier On-Time Delivery (issue #659, RPT-8) ───────────────────────────

// onTimeSupplierRow is one supplier's on-time delivery aggregate. Only
// po_line rows with both a quoted lead_time_days and a date_received are
// evaluated; lines with no quote or not yet received are excluded, not
// counted late.
type onTimeSupplierRow = reports.OnTimeSupplier

// queryOnTimeDelivery ranks suppliers by on-time delivery performance over
// the given date range (filtered on purchase_order.date_ordered), worst
// on-time % first since this is a watchlist/exception report.
func (h *Handler) queryOnTimeDelivery(ctx context.Context, rng reportDateRange) ([]onTimeSupplierRow, error) {
	return h.reports().OnTimeDelivery(ctx, rng.serviceRange())
}

// ReportsOnTime is the Supplier On-Time Delivery report page — GET /reports/on-time.
func (h *Handler) ReportsOnTime(w http.ResponseWriter, r *http.Request) {
	rng := resolveSpendDateRange(r.URL.Query(), time.Now())
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "on-time", "Range": rng, "DateRangeAction": "/reports/on-time"}
	if h.database() == nil {
		h.render(w, r, "reports/on_time.html", data)
		return
	}

	rows, err := h.queryOnTimeDelivery(r.Context(), rng)
	if err != nil {
		h.renderError(w, r, "Error loading on-time delivery: "+err.Error())
		return
	}
	data["Rows"] = rows
	h.render(w, r, "reports/on_time.html", data)
}

// ReportsOnTimeExportCSV streams the Supplier On-Time Delivery table as CSV
// — GET /reports/on-time/export.csv.
func (h *Handler) ReportsOnTimeExportCSV(w http.ResponseWriter, r *http.Request) {
	rng := resolveSpendDateRange(r.URL.Query(), time.Now())
	rows, err := h.queryOnTimeDelivery(r.Context(), rng)
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	var csvRows [][]string
	for _, row := range rows {
		csvRows = append(csvRows, []string{
			row.SupplierName,
			fmt.Sprintf("%d", row.TotalLines),
			fmt.Sprintf("%d", row.OnTimeLines),
			fmt.Sprintf("%.1f", row.OnTimePct),
			fmt.Sprintf("%.1f", row.AvgDaysLate),
		})
	}
	writeSpendCSV(w, "on_time_delivery.csv", []string{"Supplier", "Total Lines", "On-Time Lines", "On-Time %", "Avg Days Late"}, csvRows)
}

// ── PO Cycle Time (issue #659, RPT-8) ───────────────────────────────────────

// cycleTimeStageRow is one lifecycle stage's average dwell time. Only
// transitions with a recorded exit (a later purchase_order_history row for
// the same po_id) are averaged — POs still sitting in a stage are excluded
// (open-ended, no end date to measure).
type cycleTimeStageRow = reports.CycleStage

// queryPOCycleTime computes the average time POs spend in each lifecycle
// stage, filtered on the stage-entry date (entered_at) over the given date
// range. The date filter is applied in the outer query, after LEAD() has
// paired each stage's entered_at with its exited_at over each PO's full,
// unfiltered history — filtering inside the CTE would prune rows out of a
// PO's history before pairing, silently mis-pairing stages near the window
// boundary (issue #659 review). Results are ordered by natural PO lifecycle
// progression (not alphabetical or by avg_days) via the CASE expression
// below; GROUP BY already omits any stage absent from the data.
func (h *Handler) queryPOCycleTime(ctx context.Context, rng reportDateRange) ([]cycleTimeStageRow, error) {
	return h.reports().POCycleTime(ctx, rng.serviceRange())
}

// ReportsCycleTime is the PO Cycle Time report page — GET /reports/cycle-time.
func (h *Handler) ReportsCycleTime(w http.ResponseWriter, r *http.Request) {
	rng := resolveSpendDateRange(r.URL.Query(), time.Now().In(h.userLocation(r)))
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "cycle-time", "Range": rng, "DateRangeAction": "/reports/cycle-time"}
	if h.database() == nil {
		h.render(w, r, "reports/cycle_time.html", data)
		return
	}

	rows, err := h.queryPOCycleTime(r.Context(), rng)
	if err != nil {
		h.renderError(w, r, "Error loading PO cycle time: "+err.Error())
		return
	}
	data["Rows"] = rows
	h.render(w, r, "reports/cycle_time.html", data)
}

// ReportsCycleTimeExportCSV streams the PO Cycle Time table as CSV — GET
// /reports/cycle-time/export.csv.
func (h *Handler) ReportsCycleTimeExportCSV(w http.ResponseWriter, r *http.Request) {
	rng := resolveSpendDateRange(r.URL.Query(), time.Now().In(h.userLocation(r)))
	rows, err := h.queryPOCycleTime(r.Context(), rng)
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	var csvRows [][]string
	for _, row := range rows {
		csvRows = append(csvRows, []string{row.Stage, fmt.Sprintf("%d", row.POCount), fmt.Sprintf("%.1f", row.AvgDays)})
	}
	writeSpendCSV(w, "po_cycle_time.csv", []string{"Stage", "PO Count", "Avg Days"}, csvRows)
}

// ── Attachment / Data Quality Gaps (issue #659, RPT-8) ──────────────────────

// dataQualityPartRow is one part flagged by the Attachment / Data Quality
// Gaps report — shared shape for all three gap checks (Category is
// redundant/unused for the missing-default-supplier check, which is already
// scoped to BUY only).
type dataQualityPartRow = reports.DataQualityPart

// queryPartsNoAttachments lists active BUY/ASM/DWG parts with no attachments
// (part.attachment_count is a trigger-maintained denormalized column, so it's
// selected directly rather than re-COUNTing part_attachment).
func (h *Handler) queryPartsNoAttachments(ctx context.Context) ([]dataQualityPartRow, error) {
	return h.reports().PartsNoAttachments(ctx)
}

// queryPartsMissingDefaultSupplier lists active BUY parts with no default
// supplier set. Scoped to BUY only — ASM/DWG parts are typically manufactured
// in-house and don't need a direct purchasing default supplier.
func (h *Handler) queryPartsMissingDefaultSupplier(ctx context.Context) ([]dataQualityPartRow, error) {
	return h.reports().PartsMissingDefaultSupplier(ctx)
}

// queryPartsStaleRollup lists active parts with no cost rollup ever computed.
// NULL-only, no age threshold — the codebase has no existing staleness-by-age
// convention to anchor an arbitrary number on, and "never rolled up" is the
// unambiguous reading of "missing." Not scoped to BUY/ASM/DWG since any
// active part can carry a rollup cost.
func (h *Handler) queryPartsStaleRollup(ctx context.Context) ([]dataQualityPartRow, error) {
	return h.reports().PartsStaleRollup(ctx)
}

// ReportsDataQuality is the Attachment / Data Quality Gaps report page — GET
// /reports/data-quality. Point-in-time snapshot; no date range.
func (h *Handler) ReportsDataQuality(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "data-quality"}
	if h.database() == nil {
		h.render(w, r, "reports/data_quality.html", data)
		return
	}

	ctx := r.Context()
	noAttach, err := h.queryPartsNoAttachments(ctx)
	if err != nil {
		h.renderError(w, r, "Error loading parts missing attachments: "+err.Error())
		return
	}
	noSupplier, err := h.queryPartsMissingDefaultSupplier(ctx)
	if err != nil {
		h.renderError(w, r, "Error loading parts missing default supplier: "+err.Error())
		return
	}
	staleRollup, err := h.queryPartsStaleRollup(ctx)
	if err != nil {
		h.renderError(w, r, "Error loading parts with no/stale cost rollup: "+err.Error())
		return
	}
	data["NoAttachments"] = noAttach
	data["MissingDefaultSupplier"] = noSupplier
	data["StaleRollup"] = staleRollup
	h.render(w, r, "reports/data_quality.html", data)
}

func dataQualityCSVRows(rows []dataQualityPartRow) [][]string {
	var csvRows [][]string
	for _, row := range rows {
		csvRows = append(csvRows, []string{row.PartNumber, row.Description, row.Category})
	}
	return csvRows
}

// ReportsDataQualityNoAttachmentsExportCSV streams the missing-attachments
// table as CSV — GET /reports/data-quality/export-no-attachments.csv.
func (h *Handler) ReportsDataQualityNoAttachmentsExportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queryPartsNoAttachments(r.Context())
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	writeSpendCSV(w, "data_quality_no_attachments.csv", []string{"Part Number", "Description", "Category"}, dataQualityCSVRows(rows))
}

// ReportsDataQualityMissingSupplierExportCSV streams the missing-default-
// supplier table as CSV — GET /reports/data-quality/export-missing-supplier.csv.
func (h *Handler) ReportsDataQualityMissingSupplierExportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queryPartsMissingDefaultSupplier(r.Context())
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	writeSpendCSV(w, "data_quality_missing_supplier.csv", []string{"Part Number", "Description", "Category"}, dataQualityCSVRows(rows))
}

// ReportsDataQualityStaleRollupExportCSV streams the no/stale-rollup table as
// CSV — GET /reports/data-quality/export-stale-rollup.csv.
func (h *Handler) ReportsDataQualityStaleRollupExportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queryPartsStaleRollup(r.Context())
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	writeSpendCSV(w, "data_quality_stale_rollup.csv", []string{"Part Number", "Description", "Category"}, dataQualityCSVRows(rows))
}

// formOption is one form listed on a per-form report picker (e.g. Reports >
// Yield Summary, issue #244; Reports > Failure Modes, issue #245).
type formOption = reports.FormOption

// loadActiveFormOptions lists active forms for a per-form report picker,
// shared by ReportsYieldPicker and ReportsFailureModesPicker.
func (h *Handler) loadActiveFormOptions(ctx context.Context) ([]formOption, error) {
	return h.reports().ActiveFormOptions(ctx)
}

// ReportsYieldPicker is the Reports tab's entry point into the per-form yield
// summary (arx_go/records_yield.go RecordsYieldSummary) — lists forms to pick
// from, since the yield page itself is scoped to one form (issue #244).
func (h *Handler) ReportsYieldPicker(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "yield"}
	if h.database() == nil {
		h.render(w, r, "reports/yield_picker.html", data)
		return
	}

	forms, err := h.loadActiveFormOptions(r.Context())
	if err != nil {
		h.renderError(w, r, "Error loading forms: "+err.Error())
		return
	}

	data["Forms"] = forms
	h.render(w, r, "reports/yield_picker.html", data)
}

// ReportsFailureModesPicker is the Reports tab's entry point into the
// per-form failure mode report (arx_go/records_failure_modes.go
// RecordsFailureModes) — lists forms to pick from, since the report itself is
// scoped to one form (issue #245).
func (h *Handler) ReportsFailureModesPicker(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "failure-modes"}
	if h.database() == nil {
		h.render(w, r, "reports/failure_modes_picker.html", data)
		return
	}

	forms, err := h.loadActiveFormOptions(r.Context())
	if err != nil {
		h.renderError(w, r, "Error loading forms: "+err.Error())
		return
	}

	data["Forms"] = forms
	h.render(w, r, "reports/failure_modes_picker.html", data)
}

// writeSpendCSV streams a spend-report CSV: header row followed by rows,
// shared by the supplier and part export handlers below.
func writeSpendCSV(w http.ResponseWriter, filename string, header []string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	cw := csv.NewWriter(w)
	_ = cw.Write(header)
	_ = cw.WriteAll(rows)
}

// ReportsSpendBySupplierExportCSV streams the spend-by-supplier table as CSV
// — GET /reports/spend/export-suppliers.csv.
func (h *Handler) ReportsSpendBySupplierExportCSV(w http.ResponseWriter, r *http.Request) {
	rng := resolveSpendDateRange(r.URL.Query(), time.Now())
	rows, err := h.querySpendBySupplier(r.Context(), rng)
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	csvRows := make([][]string, len(rows))
	for i, row := range rows {
		csvRows[i] = []string{row.SupplierName, fmt.Sprintf("%.2f", row.TotalSpend)}
	}
	writeSpendCSV(w, "spend-by-supplier.csv", []string{"Supplier", "Total Spend"}, csvRows)
}

// ReportsSpendByPartExportCSV streams the spend-by-part table as CSV — GET
// /reports/spend/export-parts.csv.
func (h *Handler) ReportsSpendByPartExportCSV(w http.ResponseWriter, r *http.Request) {
	rng := resolveSpendDateRange(r.URL.Query(), time.Now())
	rows, err := h.querySpendByPart(r.Context(), rng)
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	csvRows := make([][]string, len(rows))
	for i, row := range rows {
		csvRows[i] = []string{row.PartNumber, row.Description, fmt.Sprintf("%.2f", row.TotalSpend)}
	}
	writeSpendCSV(w, "spend-by-part.csv", []string{"Part Number", "Description", "Total Spend"}, csvRows)
}
