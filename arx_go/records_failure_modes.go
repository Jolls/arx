package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"arx/arx_go/models"

	"github.com/go-chi/chi/v5"
)

// failureModeRow is one test step's failure tally within a form's failure
// mode report.
type failureModeRow struct {
	Parameter    string
	FailureCount int
	TotalTested  int
}

// FailureRatePct returns the failure rate percentage, or 0 if the step was
// never tested.
func (row failureModeRow) FailureRatePct() float64 {
	if row.TotalTested == 0 {
		return 0
	}
	return float64(row.FailureCount) / float64(row.TotalTested) * 100
}

// RecordsFailureModes — GET /forms/{id}/failure-modes
// Shows each test step's failure count, total tested, and failure rate across
// all of a form's records, ranked by failure count descending, over a
// selectable date range (issue #245).
func (h *Handler) RecordsFailureModes(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var form models.TestForm
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), formID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.PartNumber, &form.Title)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	filters := parseRecordFilters(r.URL.Query())
	dateClause, dateArgs := filters.dateRangeClauses(2)
	args := append([]any{formID}, dateArgs...)

	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT MAX(res.parameter) AS parameter,
			SUM(CASE WHEN res.pass_fail = 0 THEN 1 ELSE 0 END) AS failure_count,
			COUNT(res.pass_fail) AS total_tested
		FROM %s res
		JOIN %s trec ON res.record_id = trec.id
		WHERE trec.form_id = @p1 AND trec.is_active = 1 AND res.pass_fail IS NOT NULL%s
		GROUP BY res.test_id
		ORDER BY failure_count DESC, parameter ASC`,
		h.cfg.ResultsTable(), h.cfg.RecordsTable(), dateClause), args...)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var steps []failureModeRow
	for rows.Next() {
		var row failureModeRow
		if err := rows.Scan(&row.Parameter, &row.FailureCount, &row.TotalTested); err != nil {
			continue
		}
		steps = append(steps, row)
	}

	h.renderTR(w, r, "failure_modes.html", map[string]any{
		"Form":      form,
		"Steps":     steps,
		"FromStr":   filters.FromStr,
		"ToStr":     filters.ToStr,
		"ActiveTab": "records",
		"TestMode":  h.cfg.TestMode,
	})
}
