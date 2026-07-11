package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
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
	data := map[string]any{"ActiveTab": "reports"}
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
