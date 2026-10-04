package main

import (
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// yieldRecord is one form record's date and whether any of its results failed.
// RecordDate is nil if the record has no record_date set.
type yieldRecord struct {
	RecordDate *time.Time
	AnyFail    bool
}

// yieldBucket is a summarized pass/fail count, either the overall total or one month.
type yieldBucket struct {
	Label  string // "Total" or "2026-07"
	Total  int
	Passed int
	Failed int
}

// FPYPct returns the first-pass yield percentage, or 0 if there are no records.
func (b yieldBucket) FPYPct() float64 {
	if b.Total == 0 {
		return 0
	}
	return float64(b.Passed) / float64(b.Total) * 100
}

// addYieldRecord tallies one record into a bucket's total/passed/failed counts.
func addYieldRecord(b *yieldBucket, anyFail bool) {
	b.Total++
	if anyFail {
		b.Failed++
	} else {
		b.Passed++
	}
}

// computeYieldBuckets aggregates records into an overall total bucket and, if
// grouped is true, one bucket per record_date month (ordered chronologically).
// A record counts as failed if any of its results failed (pass_fail = 0);
// otherwise it counts as passed, including records with no evaluated results yet.
func computeYieldBuckets(records []yieldRecord, grouped bool) (total yieldBucket, monthly []yieldBucket) {
	total.Label = "Total"
	monthBuckets := make(map[string]*yieldBucket)

	for _, rec := range records {
		addYieldRecord(&total, rec.AnyFail)

		if !grouped {
			continue
		}
		key := "Unknown"
		if rec.RecordDate != nil {
			key = rec.RecordDate.Format("2006-01")
		}
		mb, ok := monthBuckets[key]
		if !ok {
			mb = &yieldBucket{Label: key}
			monthBuckets[key] = mb
		}
		addYieldRecord(mb, rec.AnyFail)
	}

	if grouped {
		keys := make([]string, 0, len(monthBuckets))
		for key := range monthBuckets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			monthly = append(monthly, *monthBuckets[key])
		}
	}
	return total, monthly
}

// RecordsYieldSummary — GET /forms/{id}/yield
// Shows total/passed/failed record counts and first-pass yield % for a form
// over a selectable date range, optionally broken down by month.
func (h *Handler) RecordsYieldSummary(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(r.PathValue("id"))
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
	grouped := r.URL.Query().Get("group") == "month"

	rows, err := h.records().ListYieldRecords(r.Context(), formID, filters.From, filters.To)
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	var records []yieldRecord
	for _, rec := range rows {
		records = append(records, yieldRecord{RecordDate: rec.RecordDate, AnyFail: rec.AnyFail})
	}

	total, monthly := computeYieldBuckets(records, grouped)

	h.renderRecords(w, r, "yield.html", map[string]any{
		"Form":      form,
		"Total":     total,
		"Monthly":   monthly,
		"Grouped":   grouped,
		"FromStr":   filters.FromStr,
		"ToStr":     filters.ToStr,
		"ActiveTab": "records",
		"TestMode":  h.cfg().TestMode,
	})
}
