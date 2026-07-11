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

	data["OpenPOCount"] = openPOs
	data["POsReceivedThisMonth"] = receivedThisMonth
	data["RecentActivity"] = activity
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
		`SELECT COUNT(DISTINCT po_id) FROM %s WHERE date_received >= DATEFROMPARTS(YEAR(GETDATE()), MONTH(GETDATE()), 1)`,
		h.cfg.POLineTable()),
	).Scan(&n)
	return n, err
}

// dashboardRecentActivity merges the most recently modified parts with the
// most recent PO history events into one newest-first feed, capped at limit.
func (h *Handler) dashboardRecentActivity(ctx context.Context, limit int) ([]dashboardActivityItem, error) {
	var items []dashboardActivityItem

	partRows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT TOP (@p1) id, part_number, title, modified_date
		 FROM %s WHERE modified_date IS NOT NULL ORDER BY modified_date DESC`,
		h.cfg.PartsTable()), limit)
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
		`SELECT TOP (@p1) h.po_id, po.number, h.event_type, h.to_status, h.action, h.changed_at
		 FROM %s h JOIN %s po ON h.po_id = po.id ORDER BY h.changed_at DESC`,
		h.cfg.POHistoryTable(), h.cfg.POTable()), limit)
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

// spendDateRange is the resolved date-range selection for the Spend Analysis
// report. From/To follow the same inclusive-day convention as recordFilters
// (records_filters.go): zero value means no bound, and an inclusive To is
// applied in SQL via DATEADD(day, 1, ...).
type spendDateRange struct {
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
func resolveSpendDateRange(q url.Values, now time.Time) spendDateRange {
	preset := q.Get("range")

	var rng spendDateRange
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

// spendWhereClause builds the date-range WHERE fragment (starting with " AND")
// and its args for filtering purchase_order.date_ordered, following the same
// conditional-bound pattern as recordFilters.whereClauses.
func (rng spendDateRange) whereClause(startArg int) (string, []any) {
	var sb strings.Builder
	var args []any
	n := startArg

	if !rng.From.IsZero() {
		fmt.Fprintf(&sb, " AND po.date_ordered >= @p%d", n)
		args = append(args, rng.From)
		n++
	}
	if !rng.To.IsZero() {
		fmt.Fprintf(&sb, " AND po.date_ordered < DATEADD(day, 1, @p%d)", n)
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
func (h *Handler) querySpendBySupplier(ctx context.Context, rng spendDateRange) ([]spendSupplierRow, error) {
	where, args := rng.whereClause(1)
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT po.supplier_name, ISNULL(SUM(pol.qty * pol.unit_cost), 0) AS total_spend
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
func (h *Handler) querySpendByPart(ctx context.Context, rng spendDateRange) ([]spendPartRow, error) {
	where, args := rng.whereClause(1)
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT pol.part_number_snapshot, p.title, ISNULL(SUM(pol.qty * pol.unit_cost), 0) AS total_spend
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
	data := map[string]any{"ActiveTab": "reports", "ActiveSubTab": "spend", "Range": rng}
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

// yieldFormOption is one form listed on the Reports > Yield Summary picker (issue #244).
type yieldFormOption struct {
	ID         int
	PartNumber string
	Title      string
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

	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE pn.category = 'FORM' AND pn.is_active = 1 AND f.is_active = 1
		ORDER BY pn.part_number ASC`,
		h.cfg.FormsTable(), h.cfg.PartsTable()))
	if err != nil {
		h.renderError(w, r, "Error loading forms: "+err.Error())
		return
	}
	defer rows.Close()

	var forms []yieldFormOption
	for rows.Next() {
		var f yieldFormOption
		if err := rows.Scan(&f.ID, &f.PartNumber, &f.Title); err != nil {
			continue
		}
		forms = append(forms, f)
	}

	data["Forms"] = forms
	h.render(w, r, "reports/yield_picker.html", data)
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
