package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"arx/arx_go/models"
	"arx/internal/records"
)

// stepDefJSON is one step definition as the browser sends it to /api/steps/resolve: the edit page's frozen
// rows (embedded at render), or the definition editor's unsaved rows (new rows carry negative ids).
type stepDefJSON struct {
	ID            int    `json:"id"`
	Type          int    `json:"type"`
	Parameter     string `json:"parameter"`
	Specification string `json:"specification"`
	SpecNom       string `json:"spec_nom"`
	SpecMin       string `json:"spec_min"`
	SpecMax       string `json:"spec_max"`
	SpecUnits     string `json:"spec_units"`
	PFType        string `json:"pf_type"`
	DefaultResult string `json:"default_result"`
	HideFormula   string `json:"hide"`
}

func stepDef(s *models.TestStep) stepDefJSON {
	return stepDefJSON{ID: s.ID, Type: s.Type, Parameter: s.Parameter, Specification: s.Specification,
		SpecNom: s.SpecNom, SpecMin: s.SpecMin, SpecMax: s.SpecMax, SpecUnits: s.SpecUnits, PFType: s.PFType,
		DefaultResult: s.DefaultResult, HideFormula: s.HideFormula}
}

func (d stepDefJSON) step() *models.TestStep {
	return &models.TestStep{ID: d.ID, Type: d.Type, Parameter: d.Parameter, Specification: d.Specification,
		SpecNom: d.SpecNom, SpecMin: d.SpecMin, SpecMax: d.SpecMax, SpecUnits: d.SpecUnits, PFType: d.PFType,
		DefaultResult: d.DefaultResult, HideFormula: d.HideFormula}
}

// stepRecordJSON / stepFormJSON are the record and form fields tokens read.
type stepRecordJSON struct {
	Type   string     `json:"type"`
	PN     string     `json:"pn"`
	SN     string     `json:"sn"`
	PNDesc string     `json:"pndesc"`
	Date   *time.Time `json:"date"`
}

type stepFormJSON struct {
	ID   int    `json:"id"`
	PNID int    `json:"pnid"`
	PN   string `json:"pn"`
}

// stepResolveRequest is the body of POST /api/steps/resolve. The edit page sends its embedded context
// (steps, record, form, stored) plus the live values; the definition editor sends its rows plus form_id
// and, for a preview, a sample record_id (-1 = the form's newest record) whose results fill in values.
type stepResolveRequest struct {
	Steps      []stepDefJSON        `json:"steps"`
	Show       []int                `json:"show"` // ids to return; empty = every step
	Values     map[int]string       `json:"values"`
	Stored     map[int]stepEvalJSON `json:"stored"`
	Record     *stepRecordJSON      `json:"record"`
	Form       *stepFormJSON        `json:"form"`
	FormID     int                  `json:"form_id"`
	RecordID   int                  `json:"record_id"`
	Queries    map[string]string    `json:"queries"`        // queries already run (earlier responses')
	Failed     []string             `json:"failed_queries"` // queries that failed earlier: not retried
	RunQueries bool                 `json:"run_queries"`    // false = no query runs (definition-editor lint)
	Lint       bool                 `json:"lint"`           // return definition warnings (definition editor)
}

type stepEvalJSON struct {
	Min string `json:"min"`
	Nom string `json:"nom"`
	Max string `json:"max"`
}

type stepResolvedJSON struct {
	Parameter     string        `json:"parameter"`
	Specification string        `json:"specification"`
	SpecNom       string        `json:"spec_nom"`
	SpecMin       string        `json:"spec_min"`
	SpecMax       string        `json:"spec_max"`
	SpecUnits     string        `json:"spec_units"`
	DefaultResult string        `json:"default_result"`
	Hidden        bool          `json:"hidden"`
	Warnings      []stepWarning `json:"warnings,omitempty"`
}

type stepResolveResponse struct {
	Steps        map[int]stepResolvedJSON `json:"steps"`
	Queries      map[string]string        `json:"queries"`
	QueryErrors  map[string]string        `json:"query_errors,omitempty"`
	SampleRecord string                   `json:"sample_record,omitempty"` // preview: which record supplied the context
}

// stepPageCtx is the edit page's embedded resolution context (record_edit.html), posted back with the live
// values on every change.
type stepPageCtx struct {
	Steps  []stepDefJSON        `json:"steps"`
	Show   []int                `json:"show"`
	Stored map[int]stepEvalJSON `json:"stored"`
	Record stepRecordJSON       `json:"record"`
	Form   stepFormJSON         `json:"form"`
}

// maxResolveSteps caps a resolve request; a form has far fewer steps.
const maxResolveSteps = 2000

// APIStepsResolve — POST /api/steps/resolve
// Resolves step definitions against values without writing anything (#469, #471): the edit page's live
// re-evaluation, the definition editor's preview and its lint. Runs only the named queries resolution needs
// that the caller hasn't already run (Queries), and only when RunQueries is set.
func (h *Handler) APIStepsResolve(w http.ResponseWriter, r *http.Request) {
	var req stepResolveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if len(req.Steps) > maxResolveSteps {
		http.Error(w, "too many steps", http.StatusBadRequest)
		return
	}

	steps := make(map[int]*models.TestStep, len(req.Steps))
	ids := make(map[int]bool, len(req.Steps))
	for _, d := range req.Steps {
		steps[d.ID] = d.step()
		ids[d.ID] = true
	}
	c := stepCtx{values: map[int]string{}, stored: map[int]stepEvals{}, rerunQueries: req.RunQueries}
	for id, e := range req.Stored {
		c.stored[id] = stepEvals{Min: e.Min, Nom: e.Nom, Max: e.Max}
	}
	if req.Record != nil {
		c.record = &models.TestRecord{RecordType: req.Record.Type, SerialNumberPN: req.Record.PN,
			SerialNumber: req.Record.SN, SerialNumberDesc: req.Record.PNDesc, RecordDate: req.Record.Date}
	}
	if req.Form != nil {
		c.form = &models.TestForm{ID: req.Form.ID, PartNumberID: req.Form.PNID, PartNumber: req.Form.PN}
	}

	var resp stepResolveResponse
	if req.FormID > 0 {
		sample, err := h.previewContext(r.Context(), req.FormID, req.RecordID, &c)
		if err != nil {
			serverError(w, "query error", err)
			return
		}
		resp.SampleRecord = sample
	}
	for id, v := range req.Values { // typed values win over a sample record's
		c.values[id] = v
	}

	show := req.Show
	if len(show) == 0 {
		for id := range steps {
			show = append(show, id)
		}
	}
	c.queries = req.Queries
	if req.RunQueries {
		c.queries, resp.QueryErrors = h.runStepQueries(r.Context(), steps, c, show, req.Failed)
	}

	res := newStepResolver(steps, c)
	resp.Steps = make(map[int]stepResolvedJSON, len(show))
	for _, id := range show {
		rs := res.step(id)
		if rs == nil {
			continue
		}
		out := stepResolvedJSON{Parameter: rs.Parameter, Specification: rs.Specification, SpecNom: rs.SpecNom,
			SpecMin: rs.SpecMin, SpecMax: rs.SpecMax, SpecUnits: rs.SpecUnits, DefaultResult: rs.DefaultResult,
			Hidden: rs.Hidden}
		if req.Lint {
			out.Warnings = lintStep(steps[id], ids)
		}
		resp.Steps[id] = out
	}
	resp.Queries = c.queries
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// previewContext fills c with a definition-editor preview's context: the form (for {form.X}) and, when
// recordID is set (-1 = the form's newest record), that record's fields, results and stored values.
// Returns a label for the sample record ("" when none).
func (h *Handler) previewContext(ctx context.Context, formID, recordID int, c *stepCtx) (string, error) {
	hdr, err := h.records().GetFormHeader(ctx, formID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	f := testForm(hdr)
	c.form = &f
	if recordID < 0 {
		if recordID, err = h.records().NewestFormRecordID(ctx, formID); errors.Is(err, sql.ErrNoRows) {
			return "", nil
		} else if err != nil {
			return "", err
		}
	}
	if recordID == 0 {
		return "", nil
	}
	var (
		rec  records.Record
		rows []records.ResultRow
	)
	err = parallel(ctx,
		func(ctx context.Context) (err error) { rec, err = h.records().GetRecord(ctx, recordID); return },
		func(ctx context.Context) (err error) { rows, err = h.records().ListResults(ctx, recordID); return },
	)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && rec.FormID != formID) {
		return "", nil // not a record of this form: preview without record context
	}
	if err != nil {
		return "", err
	}
	record := testRecord(rec)
	c.record = &record
	rc := recordStepCtx(testResults(rows), &record, &f)
	c.values, c.stored = rc.values, rc.stored
	return "#" + strconv.Itoa(record.ID) + " (SN " + record.SerialNumber + ")", nil
}
