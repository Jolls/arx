package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
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
type dashboardFailureModeItem struct {
	FormID       int
	PartNumber   string
	Parameter    string
	FailureCount int
}

// dashboardYieldItem is one row in the Reports dashboard's lowest-yield
// summary — a single form's all-time first-pass yield (issue #244).
type dashboardYieldItem struct {
	FormID     int
	PartNumber string
	Total      int
	Passed     int
}

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
type dashboardBelowReorderItem struct {
	PartID      int
	PartNumber  string
	StockOnHand float64
	ReorderMin  float64
}

// staleWIPThresholdDays is the age (in days since creation) past which an
// unlocked test record is flagged as stale on the Reports dashboard.
const staleWIPThresholdDays = 14

// ageDays returns the number of whole days between t and now.
func ageDays(t time.Time) int {
	return int(time.Since(t).Hours() / 24)
}

// FPYPct returns the first-pass yield percentage, or 0 if there are no records.
func (item dashboardYieldItem) FPYPct() float64 {
	if item.Total == 0 {
		return 0
	}
	return float64(item.Passed) / float64(item.Total) * 100
}

// ReportsDashboard is the Reports tab landing page (issue #282, RPT-1).
func (h *Handler) ReportsDashboard(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "dashboard"}
	if h.db == nil {
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
	var n int
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE status = 'open'`, h.cfg.POTable()),
	).Scan(&n)
	return n, err
}

// dashboardPOsReceivedThisMonth counts distinct POs with at least one line
// received since the first of the current calendar month.
func (h *Handler) dashboardPOsReceivedThisMonth(ctx context.Context) (int, error) {
	var n int
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(DISTINCT po_id) FROM %s WHERE date_received >= %s`,
		h.cfg.POLineTable(), h.dialect.MonthStartExpr()),
	).Scan(&n)
	return n, err
}

// dashboardTopFailureModes lists the top failing test steps across all forms,
// all-time, ranked by failure count descending — a dashboard-level summary of
// the per-form Failure Modes report (arx_go/records_failure_modes.go, issue
// #245).
func (h *Handler) dashboardTopFailureModes(ctx context.Context, limit int) ([]dashboardFailureModeItem, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT %sf.id, pn.part_number, MAX(res.parameter) AS parameter,
			SUM(CASE WHEN res.pass_fail = %s THEN 1 ELSE 0 END) AS failure_count
		FROM %s res
		JOIN %s trec ON res.form_record_id = trec.id
		JOIN %s f ON trec.form_id = f.id
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE trec.is_active = %s AND res.pass_fail IS NOT NULL
		GROUP BY f.id, pn.part_number, res.form_row_id
		HAVING SUM(CASE WHEN res.pass_fail = %s THEN 1 ELSE 0 END) > 0
		ORDER BY failure_count DESC`+h.dialect.LimitClause("@p1"),
		h.dialect.TopClause("@p1"), h.dialect.BoolLiteral(false), h.cfg.ResultsTable(), h.cfg.RecordsTable(), h.cfg.FormsTable(), h.cfg.PartsTable(), h.dialect.BoolLiteral(true), h.dialect.BoolLiteral(false)), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []dashboardFailureModeItem
	for rows.Next() {
		var item dashboardFailureModeItem
		if err := rows.Scan(&item.FormID, &item.PartNumber, &item.Parameter, &item.FailureCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// dashboardLowestYieldForms lists the forms with the lowest all-time
// first-pass yield, worst first — a dashboard-level summary of the per-form
// Yield Summary report (arx_go/records_yield.go, issue #244). Forms with no
// records are excluded. Per-record pass/fail is aggregated in Go since a
// record's outcome depends on all of its result rows (any failure fails the
// record), matching computeYieldBuckets in records_yield.go.
func (h *Handler) dashboardLowestYieldForms(ctx context.Context, limit int) ([]dashboardYieldItem, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT trec.form_id, pn.part_number, MAX(CASE WHEN res.pass_fail = %s THEN 1 ELSE 0 END)
		FROM %s trec
		JOIN %s f ON trec.form_id = f.id
		JOIN %s pn ON f.part_number_id = pn.id
		LEFT JOIN %s res ON res.form_record_id = trec.id
		WHERE trec.is_active = %s
		GROUP BY trec.id, trec.form_id, pn.part_number`,
		h.dialect.BoolLiteral(false), h.cfg.RecordsTable(), h.cfg.FormsTable(), h.cfg.PartsTable(), h.cfg.ResultsTable(), h.dialect.BoolLiteral(true)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byForm := make(map[int]*dashboardYieldItem)
	var order []int
	for rows.Next() {
		var formID, anyFail int
		var partNumber string
		if err := rows.Scan(&formID, &partNumber, &anyFail); err != nil {
			return nil, err
		}
		item, ok := byForm[formID]
		if !ok {
			item = &dashboardYieldItem{FormID: formID, PartNumber: partNumber}
			byForm[formID] = item
			order = append(order, formID)
		}
		item.Total++
		if anyFail == 0 {
			item.Passed++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	items := make([]dashboardYieldItem, 0, len(order))
	for _, formID := range order {
		items = append(items, *byForm[formID])
	}
	sort.Slice(items, func(i, j int) bool { return items[i].FPYPct() < items[j].FPYPct() })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// dashboardStaleWIPRecords lists unlocked test records older than
// staleWIPThresholdDays, oldest first, so forgotten/abandoned test runs
// surface on the Reports dashboard (issue #658, RPT-7).
func (h *Handler) dashboardStaleWIPRecords(ctx context.Context, limit int) ([]dashboardStaleWIPItem, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT %strec.id, trec.form_id, pn.part_number, trec.created_at
		FROM %s trec
		JOIN %s f ON trec.form_id = f.id
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE trec.is_active = %s AND trec.is_locked = %s
			AND trec.created_at <= DATEADD(day, -@p2, GETDATE())
		ORDER BY trec.created_at ASC`+h.dialect.LimitClause("@p1"),
		h.dialect.TopClause("@p1"), h.cfg.RecordsTable(), h.cfg.FormsTable(), h.cfg.PartsTable(), h.dialect.BoolLiteral(true), h.dialect.BoolLiteral(false)), limit, staleWIPThresholdDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []dashboardStaleWIPItem
	for rows.Next() {
		var item dashboardStaleWIPItem
		var createdAt time.Time
		if err := rows.Scan(&item.RecordID, &item.FormID, &item.PartNumber, &createdAt); err != nil {
			return nil, err
		}
		item.AgeDays = ageDays(createdAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

// dashboardPendingApprovalPOs lists POs awaiting approval, oldest first, as a
// bottleneck indicator on the Reports dashboard (issue #658, RPT-7). Age is
// measured from the most recent 'submitted' approval event on each PO.
func (h *Handler) dashboardPendingApprovalPOs(ctx context.Context, limit int) ([]dashboardPendingApprovalItem, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT %spo.number,
			(SELECT MAX(h.changed_at) FROM %s h
			 WHERE h.po_id = po.id AND h.event_type = 'approval' AND h.action = 'submitted') AS submitted_at
		FROM %s po
		WHERE po.approval_status = 'pending'
		ORDER BY submitted_at ASC`+h.dialect.LimitClause("@p1"),
		h.dialect.TopClause("@p1"), h.cfg.POHistoryTable(), h.cfg.POTable()), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []dashboardPendingApprovalItem
	for rows.Next() {
		var item dashboardPendingApprovalItem
		var submittedAt sql.NullTime
		if err := rows.Scan(&item.Number, &submittedAt); err != nil {
			return nil, err
		}
		if submittedAt.Valid {
			item.AgeDays = ageDays(submittedAt.Time)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// dashboardBelowReorderParts lists parts whose on-hand stock has fallen below
// their reorder minimum, most-depleted first (issue #273, INV-2). Parts with no
// reorder point set (reorder_min IS NULL) are excluded.
func (h *Handler) dashboardBelowReorderParts(ctx context.Context, limit int) ([]dashboardBelowReorderItem, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT %sid, part_number, stock_on_hand, reorder_min
		FROM %s
		WHERE reorder_min IS NOT NULL AND stock_on_hand < reorder_min
		ORDER BY (stock_on_hand - reorder_min) ASC`+h.dialect.LimitClause("@p1"),
		h.dialect.TopClause("@p1"), h.cfg.PartsTable()), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []dashboardBelowReorderItem
	for rows.Next() {
		var item dashboardBelowReorderItem
		if err := rows.Scan(&item.PartID, &item.PartNumber, &item.StockOnHand, &item.ReorderMin); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// dashboardRecentActivity merges the most recently modified parts with the
// most recent PO history events into one newest-first feed, capped at limit.
func (h *Handler) dashboardRecentActivity(ctx context.Context, limit int) ([]dashboardActivityItem, error) {
	var items []dashboardActivityItem

	partRows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT %sid, part_number, title, modified_date
		 FROM %s WHERE modified_date IS NOT NULL ORDER BY modified_date DESC`+h.dialect.LimitClause("@p1"),
		h.dialect.TopClause("@p1"), h.cfg.PartsTable()), limit)
	if err != nil {
		return nil, err
	}
	for partRows.Next() {
		var id int
		var partNumber, title sql.NullString
		var modified time.Time // query filters modified_date IS NOT NULL
		if err := partRows.Scan(&id, &partNumber, &title, &modified); err != nil {
			partRows.Close()
			return nil, err
		}
		label := partNumber.String
		if title.String != "" {
			label += " — " + title.String
		}
		items = append(items, dashboardActivityItem{
			Label: label, URL: fmt.Sprintf("/part/%d", id),
			Detail: "modified", When: formatDate(&modified), Timestamp: modified,
		})
	}
	partRows.Close()

	poRows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT %sh.po_id, po.number, h.event_type, h.to_status, h.action, h.changed_at
		 FROM %s h JOIN %s po ON h.po_id = po.id ORDER BY h.changed_at DESC`+h.dialect.LimitClause("@p1"),
		h.dialect.TopClause("@p1"), h.cfg.POHistoryTable(), h.cfg.POTable()), limit)
	if err != nil {
		return nil, err
	}
	for poRows.Next() {
		var poID int
		var number, eventType, toStatus, action sql.NullString
		var changedAt time.Time
		if err := poRows.Scan(&poID, &number, &eventType, &toStatus, &action, &changedAt); err != nil {
			poRows.Close()
			return nil, err
		}
		detail := eventType.String
		switch {
		case eventType.String == "status" && toStatus.String != "":
			detail = "→ " + toStatus.String
		case eventType.String == "approval" && action.String != "":
			detail = action.String
		}
		items = append(items, dashboardActivityItem{
			Label: "PO " + number.String, URL: fmt.Sprintf("/po/%d", poID),
			Detail: detail, When: formatDate(&changedAt), Timestamp: changedAt,
		})
	}
	poRows.Close()

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
// inclusive To is applied in SQL via DATEADD(day, 1, ...).
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
		if t, err := time.Parse(spendDateLayout, rng.FromStr); err == nil {
			rng.From = t
		} else {
			rng.FromStr = ""
		}
		rng.ToStr = q.Get("to")
		if t, err := time.Parse(spendDateLayout, rng.ToStr); err == nil {
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

// whereClause builds the date-range WHERE fragment (starting with " AND") and
// its args for filtering the given column expression (e.g. "po.date_ordered",
// "poh.changed_at"), following the same conditional-bound pattern as
// recordFilters.whereClauses.
func (rng reportDateRange) whereClause(column string, startArg int) (string, []any) {
	var sb strings.Builder
	var args []any
	n := startArg

	if !rng.From.IsZero() {
		fmt.Fprintf(&sb, " AND %s >= @p%d", column, n)
		args = append(args, rng.From)
		n++
	}
	if !rng.To.IsZero() {
		fmt.Fprintf(&sb, " AND %s < DATEADD(day, 1, @p%d)", column, n)
		args = append(args, rng.To)
		n++
	}
	return sb.String(), args
}

type spendSupplierRow struct {
	SupplierName string
	TotalSpend   float64
}

type spendPartRow struct {
	PartNumber string
	Title      string
	TotalSpend float64
}

// querySpendBySupplier totals PO line spend (qty * unit_cost) by supplier
// name over the given date range, sorted by total spend descending.
func (h *Handler) querySpendBySupplier(ctx context.Context, rng reportDateRange) ([]spendSupplierRow, error) {
	where, args := rng.whereClause("po.date_ordered", 1)
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT po.supplier_name, COALESCE(SUM(pol.qty * pol.unit_cost), 0) AS total_spend
		FROM %s pol
		JOIN %s po ON pol.po_id = po.id
		WHERE 1=1%s
		GROUP BY po.supplier_name
		ORDER BY total_spend DESC
	`, h.cfg.POLineTable(), h.cfg.POTable(), where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []spendSupplierRow
	for rows.Next() {
		var supplierName sql.NullString
		var total float64
		if err := rows.Scan(&supplierName, &total); err != nil {
			return nil, err
		}
		result = append(result, spendSupplierRow{SupplierName: supplierName.String, TotalSpend: total})
	}
	return result, rows.Err()
}

// querySpendByPart totals PO line spend (qty * unit_cost) by part over the
// given date range, sorted by total spend descending. Lines with no linked
// part_id (freeform PO lines) are grouped by their part_number_snapshot
// text so their spend stays accounted for.
func (h *Handler) querySpendByPart(ctx context.Context, rng reportDateRange) ([]spendPartRow, error) {
	where, args := rng.whereClause("po.date_ordered", 1)
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT pol.part_number_snapshot, p.title, COALESCE(SUM(pol.qty * pol.unit_cost), 0) AS total_spend
		FROM %s pol
		JOIN %s po ON pol.po_id = po.id
		LEFT JOIN %s p ON pol.part_id = p.id
		WHERE 1=1%s
		GROUP BY pol.part_number_snapshot, p.title
		ORDER BY total_spend DESC
	`, h.cfg.POLineTable(), h.cfg.POTable(), h.cfg.PartsTable(), where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []spendPartRow
	for rows.Next() {
		var partNumber, title sql.NullString
		var total float64
		if err := rows.Scan(&partNumber, &title, &total); err != nil {
			return nil, err
		}
		result = append(result, spendPartRow{PartNumber: partNumber.String, Title: title.String, TotalSpend: total})
	}
	return result, rows.Err()
}

// ReportsSpend is the Spend Analysis report page — GET /reports/spend.
func (h *Handler) ReportsSpend(w http.ResponseWriter, r *http.Request) {
	rng := resolveSpendDateRange(r.URL.Query(), time.Now())
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "spend", "Range": rng, "DateRangeAction": "/reports/spend"}
	if h.db == nil {
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
type onTimeSupplierRow struct {
	SupplierName string
	TotalLines   int
	OnTimeLines  int
	OnTimePct    float64 // OnTimeLines/TotalLines*100; TotalLines is always > 0 for a returned row
	AvgDaysLate  float64 // signed: positive = late, negative = early
}

// queryOnTimeDelivery ranks suppliers by on-time delivery performance over
// the given date range (filtered on purchase_order.date_ordered), worst
// on-time % first since this is a watchlist/exception report.
func (h *Handler) queryOnTimeDelivery(ctx context.Context, rng reportDateRange) ([]onTimeSupplierRow, error) {
	where, args := rng.whereClause("po.date_ordered", 1)
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT
			po.supplier_name,
			COUNT(*) AS total_lines,
			SUM(CASE WHEN pol.date_received <= DATEADD(day, pol.lead_time_days, po.date_ordered) THEN 1 ELSE 0 END) AS on_time_lines,
			AVG(CAST(DATEDIFF(day, DATEADD(day, pol.lead_time_days, po.date_ordered), pol.date_received) AS FLOAT)) AS avg_days_late
		FROM %s pol
		JOIN %s po ON pol.po_id = po.id
		WHERE pol.lead_time_days IS NOT NULL
		  AND pol.date_received IS NOT NULL
		  AND po.date_ordered IS NOT NULL%s
		GROUP BY po.supplier_name
	`, h.cfg.POLineTable(), h.cfg.POTable(), where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []onTimeSupplierRow
	for rows.Next() {
		var supplierName sql.NullString
		var row onTimeSupplierRow
		var avgDaysLate sql.NullFloat64
		if err := rows.Scan(&supplierName, &row.TotalLines, &row.OnTimeLines, &avgDaysLate); err != nil {
			return nil, err
		}
		row.SupplierName = supplierName.String
		row.AvgDaysLate = avgDaysLate.Float64
		// TotalLines is a COUNT(*) under GROUP BY, so it's always >= 1 here.
		row.OnTimePct = float64(row.OnTimeLines) / float64(row.TotalLines) * 100
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].OnTimePct != result[j].OnTimePct {
			return result[i].OnTimePct < result[j].OnTimePct
		}
		return result[i].SupplierName < result[j].SupplierName
	})
	return result, nil
}

// ReportsOnTime is the Supplier On-Time Delivery report page — GET /reports/on-time.
func (h *Handler) ReportsOnTime(w http.ResponseWriter, r *http.Request) {
	rng := resolveSpendDateRange(r.URL.Query(), time.Now())
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "on-time", "Range": rng, "DateRangeAction": "/reports/on-time"}
	if h.db == nil {
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
type cycleTimeStageRow struct {
	Stage   string
	POCount int
	AvgDays float64
}

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
	where, args := rng.whereClause("entered_at", 1)
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		WITH stage_durations AS (
			SELECT
				poh.po_id,
				poh.to_status AS stage,
				poh.changed_at AS entered_at,
				LEAD(poh.changed_at) OVER (PARTITION BY poh.po_id ORDER BY poh.changed_at, poh.id) AS exited_at
			FROM %s poh
			WHERE poh.event_type = 'status'
			  AND poh.to_status IN ('draft','open','sent','partially_received','closed')
		)
		SELECT
			stage,
			COUNT(*) AS po_count,
			AVG(CAST(DATEDIFF(hour, entered_at, exited_at) AS FLOAT) / 24.0) AS avg_days
		FROM stage_durations
		WHERE exited_at IS NOT NULL%s
		GROUP BY stage
		ORDER BY CASE stage
			WHEN 'draft' THEN 1
			WHEN 'open' THEN 2
			WHEN 'sent' THEN 3
			WHEN 'partially_received' THEN 4
			WHEN 'closed' THEN 5
		END
	`, h.cfg.POHistoryTable(), where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []cycleTimeStageRow
	for rows.Next() {
		var row cycleTimeStageRow
		if err := rows.Scan(&row.Stage, &row.POCount, &row.AvgDays); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// ReportsCycleTime is the PO Cycle Time report page — GET /reports/cycle-time.
func (h *Handler) ReportsCycleTime(w http.ResponseWriter, r *http.Request) {
	rng := resolveSpendDateRange(r.URL.Query(), time.Now())
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "cycle-time", "Range": rng, "DateRangeAction": "/reports/cycle-time"}
	if h.db == nil {
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
	rng := resolveSpendDateRange(r.URL.Query(), time.Now())
	rows, err := h.queryPOCycleTime(r.Context(), rng)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
type dataQualityPartRow struct {
	PartNumber string
	Title      string
	Category   string
}

func (h *Handler) queryDataQualityParts(ctx context.Context, where string) ([]dataQualityPartRow, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT p.part_number, p.title, p.category
		FROM %s p
		WHERE p.is_active = %s AND %s
		ORDER BY p.part_number ASC
	`, h.cfg.PartsTable(), h.dialect.BoolLiteral(true), where))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []dataQualityPartRow
	for rows.Next() {
		var partNumber, title, category sql.NullString
		if err := rows.Scan(&partNumber, &title, &category); err != nil {
			return nil, err
		}
		result = append(result, dataQualityPartRow{PartNumber: partNumber.String, Title: title.String, Category: category.String})
	}
	return result, rows.Err()
}

// queryPartsNoAttachments lists active BUY/ASM/DWG parts with no attachments
// (part.attachment_count is a trigger-maintained denormalized column, so it's
// selected directly rather than re-COUNTing part_attachment).
func (h *Handler) queryPartsNoAttachments(ctx context.Context) ([]dataQualityPartRow, error) {
	return h.queryDataQualityParts(ctx, "p.category IN ('BUY','ASM','DWG') AND p.attachment_count = 0")
}

// queryPartsMissingDefaultSupplier lists active BUY parts with no default
// supplier set. Scoped to BUY only — ASM/DWG parts are typically manufactured
// in-house and don't need a direct purchasing default supplier.
func (h *Handler) queryPartsMissingDefaultSupplier(ctx context.Context) ([]dataQualityPartRow, error) {
	return h.queryDataQualityParts(ctx, "p.category = 'BUY' AND p.default_supplier_id IS NULL")
}

// queryPartsStaleRollup lists active parts with no cost rollup ever computed.
// NULL-only, no age threshold — the codebase has no existing staleness-by-age
// convention to anchor an arbitrary number on, and "never rolled up" is the
// unambiguous reading of "missing." Not scoped to BUY/ASM/DWG since any
// active part can carry a rollup cost.
func (h *Handler) queryPartsStaleRollup(ctx context.Context) ([]dataQualityPartRow, error) {
	return h.queryDataQualityParts(ctx, "(p.last_rollup_cost IS NULL OR p.last_rollup_at IS NULL)")
}

// ReportsDataQuality is the Attachment / Data Quality Gaps report page — GET
// /reports/data-quality. Point-in-time snapshot; no date range.
func (h *Handler) ReportsDataQuality(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "data-quality"}
	if h.db == nil {
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
		csvRows = append(csvRows, []string{row.PartNumber, row.Title, row.Category})
	}
	return csvRows
}

// ReportsDataQualityNoAttachmentsExportCSV streams the missing-attachments
// table as CSV — GET /reports/data-quality/export-no-attachments.csv.
func (h *Handler) ReportsDataQualityNoAttachmentsExportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queryPartsNoAttachments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeSpendCSV(w, "data_quality_no_attachments.csv", []string{"Part Number", "Title", "Category"}, dataQualityCSVRows(rows))
}

// ReportsDataQualityMissingSupplierExportCSV streams the missing-default-
// supplier table as CSV — GET /reports/data-quality/export-missing-supplier.csv.
func (h *Handler) ReportsDataQualityMissingSupplierExportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queryPartsMissingDefaultSupplier(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeSpendCSV(w, "data_quality_missing_supplier.csv", []string{"Part Number", "Title", "Category"}, dataQualityCSVRows(rows))
}

// ReportsDataQualityStaleRollupExportCSV streams the no/stale-rollup table as
// CSV — GET /reports/data-quality/export-stale-rollup.csv.
func (h *Handler) ReportsDataQualityStaleRollupExportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queryPartsStaleRollup(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeSpendCSV(w, "data_quality_stale_rollup.csv", []string{"Part Number", "Title", "Category"}, dataQualityCSVRows(rows))
}

// formOption is one form listed on a per-form report picker (e.g. Reports >
// Yield Summary, issue #244; Reports > Failure Modes, issue #245).
type formOption struct {
	ID         int
	PartNumber string
	Title      string
}

// loadActiveFormOptions lists active forms for a per-form report picker,
// shared by ReportsYieldPicker and ReportsFailureModesPicker.
func (h *Handler) loadActiveFormOptions(ctx context.Context) ([]formOption, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT f.id, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE pn.category = 'FORM' AND pn.is_active = %s AND f.is_active = %s
		ORDER BY pn.part_number ASC`,
		h.cfg.FormsTable(), h.cfg.PartsTable(), h.dialect.BoolLiteral(true), h.dialect.BoolLiteral(true)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var forms []formOption
	for rows.Next() {
		var f formOption
		if err := rows.Scan(&f.ID, &f.PartNumber, &f.Title); err != nil {
			continue
		}
		forms = append(forms, f)
	}
	return forms, rows.Err()
}

// ReportsYieldPicker is the Reports tab's entry point into the per-form yield
// summary (arx_go/records_yield.go RecordsYieldSummary) — lists forms to pick
// from, since the yield page itself is scoped to one form (issue #244).
func (h *Handler) ReportsYieldPicker(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "yield"}
	if h.db == nil {
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
	if h.db == nil {
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	csvRows := make([][]string, len(rows))
	for i, row := range rows {
		csvRows[i] = []string{row.PartNumber, row.Title, fmt.Sprintf("%.2f", row.TotalSpend)}
	}
	writeSpendCSV(w, "spend-by-part.csv", []string{"Part Number", "Title", "Total Spend"}, csvRows)
}
