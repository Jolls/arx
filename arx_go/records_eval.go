package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/url"
	"strconv"
	"strings"

	"arx/arx_go/models"
	"arx/internal/records"
)

// submittedValues overlays a save's posted result_<id> values onto a record's value map.
func submittedValues(values map[int]string, form url.Values) {
	for key, vals := range form {
		if !strings.HasPrefix(key, "result_") || len(vals) == 0 {
			continue
		}
		if id, err := strconv.Atoi(strings.TrimPrefix(key, "result_")); err == nil {
			values[id] = strings.TrimSpace(vals[0])
		}
	}
}

// saveQueryValues runs the named queries a save of this record needs — query: in a spec bound, or a
// query: nominal something references — against the submitted values (#468). Call it before the save's
// tx. Records whose snapshot has no query: bound or nominal cost one read. A missing record returns
// nil, leaving the not-found answer to the tx's own read.
func (h *Handler) saveQueryValues(ctx context.Context, recordID int, form url.Values) (map[string]string, error) {
	rows, err := h.records().ListResults(ctx, recordID)
	if err != nil || !resultsUseQueries(rows) {
		return nil, err
	}
	got, err := h.records().GetRecord(ctx, recordID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record := testRecord(got)
	if _, ok := form["record_type"]; ok {
		record.RecordType = strings.TrimSpace(form.Get("record_type"))
	}
	var (
		hdr       records.FormHeader
		liveSteps map[int]*models.TestStep
	)
	err = parallel(ctx,
		func(ctx context.Context) (err error) {
			hdr, err = h.records().GetFormHeader(ctx, record.FormID)
			return
		},
		func(ctx context.Context) (err error) {
			liveSteps, err = h.loadSteps(ctx, h.records(), record.FormID)
			return
		},
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	f := testForm(hdr)
	results := testResults(rows)
	c := recordStepCtx(results, &record, &f)
	c.rerunQueries = true
	submittedValues(c.values, form)
	return h.runStepQueries(ctx, recordStepSet(results, liveSteps), c, results), nil
}

// resultsUseQueries reports whether a save may need a query run: a query: bound, or a query: nominal
// that something references ({nom} on its own row, or its {id} anywhere) — a picker alone never does.
func resultsUseQueries(rows []records.ResultRow) bool {
	var all strings.Builder
	for _, r := range rows {
		if strings.Contains(r.SpecMin, "query:") || strings.Contains(r.SpecMax, "query:") {
			return true
		}
		all.WriteString(r.Parameter + r.Specification + r.SpecMin + r.SpecNom + r.SpecMax + r.SpecUnits + r.DefaultResult + r.HideFormula)
	}
	text := all.String()
	for _, r := range rows {
		if strings.Contains(r.SpecNom, "query:") &&
			(strings.Contains(r.Parameter+r.Specification+r.DefaultResult, "{nom}") || strings.Contains(text, "{"+strconv.Itoa(r.FormRowID)+"}")) {
			return true
		}
	}
	return false
}

// runStepQueries runs, in parallel, every query resolving the record's own steps needs, repeating while a
// query's parameters depend on another query's result. A query that returns no row resolves to nothing;
// one that fails is logged and left unrun, so the save falls back to its stored value.
func (h *Handler) runStepQueries(ctx context.Context, steps map[int]*models.TestStep, c stepCtx, onRecord map[int]*models.TestResult) map[string]string {
	queries := map[string]string{}
	failed := map[string]bool{}
	for range 3 {
		c.queries = queries
		res := newStepResolver(steps, c)
		for id := range onRecord {
			res.step(id)
		}
		var pending []string
		for _, spec := range res.pendingQueries() {
			if !failed[spec] {
				pending = append(pending, spec)
			}
		}
		if len(pending) == 0 {
			break
		}
		vals := make([]string, len(pending))
		errs := make([]error, len(pending))
		branches := make([]func(context.Context) error, len(pending))
		for i, spec := range pending {
			branches[i] = func(ctx context.Context) error {
				qr, err := h.runNamedQuery(ctx, spec)
				if errs[i] = err; err == nil && len(qr.Rows) > 0 {
					vals[i] = qr.Rows[0].Value
				}
				return nil
			}
		}
		_ = parallel(ctx, branches...) // branches never fail
		for i, spec := range pending {
			if errs[i] != nil {
				log.Printf("step query %q: %v", spec, errs[i])
				failed[spec] = true
				continue
			}
			queries[spec] = vals[i]
		}
	}
	return queries
}
