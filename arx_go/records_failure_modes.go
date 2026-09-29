package main

import (
	"database/sql"
	"net/http"
	"strconv"

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

	hdr, err := h.records().GetFormHeader(r.Context(), formID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	form := testForm(hdr)

	filters := parseRecordFilters(r.URL.Query())

	rows, err := h.records().ListFailureModes(r.Context(), formID, filters.From, filters.To)
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	var steps []failureModeRow
	for _, row := range rows {
		steps = append(steps, failureModeRow{Parameter: row.Parameter, FailureCount: row.FailureCount, TotalTested: row.TotalTested})
	}

	h.renderRecords(w, r, "failure_modes.html", map[string]any{
		"Form":      form,
		"Steps":     steps,
		"FromStr":   filters.FromStr,
		"ToStr":     filters.ToStr,
		"ActiveTab": "records",
		"TestMode":  h.cfg().TestMode,
	})
}
