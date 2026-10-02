package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/internal/inventory"
	"arx/internal/records"
)

func (h *Handler) records() *records.Service { return records.New(handlerDB{h}) }

// recordsTx runs fn against a records service bound to one transaction, so a state change and the
// audit event that records it commit or roll back together.
func (h *Handler) recordsTx(ctx context.Context, fn func(*records.Service) error) error {
	tx, err := h.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(records.New(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// testForm copies a form header into the model the templates render.
func testForm(f records.FormHeader) models.TestForm {
	return models.TestForm{
		ID: f.ID, PartNumberID: f.PartNumberID, IsLocked: f.IsLocked, TestOrder: f.TestOrder,
		PartNumber: f.PartNumber, Description: f.Description, RecordTypes: f.RecordTypes,
		InstrumentTypes: f.InstrumentTypes, Revision: f.Revision,
	}
}

// testRecord copies a record row into the model the templates render.
func testRecord(r records.Record) models.TestRecord {
	return models.TestRecord{
		ID: r.ID, FormID: r.FormID, PartNumberID: r.PartID, SerialNumber: r.SerialNumber,
		SerialNumberPN: r.SubjectPartNumber, SerialNumberDesc: r.SubjectPNDescription, RecordDate: r.RecordDate,
		RecordType: r.RecordType, Notes: r.Notes, InstrumentType: r.InstrumentType, IsLocked: r.IsLocked,
		IsApproved: r.IsApproved, IsActive: r.IsActive, TestOrder: r.TestOrder,
		LotID: r.LotID, BuildID: r.BuildID, UnitID: r.UnitID,
	}
}

// recordStatus is the records-table status label of a listed record.
func recordStatus(locked, approved bool) string {
	switch {
	case approved:
		return "approved"
	case locked:
		return "complete"
	default:
		return "wip"
	}
}

// refToken matches {123} step-ID tokens, {record.field}, and {form.field} context tokens.
var refToken = regexp.MustCompile(`\{(\d+|record\.\w+|form\.\w+)\}`)

// stepAppliesToRecord returns true if the step should be shown for the given instrument type.
// Empty instrument_types on the step means the step applies to all records.
// Empty instrument_type on the record means no filtering — show all steps.
func stepAppliesToRecord(instrumentTypes, recordType string) bool {
	if instrumentTypes == "" || recordType == "" {
		return true
	}
	for t := range strings.SplitSeq(instrumentTypes, ",") {
		if strings.EqualFold(strings.TrimSpace(t), strings.TrimSpace(recordType)) {
			return true
		}
	}
	return false
}

// stepVisibleOnRecord decides whether an archived step should appear on a record.
// Archived (retired) steps are kept off new records but stay visible on historical
// records that already recorded a result for them. Non-archived steps are unaffected.
func stepVisibleOnRecord(archived, hasResult bool) bool {
	return !archived || hasResult
}

// stepFromResult builds a render step from a materialized snapshot row (#487). A saved record
// renders entirely from these — the live form_row is never consulted for a materialized row.
func stepFromResult(tid int, res *models.TestResult) *models.TestStep {
	return &models.TestStep{
		ID:            tid,
		Type:          res.Type,
		Parameter:     res.Parameter,
		Specification: res.Specification,
		SpecMin:       res.SpecMin,
		SpecNom:       res.SpecNom,
		SpecMax:       res.SpecMax,
		SpecUnits:     res.SpecUnits,
		PFType:        res.PFType,
		Format:        res.Format,
		HideFormula:   res.HideFormula,
		DefaultResult: res.DefaultResult,
	}
}

// bakeStepTokens resolves self ({nom}/{min}/{max}/{units}), {record.X} and {form.X} tokens on a step
// in place, leaving numeric {id} cross-step tokens intact for render-time resolution (#487). Passing
// nil results/steps to substituteRefs is what preserves the {id} tokens. Used at materialization and
// for live-definition fallback rows so every row reaches the caller in the same "baked except {id}"
// state.
func bakeStepTokens(step *models.TestStep, record *models.TestRecord, form *models.TestForm) {
	step.SpecMin = substituteRefs(step.SpecMin, nil, nil, record, form)
	step.SpecMax = substituteRefs(step.SpecMax, nil, nil, record, form)
	step.SpecNom = substituteRefs(step.SpecNom, nil, nil, record, form)
	step.SpecUnits = substituteRefs(step.SpecUnits, nil, nil, record, form)
	step.Parameter = substituteRefs(step.Parameter, nil, nil, record, form)
	step.Specification = substituteRefs(step.Specification, nil, nil, record, form)
	step.DefaultResult = substituteRefs(step.DefaultResult, nil, nil, record, form)
	// Self tokens ({nom}/{min}/{max}/{units}) only when spec_nom is a literal — a query:/List:
	// directive must not be baked into the spec/parameter text (it's resolved live during edit).
	if !strings.HasPrefix(step.SpecNom, "query:") && !strings.HasPrefix(step.SpecNom, "List:") {
		step.Parameter = substituteStepSelf(step.Parameter, step)
		step.Specification = substituteStepSelf(step.Specification, step)
		step.DefaultResult = substituteStepSelf(step.DefaultResult, step)
	}
}

// resolveStepRefs resolves numeric {id} cross-step tokens against the record's own results (frozen —
// never the live definition). Self/record/form tokens are assumed already baked.
func resolveStepRefs(step *models.TestStep, results map[int]*models.TestResult, refSteps map[int]*models.TestStep, record *models.TestRecord, form *models.TestForm) {
	step.Parameter = substituteRefs(step.Parameter, results, refSteps, record, form)
	step.Specification = substituteRefs(step.Specification, results, refSteps, record, form)
	step.SpecNom = substituteRefs(step.SpecNom, results, refSteps, record, form)
	step.SpecMin = substituteRefs(step.SpecMin, results, refSteps, record, form)
	step.SpecMax = substituteRefs(step.SpecMax, results, refSteps, record, form)
	step.DefaultResult = substituteRefs(step.DefaultResult, results, refSteps, record, form)
}

// substituteStepSelf replaces {min}, {max}, {nom} in s with the step's own spec bound values.
// Called after substituteRefs so cross-step tokens resolve first.
func substituteStepSelf(s string, step *models.TestStep) string {
	if step == nil || (!strings.Contains(s, "{min}") && !strings.Contains(s, "{max}") && !strings.Contains(s, "{nom}") && !strings.Contains(s, "{units}")) {
		return s
	}
	s = strings.ReplaceAll(s, "{min}", step.SpecMin)
	s = strings.ReplaceAll(s, "{max}", step.SpecMax)
	s = strings.ReplaceAll(s, "{nom}", step.SpecNom)
	s = strings.ReplaceAll(s, "{units}", step.SpecUnits)
	return s
}

// serialAllocLockNS namespaces the per-form advisory lock that serializes
// auto serial allocation in CreateRecord (arx-legacy#369, #33).
const serialAllocLockNS = 369

// isAutoSerial reports whether the submitted serial number is the unchanged
// GET-time suggestion (the user accepted the default), meaning the server should
// re-derive it atomically. A differing value is a deliberate manual override.
func isAutoSerial(submitted, suggested string) bool {
	return strings.TrimSpace(submitted) == strings.TrimSpace(suggested)
}

// substituteRefs replaces tokens in s:
//   - {123}              → recorded result for step 123, falling back to that step's spec_nom
//   - {record.type}      → record's Type (record_type field)
//   - {record.pn}        → record's unit-under-test part number (subject_part_number)
//   - {record.sn}        → record's serial number
//   - {record.pndesc}    → record's unit-under-test description (subject_pn_description / description)
//   - {record.date}      → record's test date (MM/DD/YYYY) — date only, safe for SQL format 101
//   - {record.datetime}  → record's test date + time (MM/DD/YYYY H:MM AM/PM)
//
// Unresolvable tokens are left as-is. Pass nil for any context that isn't available.
func substituteRefs(s string, results map[int]*models.TestResult, steps map[int]*models.TestStep, record *models.TestRecord, form *models.TestForm) string {
	return refToken.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]

		if strings.HasPrefix(inner, "record.") {
			if record == nil {
				return m
			}
			switch inner {
			case "record.type":
				return record.RecordType
			case "record.pn":
				return record.SerialNumberPN
			case "record.sn":
				return record.SerialNumber
			case "record.pndesc":
				return record.SerialNumberDesc
			case "record.date":
				if record.RecordDate != nil {
					return record.RecordDate.Format("01/02/2006")
				}
				return ""
			case "record.datetime":
				if record.RecordDate != nil {
					return record.RecordDate.Format("01/02/2006 3:04 PM")
				}
				return ""
			}
			return m
		}

		if strings.HasPrefix(inner, "form.") {
			if form == nil {
				return m
			}
			switch inner {
			case "form.id":
				return strconv.Itoa(form.ID)
			case "form.pnid":
				return strconv.Itoa(form.PartNumberID)
			case "form.pn":
				return form.PartNumber
			}
			return m
		}

		id, err := strconv.Atoi(inner)
		if err != nil {
			return m
		}
		if results != nil {
			if res, ok := results[id]; ok && res.Result != "" {
				return res.Result
			}
		}
		if steps != nil {
			if step, ok := steps[id]; ok && step.SpecNom != "" {
				return step.SpecNom
			}
		}
		return m
	})
}

// FormsList â€" GET /
func (h *Handler) FormsList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.records().ListForms(r.Context())
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	var forms []models.TestForm
	for _, f := range rows {
		forms = append(forms, models.TestForm{ID: f.ID, PartNumberID: f.PartNumberID, IsLocked: f.IsLocked,
			Revision: f.Revision, PartNumber: f.PartNumber, Description: f.Description})
	}

	h.renderRecords(w, r, "index.html", map[string]any{
		"Forms":     forms,
		"ActiveTab": "records",
		"TestMode":  h.cfg().TestMode,
	})
}

// RecordsList â€" GET /forms/{id}/records
func (h *Handler) RecordsList(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Load the form header.
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

	lockedCount, _ := strconv.Atoi(r.URL.Query().Get("locked"))

	// Distinct Type (record_type) values for this form, to populate the filter datalist.
	typeOptions, terr := h.records().ListRecordTypes(r.Context(), records.ScopeForm, formID)
	if terr != nil {
		log.Printf("RecordsList: type filter error: %v", terr)
	}

	h.renderRecords(w, r, "records_index.html", map[string]any{
		"Form":        form,
		"TypeOptions": typeOptions,
		"LockedCount": lockedCount,
		"CSRFToken":   h.csrfToken(w, r),
		"ActiveTab":   "records",
		"TestMode":    h.cfg().TestMode,
	})
}

// RecordsRows — GET /api/forms/{id}/records/rows
// JSON rows for the shared client-side table (mirrors PartsRows/PORows/etc. in parts.go/pos.go).
// Filtering/sorting/pagination are done client-side; this returns every active record
// for the form in the same default order RecordsList used to apply server-side.
func (h *Handler) RecordsRows(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	start := time.Now()

	type row struct {
		ID           int    `json:"id"`
		PartNumberID int    `json:"pnId"`
		SN           string `json:"sn"`
		SNPN         string `json:"snPN"`
		SNDesc       string `json:"snDesc"`
		Date         string `json:"date"`
		Type         string `json:"type"`
		Status       string `json:"status"`
		FormRev      string `json:"formRev"`
	}

	listed, err := h.records().ListFormRecords(r.Context(), formID)
	if err != nil {
		serverError(w, "database error", err)
		return
	}

	out := make([]row, 0)
	for _, l := range listed {
		rec := row{ID: l.ID, PartNumberID: l.PartID, SN: l.SerialNumber, SNPN: l.SubjectPartNumber,
			SNDesc: l.SubjectPNDescription, Type: l.RecordType, Status: recordStatus(l.IsLocked, l.IsApproved),
			FormRev: models.TestRecord{FormRevision: l.FormRevision}.FormRevLabel()}
		if l.RecordDate != nil {
			rec.Date = l.RecordDate.Format("2006-01-02 15:04")
		}
		out = append(out, rec)
	}
	log.Printf("[rows] records form=%d: %d rows in %v", formID, len(out), time.Since(start))
	writeJSON(w, out)
}

// scopedRecordRow is one row for the Part/Lot/Unit-scoped records tables
// (PartRecordsRows/LotRecordsRows/UnitRecordsRows, #875) — like RecordsRows'
// row but spans multiple forms, so it carries the Form (test template) too.
type scopedRecordRow struct {
	ID           int    `json:"id"`
	PartNumberID int    `json:"pnId"`
	SN           string `json:"sn"`
	SNPN         string `json:"snPN"`
	SNDesc       string `json:"snDesc"`
	Date         string `json:"date"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	FormRev      string `json:"formRev"`
	FormID       int    `json:"formId"`
	FormLabel    string `json:"formLabel"` // "<form part number> — <form description>"
}

// scopedRecordsRows returns every active form_record of a part, lot or unit
// (records.ScopePart / ScopeLot / ScopeUnit), across all forms, for the
// Part/Lot/Unit records tables (#875).
func (h *Handler) scopedRecordsRows(ctx context.Context, scope records.Scope, id int) ([]scopedRecordRow, error) {
	listed, err := h.records().ListScopedRecords(ctx, scope, id)
	if err != nil {
		return nil, err
	}

	out := make([]scopedRecordRow, 0)
	for _, l := range listed {
		rec := scopedRecordRow{ID: l.ID, PartNumberID: l.PartID, SN: l.SerialNumber, SNPN: l.SubjectPartNumber,
			SNDesc: l.SubjectPNDescription, Type: l.RecordType, Status: recordStatus(l.IsLocked, l.IsApproved),
			FormRev: models.TestRecord{FormRevision: l.FormRevision}.FormRevLabel(), FormID: l.FormID}
		if l.RecordDate != nil {
			rec.Date = l.RecordDate.Format("2006-01-02 15:04")
		}
		rec.FormLabel = l.FormPartNumber
		if l.FormDescription != "" {
			rec.FormLabel += " — " + l.FormDescription
		}
		out = append(out, rec)
	}
	return out, nil
}

// scopedRecordTypeOptions returns distinct non-empty record_type (Type) values for
// the filter-row datalist, scoped the same way as scopedRecordsRows.
func (h *Handler) scopedRecordTypeOptions(ctx context.Context, scope records.Scope, id int) ([]string, error) {
	return h.records().ListRecordTypes(ctx, scope, id)
}

// FormDef â€" GET /forms/{id}/def
// Shows all test step definitions for a form without any result data.
// Used as a reference when creating a new test record.
func (h *Handler) FormDef(w http.ResponseWriter, r *http.Request) {
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

	stepsMap, err := h.loadSteps(r.Context(), h.records(), formID)
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	ids := form.OrderedTestIDs()
	var steps []*models.TestStep
	hasArchived := false
	for _, tid := range ids {
		step, ok := stepsMap[tid]
		if !ok {
			continue
		}
		// nil results/record: form-definition view has no record context; unresolvable tokens
		// leave the formula unchanged so comparisons don't match → step shown. Intentional.
		if evaluateHide(step.HideFormula, nil, stepsMap, nil, &form) {
			continue
		}
		// Archived steps are still rendered here but hidden by default; the "Show archived"
		// toggle reveals them. Skipping them entirely would hide them from the definition view.
		if step.Archived {
			hasArchived = true
		}
		steps = append(steps, step)
	}

	// Resolve {id} cross-reference tokens; no results yet so falls back to spec_nom.
	// Named queries get a display label â€" params can't be resolved without a real record.
	for _, step := range steps {
		if step.Type > 0 {
			continue
		}
		step.Parameter = substituteRefs(step.Parameter, nil, stepsMap, nil, &form)
		step.SpecMin = substituteRefs(step.SpecMin, nil, stepsMap, nil, &form)
		step.SpecMax = substituteRefs(step.SpecMax, nil, stepsMap, nil, &form)
	}

	// Load history timestamps for timeline dots. changed_at is a timestamptz
	// assigned by the database (#192), so the calendar-day
	// bucketing happens in Go in the viewing user's timezone (#847) — SQL-side
	// AT TIME ZONE would need Windows zone names, not the IANA names we store.
	type HistoryPoint struct {
		At      time.Time
		Count   int
		PctLeft float64 // position along timeline bar (5â€"95%)
	}
	loc := h.userLocation(r)
	stamps, err := h.records().ListFormHistoryStamps(r.Context(), formID)
	var histPoints []HistoryPoint
	if err == nil {
		// Rows arrive ORDER BY changed_at ASC, so same-day rows (keyed by local date in
		// loc) are contiguous — track only the current day's bucket instead of a map
		// keyed by day string.
		type dayBucket struct {
			day  time.Time
			key  string
			rows map[int]bool
		}
		var buckets []*dayBucket
		var current *dayBucket
		for _, st := range stamps {
			rowID := st.FormRowID
			local := st.ChangedAt.In(loc)
			key := local.Format("2006-01-02")
			if current == nil || current.key != key {
				day, err := time.ParseInLocation("2006-01-02", key, loc)
				if err != nil {
					continue
				}
				current = &dayBucket{day: day, key: key, rows: map[int]bool{}}
				buckets = append(buckets, current)
			}
			current.rows[rowID] = true
		}
		for _, b := range buckets {
			histPoints = append(histPoints, HistoryPoint{At: b.day, Count: len(b.rows)})
		}
	}
	// Position dots: earliest â†' 5%, now â†' 95%.
	if len(histPoints) > 0 {
		earliest := histPoints[0].At.Unix()
		now := time.Now().Unix()
		span := float64(now - earliest)
		for i := range histPoints {
			if span <= 0 {
				histPoints[i].PctLeft = 50
			} else {
				pct := float64(histPoints[i].At.Unix()-earliest) / span * 83
				histPoints[i].PctLeft = 5 + pct
			}
		}
	}

	h.renderRecords(w, r, "form_def.html", map[string]any{
		"Form":        form,
		"Steps":       steps,
		"HasArchived": hasArchived,
		"HistPoints":  histPoints,
		"CSRFToken":   h.csrfToken(w, r),
		"ActiveTab":   "records",
		"TestMode":    h.cfg().TestMode,
	})
}

// FormDefHistory â€" GET /api/forms/{id}/def/history?at=<RFC3339 timestamp>
// Returns the form definition state at that timestamp as JSON.
// Changed rows are those with a history entry at exactly that timestamp (pre-change values).
func (h *Handler) FormDefHistory(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	// `at` is a calendar day in the viewing user's timezone (#847); changed_at is a
	// timestamptz assigned by the database (#192), so resolve the day to a half-open UTC range in Go rather
	// than CAST(changed_at AS DATE), which would take the UTC day.
	atStr := r.URL.Query().Get("at")
	loc := h.userLocation(r)
	dayStart, err := time.ParseInLocation("2006-01-02", atStr, loc)
	if err != nil {
		http.Error(w, "bad at param", http.StatusBadRequest)
		return
	}
	dayEnd := dayStart.AddDate(0, 0, 1)

	type stepState struct {
		ID            int    `json:"id"`
		Type          int    `json:"type"`
		Parameter     string `json:"parameter"`
		SpecNom       string `json:"spec_nom"`
		SpecMin       string `json:"spec_min"`
		SpecMax       string `json:"spec_max"`
		SpecUnits     string `json:"spec_units"`
		PFType        string `json:"pf_type"`
		DefaultResult string `json:"default_result"`
		HideFormula   string `json:"hide_formula"`
		Changed       bool   `json:"changed"`
	}

	rows, err := h.records().ListFormStepsAt(r.Context(), formID, dayStart.UTC(), dayEnd.UTC())
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	var steps []stepState
	for _, s := range rows {
		steps = append(steps, stepState{ID: s.ID, Type: s.Type, Parameter: s.Parameter, SpecNom: s.SpecNom,
			SpecMin: s.SpecMin, SpecMax: s.SpecMax, SpecUnits: s.SpecUnits, PFType: s.PfType,
			DefaultResult: s.DefaultResult, HideFormula: s.HideFormula, Changed: s.Changed})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"steps": steps})
}

// EditFormDef â€" GET /forms/{id}/def/edit
func (h *Handler) EditFormDef(w http.ResponseWriter, r *http.Request) {
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

	// Load raw step values â€" no substituteRefs, we want to edit the actual stored values.
	stepsMap, err := h.loadSteps(r.Context(), h.records(), formID)
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	ids := form.OrderedTestIDs()
	var steps []*models.TestStep
	hasArchived := false
	for _, tid := range ids {
		if s, ok := stepsMap[tid]; ok {
			if s.Archived {
				hasArchived = true
			}
			steps = append(steps, s)
		}
	}

	namedQueries, err := h.listNamedQueries(r.Context())
	if err != nil {
		log.Printf("warning: could not load named queries for def editor: %v", err)
	}

	h.renderRecords(w, r, "form_def_edit.html", map[string]any{
		"Form":         form,
		"Steps":        steps,
		"HasArchived":  hasArchived,
		"NamedQueries": namedQueries,
		"CSRFToken":    h.csrfToken(w, r),
		"ActiveTab":    "records",
		"TestMode":     h.cfg().TestMode,
	})
}

// SaveFormDef â€" POST /forms/{id}/def/edit
func (h *Handler) SaveFormDef(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	// Change detection uses data-original values submitted by the form.
	// This correctly handles concurrency: if person B changed a field A didn't touch,
	// A's submitted value == A's original â†' skip â†' B's change is preserved.
	// No SELECT needed â€" eliminates a DB round trip.
	sid := func(id int, field string) string {
		return r.FormValue(fmt.Sprintf("%s_%d", field, id))
	}
	orig := func(id int, field string) string {
		return r.FormValue(fmt.Sprintf("original_%s_%d", field, id))
	}

	// Collect step IDs from submitted original_ fields.
	stepIDs := map[int]bool{}
	for key := range r.Form {
		if after, ok := strings.CutPrefix(key, "original_parameter_"); ok {
			if id, err := strconv.Atoi(after); err == nil {
				stepIDs[id] = true
			}
		}
	}

	// Open a transaction so the transaction-local arx.username GUC is seen by the
	// trg_form_row_history trigger on every UPDATE in this batch.
	tx, err := h.beginTx(r.Context())
	if err != nil {
		serverError(w, "could not start transaction", err)
		return
	}
	defer tx.Rollback()
	svc := records.New(tx)

	if u := h.currentUser(r); u != nil {
		svc.SetAuditUser(r.Context(), u.Username) // best-effort, as before: a failure only leaves history unattributed
	}

	for id := range stepIDs {
		stepType := 0
		if t, err := strconv.Atoi(sid(id, "type")); err == nil {
			stepType = t
		}
		origType := 0
		if t, err := strconv.Atoi(orig(id, "type")); err == nil {
			origType = t
		}
		hideFormula := strings.TrimSpace(r.FormValue(fmt.Sprintf("hide_%d", id)))

		// Skip if nothing changed relative to what the user saw when the page loaded.
		if stepType == origType &&
			sid(id, "parameter") == orig(id, "parameter") &&
			sid(id, "specification") == orig(id, "specification") &&
			sid(id, "spec_nom") == orig(id, "spec_nom") &&
			sid(id, "spec_min") == orig(id, "spec_min") &&
			sid(id, "spec_max") == orig(id, "spec_max") &&
			sid(id, "spec_units") == orig(id, "spec_units") &&
			sid(id, "pf_type") == orig(id, "pf_type") &&
			sid(id, "default_result") == orig(id, "default_result") &&
			hideFormula == orig(id, "hide") &&
			sid(id, "category") == orig(id, "category") &&
			sid(id, "sheet_name") == orig(id, "sheet_name") &&
			sid(id, "instrument_types") == orig(id, "instrument_types") &&
			sid(id, "format") == orig(id, "format") &&
			sid(id, "comment") == orig(id, "comment") {
			continue
		}

		if err := svc.UpdateStep(r.Context(), formID, id, records.StepDef{
			Type: stepType, Parameter: sid(id, "parameter"), Specification: sid(id, "specification"),
			SpecNom: sid(id, "spec_nom"), SpecMin: sid(id, "spec_min"), SpecMax: sid(id, "spec_max"),
			SpecUnits: sid(id, "spec_units"), PFType: sid(id, "pf_type"), DefaultResult: sid(id, "default_result"),
			HideFormula: hideFormula, Category: sid(id, "category"), SheetName: sid(id, "sheet_name"),
			InstrumentTypes: sid(id, "instrument_types"), Format: sid(id, "format"), Comment: sid(id, "comment"),
		}); err != nil {
			serverError(w, "could not save step", err)
			return
		}
	}

	// Parse and INSERT new rows submitted via new_row[IDX][field] inputs.
	type newRowData struct {
		Type            string
		Parameter       string
		Specification   string
		SpecNom         string
		SpecMin         string
		SpecMax         string
		SpecUnits       string
		PFType          string
		DefaultResult   string
		Hide            string
		Category        string
		SheetName       string
		InstrumentTypes string
		Format          string
		Comment         string
	}
	parsedNewRows := map[string]newRowData{}
	for key, vals := range r.Form {
		if !strings.HasPrefix(key, "new_row[") {
			continue
		}
		rest := key[len("new_row["):]
		before, after, ok := strings.Cut(rest, "][")
		if !ok {
			continue
		}
		idx := before
		field := strings.TrimSuffix(after, "]")
		val := ""
		if len(vals) > 0 {
			val = strings.TrimSpace(vals[0])
		}
		row := parsedNewRows[idx]
		switch field {
		case "type":
			row.Type = val
		case "parameter":
			row.Parameter = val
		case "specification":
			row.Specification = val
		case "spec_nom":
			row.SpecNom = val
		case "spec_min":
			row.SpecMin = val
		case "spec_max":
			row.SpecMax = val
		case "spec_units":
			row.SpecUnits = val
		case "pf_type":
			row.PFType = val
		case "default_result":
			row.DefaultResult = val
		case "hide":
			row.Hide = val
		case "category":
			row.Category = val
		case "sheet_name":
			row.SheetName = val
		case "instrument_types":
			row.InstrumentTypes = val
		case "format":
			row.Format = val
		case "comment":
			row.Comment = val
		}
		parsedNewRows[idx] = row
	}

	// INSERT each non-blank new row; build map of client idx → real DB id.
	newIDMap := map[string]int{}
	for idx, row := range parsedNewRows {
		stepType := 0
		if t, err2 := strconv.Atoi(row.Type); err2 == nil {
			stepType = t
		}
		// Skip blank data rows (headings are kept even with no parameter).
		if stepType == 0 && row.Parameter == "" {
			continue
		}
		newID, err2 := svc.InsertStep(r.Context(), formID, records.StepDef{
			Type: stepType, Parameter: row.Parameter, Specification: row.Specification,
			SpecNom: row.SpecNom, SpecMin: row.SpecMin, SpecMax: row.SpecMax, SpecUnits: row.SpecUnits,
			PFType: row.PFType, DefaultResult: row.DefaultResult, HideFormula: row.Hide, Category: row.Category,
			SheetName: row.SheetName, InstrumentTypes: row.InstrumentTypes, Format: row.Format, Comment: row.Comment,
		})
		if err2 != nil {
			http.Error(w, "could not insert new step: "+err2.Error(), http.StatusInternalServerError)
			return
		}
		newIDMap[idx] = newID
	}

	// Update Forms.test_order if the order changed or new rows were added (same tx: new steps
	// and the order that shows them commit together).
	normalizeOrder := func(s string) string {
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return strings.Join(out, ",")
	}

	stepOrder := strings.TrimSpace(r.FormValue("step_order"))
	if stepOrder != "" || len(newIDMap) > 0 {
		// Substitute new_X tokens with real IDs; drop tokens for skipped (blank) rows.
		if stepOrder != "" {
			parts := strings.Split(stepOrder, ",")
			kept := make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if after, ok := strings.CutPrefix(p, "new_"); ok {
					idx := after
					if realID, ok := newIDMap[idx]; ok {
						kept = append(kept, strconv.Itoa(realID))
					}
					// else: blank row was skipped — omit from order
				} else if p != "" {
					kept = append(kept, p)
				}
			}
			stepOrder = strings.Join(kept, ",")
		}

		// Append any inserted rows whose new_X token wasn't in step_order.
		inOrder := map[string]bool{}
		for p := range strings.SplitSeq(stepOrder, ",") {
			inOrder[strings.TrimSpace(p)] = true
		}
		for _, realID := range newIDMap {
			if !inOrder[strconv.Itoa(realID)] {
				if stepOrder != "" {
					stepOrder += ","
				}
				stepOrder += strconv.Itoa(realID)
			}
		}

		current, err := svc.GetFormHeader(r.Context(), formID)
		if err != nil && err != sql.ErrNoRows { // a missing form reads as an empty order
			serverError(w, "could not read step order", err)
			return
		}
		if normalizeOrder(stepOrder) != normalizeOrder(current.TestOrder) {
			if err := svc.SetFormTestOrder(r.Context(), formID, normalizeOrder(stepOrder)); err != nil {
				serverError(w, "could not save step order", err)
				return
			}
		}
	}

	// Update form-level record_types and instrument_types if changed.
	newRecordTypes := strings.TrimSpace(r.FormValue("record_types"))
	newInstrTypes := strings.TrimSpace(r.FormValue("instrument_types"))
	if newRecordTypes != r.FormValue("original_record_types") ||
		newInstrTypes != r.FormValue("original_instrument_types") {
		if err := svc.SetFormTypes(r.Context(), formID, newRecordTypes, newInstrTypes); err != nil {
			serverError(w, "could not save record types", err)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		serverError(w, "could not save steps", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/def", formID), http.StatusSeeOther)
}

// loadSteps loads all form_row rows for a form keyed by id, with every field used for
// rendering and token resolution. Used as the live-definition fallback for un-materialized rows.
// svc is h.records(), or a Service over the caller's tx when the read belongs to a write.
func (h *Handler) loadSteps(ctx context.Context, svc *records.Service, formID int) (map[int]*models.TestStep, error) {
	rows, err := svc.ListFormSteps(ctx, formID)
	if err != nil {
		return nil, err
	}
	steps := map[int]*models.TestStep{}
	for _, r := range rows {
		s := models.TestStep{
			ID: r.ID, FormID: r.FormID, Parameter: r.Parameter, Specification: r.Specification,
			DefaultResult: r.DefaultResult, HideFormula: r.HideFormula, Type: r.Type, Archived: r.Archived,
			SpecMin: r.SpecMin, SpecMax: r.SpecMax, PFType: r.PfType, Category: r.Category,
			SheetName: r.SheetName, SpecUnits: r.SpecUnits, SpecNom: r.SpecNom,
			InstrumentTypes: r.InstrumentTypes, Format: r.Format, StepComment: r.Comment,
			StepCreatedAt: r.CreatedAt, StepUpdatedAt: r.UpdatedAt,
		}
		steps[s.ID] = &s
	}
	return steps, nil
}

// loadRecordResults loads the materialized snapshot rows for a record, keyed by form_row_id (#487).
func (h *Handler) loadRecordResults(ctx context.Context, recordID int) (map[int]*models.TestResult, error) {
	rows, err := h.records().ListResults(ctx, recordID)
	if err != nil {
		return nil, err
	}
	results := map[int]*models.TestResult{}
	for _, r := range rows {
		res := models.TestResult{
			ID: r.ID, RecordID: r.FormRecordID, TestID: r.FormRowID, Parameter: r.Parameter,
			Specification: r.Specification, Result: r.Result, Comment: r.Comment,
			SpecMin: r.SpecMin, SpecNom: r.SpecNom, SpecMax: r.SpecMax, SpecUnits: r.SpecUnits,
			PFType: r.PfType, Format: r.Format, Type: r.Type, HideFormula: r.HideFormula,
			DefaultResult: r.DefaultResult, UpdatedAt: r.UpdatedAt,
		}
		if r.PassFail.Valid {
			res.PassFail = &r.PassFail.Bool
		}
		results[res.TestID] = &res
	}
	return results, nil
}

// loadFrozenRows builds the ordered, frozen rows for a saved record. Content comes from the
// materialized snapshot; steps with no snapshot row (headings/unrecorded rows on a pre-#487 record)
// fall back to the live definition, baked the same way. Visibility (hide_formula, archived, instrument
// type) is applied. {id} cross-step tokens are LEFT UNRESOLVED so the caller resolves them against the
// record's own results (view) or captures Raw* fields first (edit). Returns the rows, the record's
// result map, and the synthetic step map used for {id} spec_nom fallback.
// includeHidden=true keeps steps whose hide_formula currently evaluates to hidden, flagging them
// ResultRow.Hidden instead of dropping them. The edit view passes true so client-side JS can toggle
// visibility live as results change; the read-only/print views pass false.
func (h *Handler) loadFrozenRows(ctx context.Context, record *models.TestRecord, form *models.TestForm, includeHidden bool) ([]models.ResultRow, map[int]*models.TestResult, map[int]*models.TestStep, error) {
	results, err := h.loadRecordResults(ctx, record.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	liveSteps, err := h.loadSteps(ctx, h.records(), form.ID)
	if err != nil {
		return nil, nil, nil, err
	}

	// Synthetic step map for {id} spec_nom fallback: prefer the frozen snapshot nominal.
	refSteps := map[int]*models.TestStep{}
	maps.Copy(refSteps, liveSteps)
	for tid, res := range results {
		refSteps[tid] = &models.TestStep{ID: tid, SpecNom: res.SpecNom}
	}

	ids := record.OrderedTestIDs()
	if len(ids) == 0 {
		ids = form.OrderedTestIDs()
	}

	var rows []models.ResultRow
	for _, tid := range ids {
		if res, ok := results[tid]; ok {
			// Frozen snapshot row — visibility from the frozen hide formula against own results.
			hidden := evaluateHide(res.HideFormula, results, refSteps, record, form)
			if hidden && !includeHidden {
				continue
			}
			rows = append(rows, models.ResultRow{Step: stepFromResult(tid, res), Result: res, Level: res.Type, Hidden: hidden})
			continue
		}
		// Legacy fallback: render from the live definition (un-materialized row on an old record).
		step, ok := liveSteps[tid]
		if !ok {
			continue
		}
		if !stepVisibleOnRecord(step.Archived, false) {
			continue
		}
		hidden := evaluateHide(step.HideFormula, results, refSteps, record, form)
		if hidden && !includeHidden {
			continue
		}
		if !stepAppliesToRecord(step.InstrumentTypes, record.InstrumentType) {
			continue
		}
		bakeStepTokens(step, record, form)
		rows = append(rows, models.ResultRow{Step: step, Result: nil, Level: step.Type, Hidden: hidden})
	}
	return rows, results, refSteps, nil
}

// RecordDetail â€" GET /records/{id}
func (h *Handler) RecordDetail(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	rec, err := h.records().GetRecord(r.Context(), recordID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	record := testRecord(rec)

	hdr, err := h.records().GetFormHeader(r.Context(), record.FormID)
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	form := testForm(hdr)

	// Build the frozen rows from the materialized snapshot (live def is only a legacy fallback).
	resultRows, results, refSteps, err := h.loadFrozenRows(r.Context(), &record, &form, false)
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	// Resolve {id} cross-step tokens against the record's own results (frozen). The read-only view
	// shows only what was recorded: drop the frozen default and present an unfilled row as untouched.
	for i := range resultRows {
		row := &resultRows[i]
		if row.Level > 0 || row.Step == nil {
			continue
		}
		resolveStepRefs(row.Step, results, refSteps, &record, &form)
		row.Step.DefaultResult = ""
		if row.Result != nil && row.Result.Result == "" && row.Result.Comment == "" {
			row.Result = nil
		}
	}

	// Prev/next record IDs within this form, same ordering as the records list (0 when the
	// record isn't an active record of it).
	prevID, nextID, _ := h.records().Neighbors(r.Context(), record.FormID, recordID)

	var imageRows []models.ResultRow
	for _, row := range resultRows {
		if row.Level == 0 && isImageRow(row) {
			imageRows = append(imageRows, row)
		}
	}

	// Lifecycle audit trail (#250) — complete/approve/unlock events, oldest first.
	var events []models.RecordEvent
	eventRows, err := h.records().ListEvents(r.Context(), recordID)
	if err != nil {
		log.Printf("RecordDetail: audit trail query error: %v", err)
	}
	for _, e := range eventRows {
		events = append(events, models.RecordEvent{ID: e.ID, TestRecordID: e.FormRecordID, EventType: e.EventType,
			Username: e.Username, EventDate: &e.EventDate, Comments: e.Comments})
	}

	// Per-lock result snapshots (#251), keyed by event_id and diffed against the prior snapshot.
	snapshots, err := h.loadEventSnapshots(r.Context(), recordID)
	if err != nil {
		log.Printf("RecordDetail: event snapshots error: %v", err)
	}

	canApproveRecords := false
	if u := h.currentUser(r); u != nil {
		canApproveRecords = u.CanApproveRecords
	}

	trace, err := h.loadRecordTrace(r.Context(), &record, false)
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	h.renderRecords(w, r, "records_show.html", map[string]any{
		"Form":              form,
		"Record":            record,
		"Rows":              resultRows,
		"ImageRows":         imageRows,
		"Events":            events,
		"Snapshots":         snapshots,
		"Trace":             trace,
		"CanApproveRecords": canApproveRecords,
		"PrevID":            prevID,
		"NextID":            nextID,
		"CSRFToken":         h.csrfToken(w, r),
		"ActiveTab":         "records",
		"TestMode":          h.cfg().TestMode,
		"DebugMode":         h.cfg().DebugMode,
	})
}

// RecordPrint — GET /records/{id}/print
func (h *Handler) RecordPrint(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	rec, err := h.records().GetRecord(r.Context(), recordID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	record := testRecord(rec)

	hdr, err := h.records().GetFormHeader(r.Context(), record.FormID)
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	form := testForm(hdr)

	// Build the frozen rows from the materialized snapshot (live def is only a legacy fallback).
	resultRows, results, refSteps, err := h.loadFrozenRows(r.Context(), &record, &form, false)
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	// Resolve {id} cross-step tokens against the record's own results (frozen). The read-only view
	// shows only what was recorded: drop the frozen default and present an unfilled row as untouched.
	for i := range resultRows {
		row := &resultRows[i]
		if row.Level > 0 || row.Step == nil {
			continue
		}
		resolveStepRefs(row.Step, results, refSteps, &record, &form)
		row.Step.DefaultResult = ""
		if row.Result != nil && row.Result.Result == "" && row.Result.Comment == "" {
			row.Result = nil
		}
	}

	var imageRows []models.ResultRow
	for _, row := range resultRows {
		if row.Level == 0 && isImageRow(row) {
			imageRows = append(imageRows, row)
		}
	}

	h.renderPrintRecords(w, "record_print.html", map[string]any{
		"Form":      form,
		"Record":    record,
		"Rows":      resultRows,
		"ImageRows": imageRows,
		"ActiveTab": "records",
		"TestMode":  h.cfg().TestMode,
	})
}

// BOMPart is one row from the form's BOM (PL → PN join).
type BOMPart struct {
	PartNumberID int
	PartNumber   string
	Description  string
}

// NewRecord — GET /forms/{id}/records/new
func (h *Handler) NewRecord(w http.ResponseWriter, r *http.Request) {
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

	// BOM lookup: parts listed under the form's own part number in PL.
	var bomParts []BOMPart
	if bomRows, err := h.records().ListBOMParts(r.Context(), form.PartNumberID); err == nil {
		for _, p := range bomRows {
			bomParts = append(bomParts, BOMPart{PartNumberID: p.ID, PartNumber: p.PartNumber, Description: p.Description})
		}
	}

	// Next serial number: max numeric SN + 1, defaulting to 1 if none exist.
	// This is only a suggestion shown in the form; CreateRecord re-derives the SN
	// atomically under a lock when the user accepts it, closing the concurrent-create race (#369).
	nextSNStr := "1"
	if nextSN, err := h.records().NextSerial(r.Context(), formID); err == nil {
		nextSNStr = strconv.Itoa(nextSN)
	}

	h.renderRecords(w, r, "record_new.html", map[string]any{
		"Form":      form,
		"BOMParts":  bomParts,
		"NextSN":    nextSNStr,
		"Today":     h.userNow(r).Format("2006-01-02T15:04"),
		"CSRFToken": h.csrfToken(w, r),
		"ActiveTab": "records",
		"TestMode":  h.cfg().TestMode,
	})
}

// CreateRecord — POST /forms/{id}/records/new
func (h *Handler) CreateRecord(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
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

	serialNumber := strings.TrimSpace(r.FormValue("serial_number"))
	suggestedSN := strings.TrimSpace(r.FormValue("suggested_serial_number"))
	recordType := strings.TrimSpace(r.FormValue("record_type"))
	instrumentType := strings.TrimSpace(r.FormValue("instrument_type"))

	// Resolve the selected BOM part into denormalized PN fields.
	var snPN, snDesc string
	var partNumberID *int
	if pnidStr := r.FormValue("bom_pnid"); pnidStr != "" {
		if pnid, convErr := strconv.Atoi(pnidStr); convErr == nil {
			pn, description, lookupErr := h.records().GetPartLabel(r.Context(), pnid)
			switch {
			case lookupErr == nil:
				snPN = pn
				snDesc = description
				partNumberID = &pnid
			case lookupErr != sql.ErrNoRows: // an unknown part id just leaves the subject blank
				serverError(w, "query error", lookupErr)
				return
			}
		}
	}

	recordDate := h.userNow(r)
	if rdStr := r.FormValue("record_date"); rdStr != "" {
		if rd, parseErr := time.Parse("2006-01-02T15:04", rdStr); parseErr == nil {
			recordDate = rd
		} else if rd, parseErr := time.Parse("2006-01-02", rdStr); parseErr == nil {
			recordDate = rd
		}
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		serverError(w, "could not start transaction", err)
		return
	}
	defer tx.Rollback()
	svc := records.New(tx)

	// SN allocation is atomic when the user accepted the suggested default (#369).
	// Re-derive the next per-form SN inside the transaction under a lock so two
	// concurrent creates for the same form get N and N+1, not the same value.
	// A user-typed override (serialNumber != suggestedSN) is inserted as-is.
	if isAutoSerial(serialNumber, suggestedSN) {
		// Serialize concurrent auto-allocations for this form: the xact-scoped
		// advisory lock is held until tx commit/rollback, so the MAX()+1 read and
		// the INSERT below are atomic w.r.t. other creates on the same form.
		if err = svc.LockSerialAllocation(r.Context(), serialAllocLockNS, formID); err != nil {
			serverError(w, "serial lock error", err)
			return
		}
		nextSN, err := svc.NextSerial(r.Context(), formID)
		if err != nil {
			serverError(w, "serial number error", err)
			return
		}
		serialNumber = strconv.Itoa(nextSN)
	}

	newID, err := svc.InsertRecord(r.Context(), records.NewRecord{
		FormID: formID, PartID: partNumberID, SerialNumber: serialNumber, SubjectPartNumber: snPN,
		SubjectPnDescription: snDesc, RecordType: recordType, InstrumentType: instrumentType,
		TestOrder: form.TestOrder, RecordDate: recordDate, FormRevision: form.Revision,
	})
	if err != nil {
		serverError(w, "insert error", err)
		return
	}

	// Freeze the record from the start: materialize a snapshot row per applicable step (#487).
	rec := models.TestRecord{
		ID: newID, FormID: formID, SerialNumber: serialNumber,
		SerialNumberPN: snPN, SerialNumberDesc: snDesc, RecordType: recordType,
		InstrumentType: instrumentType, RecordDate: &recordDate, TestOrder: form.TestOrder,
	}
	if err := h.materializeRecordSteps(r.Context(), svc, &rec, &form); err != nil {
		serverError(w, "materialize error", err)
		return
	}

	if err := tx.Commit(); err != nil {
		serverError(w, "commit error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d/edit", newID), http.StatusSeeOther)
}

// materializeRecordSteps creates a frozen result snapshot row for every applicable step in the
// form's order at record creation (#487): headings included (type > 0); archived and non-applicable
// instrument-type data steps skipped. Self/record/form tokens are baked; {id} cross-step tokens are
// left for render-time resolution. result/comment/pass_fail start empty. svc runs on the caller's tx,
// the step read included.
func (h *Handler) materializeRecordSteps(ctx context.Context, svc *records.Service, record *models.TestRecord, form *models.TestForm) error {
	steps, err := h.loadSteps(ctx, svc, form.ID)
	if err != nil {
		return err
	}
	ids := record.OrderedTestIDs()
	if len(ids) == 0 {
		ids = form.OrderedTestIDs()
	}
	for _, tid := range ids {
		step, ok := steps[tid]
		if !ok {
			continue
		}
		if step.Archived {
			continue
		}
		if step.Type == 0 && !stepAppliesToRecord(step.InstrumentTypes, record.InstrumentType) {
			continue
		}
		bakeStepTokens(step, record, form)
		if err := svc.InsertResult(ctx, record.ID, tid, resultDef(step), nil, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// resultDef is the (baked) step definition a result row freezes.
func resultDef(s *models.TestStep) records.ResultDef {
	return records.ResultDef{Type: s.Type, Parameter: s.Parameter, Specification: s.Specification, SpecMin: s.SpecMin,
		SpecNom: s.SpecNom, SpecMax: s.SpecMax, SpecUnits: s.SpecUnits, PFType: s.PFType, Format: s.Format,
		HideFormula: s.HideFormula, DefaultResult: s.DefaultResult}
}

// RecordTrace is the lot/build linkage panel for a test record (#677): the currently
// linked lot/build, and — when editable — the pickable options plus whether the part
// can be built. Nil when the record has no resolved part.
type RecordTrace struct {
	PartID        int
	IsLotTracked  bool
	TracksSerials bool             // the record's part is serial/lot_serial → tested records mint a unit (#745)
	Buildable     bool             // the record's part has a BOM → offer "Build this unit"
	LinkedLot     *LotRow          // currently linked lot, nil if none
	LinkedBuild   *BuildOption     // currently linked build, nil if none
	SelectedLot   int              // record.LotID (0 = none) — for the picker's selected option
	SelectedBuild int              // record.BuildID (0 = none)
	Lots          []LotOption      // active lots to pick (editable view only)
	Builds        []BuildOption    // builds to pick (editable view only)
	Components    []buildComponent // BOM lines for the inline build-at-test-time panel (#747; editable + Buildable)
}

// loadRecordTrace builds the traceability view for a record's tested part (#677).
// Returns nil (no error) when the record has no resolved part. When editable, it also
// loads the pickable lot/build options for the edit form.
func (h *Handler) loadRecordTrace(ctx context.Context, record *models.TestRecord, editable bool) (*RecordTrace, error) {
	if record.PartNumberID == 0 {
		return nil, nil
	}
	t := &RecordTrace{PartID: record.PartNumberID}
	// A unit-level record carries its provenance on the unit, not the form_record
	// (#745, Q8): lot_id/build_id are left NULL and read through the unit. Resolve the
	// effective lot/build here so the picker shows them selected and re-submits them —
	// without this, re-saving a serial record would clear its unit link.
	lotID, buildID := record.LotID, record.BuildID
	if record.UnitID != nil {
		ulot, ubuild, err := h.inventory().UnitProvenance(ctx, *record.UnitID)
		if err != nil {
			return nil, err
		}
		if ulot != nil {
			lotID = ulot
		}
		if ubuild != nil {
			buildID = ubuild
		}
	}
	if lotID != nil {
		t.SelectedLot = *lotID
	}
	if buildID != nil {
		t.SelectedBuild = *buildID
	}

	// The tested part's lot-tracking + whether it has a BOM (is buildable).
	// part_id is a logical reference with no FK, so a stale id may not resolve —
	// treat that as simply having no trace rather than failing the whole page.
	tracking, err := h.records().GetPartTracking(ctx, record.PartNumberID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.IsLotTracked = models.TracksLots(tracking.TrackingMode)
	t.TracksSerials = models.TracksSerials(tracking.TrackingMode)
	t.Buildable = tracking.BomCount > 0

	if lotID != nil {
		if lr, found, err := h.fetchLotRow(ctx, *lotID); err != nil {
			return nil, err
		} else if found {
			t.LinkedLot = &lr
		}
	}
	if buildID != nil {
		b, err := h.fetchBuildOption(ctx, *buildID)
		if err != nil {
			return nil, err
		}
		t.LinkedBuild = b
	}

	if editable {
		if t.IsLotTracked {
			lots, err := h.activeLotsForPart(ctx, record.PartNumberID)
			if err != nil {
				return nil, err
			}
			// The currently-linked lot may since have been deactivated; keep it in the
			// options (it's the selected one) so saving the form doesn't silently clear
			// a still-valid link the user never touched.
			if t.SelectedLot != 0 && t.LinkedLot != nil && !containsLotOption(lots, t.SelectedLot) {
				lots = append(lots, LotOption{ID: t.LinkedLot.ID, Label: t.LinkedLot.LotNumber + " (inactive)"})
			}
			t.Lots = lots
		}
		builds, err := h.activeBuildsForPart(ctx, record.PartNumberID)
		if err != nil {
			return nil, err
		}
		t.Builds = builds
		// #747: BOM components for the inline "build this unit" panel — only when the
		// part is actually buildable (has a BOM).
		if t.Buildable {
			comps, err := h.loadBuildComponents(ctx, record.PartNumberID)
			if err != nil {
				return nil, err
			}
			t.Components = comps
		}
	}
	return t, nil
}

// containsLotOption reports whether lots already includes the lot with id lotID.
func containsLotOption(lots []LotOption, lotID int) bool {
	for _, o := range lots {
		if o.ID == lotID {
			return true
		}
	}
	return false
}

// recordLinkageArgs reads and validates the lot_id/build_id form fields for a record
// whose tested part is partID (#677), for saving alongside the record's other metadata.
// Each returned id is the chosen one, or nil to clear the link when the field is
// blank. A non-blank id that doesn't belong to the part yields an error (surfaced as a
// 400). Unlike the build's component-lot check, a lot need not be active here — a record
// may legitimately reference a since-retired lot.
func (h *Handler) recordLinkageArgs(r *http.Request, partID int) (lotArg, buildArg *int, err error) {
	ctx := r.Context()
	inv := h.inventory()
	belongs := func(owns func(context.Context, int, int) (bool, error), v string) (*int, error) {
		id, convErr := strconv.Atoi(v)
		if convErr != nil || id <= 0 {
			return nil, fmt.Errorf("invalid selection")
		}
		ok, e := owns(ctx, id, partID)
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, fmt.Errorf("selection does not belong to this record's part")
		}
		return &id, nil
	}
	if v := fv(r, "lot_id"); v != "" {
		if lotArg, err = belongs(inv.PartHasLot, v); err != nil {
			return nil, nil, err
		}
	}
	if v := fv(r, "build_id"); v != "" {
		if buildArg, err = belongs(inv.PartHasBuild, v); err != nil {
			return nil, nil, err
		}
	}
	return lotArg, buildArg, nil
}

// EditRecord — GET /records/{id}/edit
func (h *Handler) EditRecord(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	rec, err := h.records().GetRecord(r.Context(), recordID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	record := testRecord(rec)
	if record.IsLocked {
		http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
		return
	}

	hdr, err := h.records().GetFormHeader(r.Context(), record.FormID)
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	form := testForm(hdr)

	// Build the frozen rows from the materialized snapshot (live def is only a legacy fallback).
	// Edit always works against the frozen spec; "Update to latest" is the only re-pull path (#487).
	// includeHidden=true: render conditionally-hidden steps (display:none) so JS can toggle them live (#257).
	resultRows, results, refSteps, err := h.loadFrozenRows(r.Context(), &record, &form, true)
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	namedQueryDescs := map[string]string{}
	if nqs, err := h.listNamedQueries(r.Context()); err == nil {
		for _, nq := range nqs {
			namedQueryDescs[nq.Name] = nq.Description
		}
	}

	for i := range resultRows {
		row := &resultRows[i]
		if row.Level > 0 || row.Step == nil {
			continue
		}
		// Rows come back baked (self/record/form resolved) with {id} intact — exactly the
		// "raw" state the edit JS re-resolves live as cross-step results change.
		if strings.HasPrefix(row.Step.SpecNom, "query:") {
			row.RawSpecNom = row.Step.SpecNom
			name, _ := parseQuerySpec(row.Step.SpecNom)
			row.QueryDescription = namedQueryDescs[name]
		}
		if row.Step.DefaultResult != "" {
			row.RawDefault = row.Step.DefaultResult
		}
		// Resolve {id} cross-step tokens against the record's own results (frozen).
		resolveStepRefs(row.Step, results, refSteps, &record, &form)
	}

	trace, err := h.loadRecordTrace(r.Context(), &record, true)
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	h.renderRecords(w, r, "record_edit.html", map[string]any{
		"Form":                form,
		"Record":              record,
		"Rows":                resultRows,
		"Trace":               trace,
		"CSRFToken":           h.csrfToken(w, r),
		"ActiveTab":           "records",
		"TestMode":            h.cfg().TestMode,
		"ImageRootConfigured": h.cfg().ImageRoot != "",
	})
}

// LockRecord — POST /records/{id}/lock
// Sets locked=1 on the record and writes a 'locked' event to record_events.
func (h *Handler) LockRecord(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	username := ""
	if u := h.currentUser(r); u != nil {
		username = u.Username
	}

	// Lock and, on transition, log the 'completed' event + result snapshot (#251).
	if _, err := h.completeRecordTx(r.Context(), recordID, 0, username); err != nil {
		if errors.Is(err, errRecordNeedsLot) {
			http.Error(w, errRecordNeedsLot.Error(), http.StatusBadRequest)
			return
		}
		serverError(w, "lock error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
}

// ApproveRecord — POST /records/{id}/approve
// Reviewer sign-off (#249). Requires the can_approve_records permission and a Complete
// record (is_locked=1, is_approved=0). Sets is_approved=1 and logs an 'approved' event.
func (h *Handler) ApproveRecord(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	u := h.currentUser(r)
	if u == nil || !u.CanApproveRecords {
		http.Error(w, "you do not have permission to approve records", http.StatusForbidden)
		return
	}

	if err := h.recordsTx(r.Context(), func(svc *records.Service) error {
		approved, err := svc.ApproveRecord(r.Context(), recordID)
		if err != nil || !approved {
			return err
		}
		_, err = svc.InsertRecordEvent(r.Context(), recordID, "approved", u.Username, "")
		return err
	}); err != nil {
		serverError(w, "approve error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
}

// UnlockRecord — POST /records/{id}/unlock
// Requires a comment, returns the record to WIP, and writes an 'unlocked' event. Unlocking an
// approved record requires the can_approve_records permission (#249).
func (h *Handler) UnlockRecord(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	comment := strings.TrimSpace(r.FormValue("comment"))
	if comment == "" {
		http.Error(w, "a comment is required to unlock a record", http.StatusBadRequest)
		return
	}

	// Approved records may only be unlocked by a TR reviewer. This read gives the 403; the UPDATE
	// re-checks under the row lock, so an approval that lands in between still wins.
	rec, err := h.records().GetRecord(r.Context(), recordID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, "unlock error", err)
		return
	}
	u := h.currentUser(r)
	reviewer := u != nil && u.CanApproveRecords
	if rec.IsApproved && !reviewer {
		http.Error(w, "only a TR reviewer can unlock an approved record", http.StatusForbidden)
		return
	}

	username := ""
	if u != nil {
		username = u.Username
	}

	if err := h.recordsTx(r.Context(), func(svc *records.Service) error {
		unlocked, err := svc.UnlockRecord(r.Context(), recordID, reviewer)
		if err != nil || !unlocked {
			return err
		}
		_, err = svc.InsertRecordEvent(r.Context(), recordID, "unlocked", username, comment)
		return err
	}); err != nil {
		serverError(w, "unlock error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
}

// BulkLockRecords — POST /forms/{id}/records/bulk-lock
// Marks multiple WIP records as Complete (is_locked=1) and logs a 'completed' event per record.
// The form_id guard in the UPDATE ensures records belong to this form.
func (h *Handler) BulkLockRecords(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	username := ""
	if u := h.currentUser(r); u != nil {
		username = u.Username
	}

	var locked int
	for _, raw := range r.Form["record_ids[]"] {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			continue
		}
		if ok, err := h.completeRecordTx(r.Context(), id, formID, username); err == nil && ok {
			locked++
		}
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/records?locked=%d", formID, locked), http.StatusSeeOther)
}

// DuplicateRecord — POST /records/{id}/duplicate
// Creates a new WIP record with the same serial number as the source, copying all result rows.
// Redirects to the new record's edit view on success.
func (h *Handler) DuplicateRecord(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		serverError(w, "could not start transaction", err)
		return
	}
	defer tx.Rollback()
	svc := records.New(tx)

	// A duplicate is a fresh re-test: dated now and capturing the CURRENT form revision, not the
	// source's frozen one (#260). unit_id is carried over unchanged: a retest points at the SAME
	// serialized unit as the source record (#745, design §2/§5), never a new unit row.
	newID, err := svc.DuplicateRecord(r.Context(), recordID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, "insert error", err)
		return
	}
	if err := svc.CopyResults(r.Context(), recordID, newID); err != nil {
		serverError(w, "insert error", err)
		return
	}

	if err := tx.Commit(); err != nil {
		serverError(w, "commit error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d/edit", newID), http.StatusSeeOther)
}

// LockForm — POST /forms/{id}/lock
// Sets locked=1 on the form and writes a 'locked' event to form_events.
func (h *Handler) LockForm(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	username := ""
	if u := h.currentUser(r); u != nil {
		username = u.Username
	}

	if err := h.recordsTx(r.Context(), func(svc *records.Service) error {
		locked, err := svc.LockForm(r.Context(), formID)
		if err != nil || !locked {
			return err
		}
		return svc.InsertFormEvent(r.Context(), formID, "locked", username, "")
	}); err != nil {
		serverError(w, "lock error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/def", formID), http.StatusSeeOther)
}

// UnlockForm — POST /forms/{id}/unlock
// Requires a comment, sets locked=0, and writes an 'unlocked' event to form_events.
func (h *Handler) UnlockForm(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	comment := strings.TrimSpace(r.FormValue("comment"))
	if comment == "" {
		http.Error(w, "a comment is required to unlock a form", http.StatusBadRequest)
		return
	}

	username := ""
	if u := h.currentUser(r); u != nil {
		username = u.Username
	}

	if err := h.recordsTx(r.Context(), func(svc *records.Service) error {
		unlocked, err := svc.UnlockForm(r.Context(), formID)
		if err != nil || !unlocked {
			return err
		}
		return svc.InsertFormEvent(r.Context(), formID, "unlocked", username, comment)
	}); err != nil {
		serverError(w, "unlock error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/def", formID), http.StatusSeeOther)
}

// ArchiveStep — POST /forms/{id}/tests/{testID}/archive
// Toggles form_row.archived for a single step. Form field "archived"=1 archives,
// anything else unarchives. No hard delete. Wrapped in a transaction so SetAuditUser
// attributes the history-trigger row to the current user (same pattern as SaveFormDef).
func (h *Handler) ArchiveStep(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	testID, err := strconv.Atoi(chi.URLParam(r, "testID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}
	archived := r.FormValue("archived") == "1"

	tx, err := h.beginTx(r.Context())
	if err != nil {
		serverError(w, "could not start transaction", err)
		return
	}
	defer tx.Rollback()
	svc := records.New(tx)

	if u := h.currentUser(r); u != nil {
		svc.SetAuditUser(r.Context(), u.Username) // best-effort, as in SaveFormDef
	}

	if err := svc.SetStepArchived(r.Context(), formID, testID, archived); err != nil {
		serverError(w, "archive error", err)
		return
	}

	if err := tx.Commit(); err != nil {
		serverError(w, "archive error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/def/edit", formID), http.StatusSeeOther)
}

// SaveResults â€" POST /records/{id}/edit
func (h *Handler) SaveResults(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	// #191: every write below happens in this one transaction.
	tx, err := h.beginTx(r.Context())
	if err != nil {
		serverError(w, "could not start transaction", err)
		return
	}
	defer tx.Rollback()
	svc := records.New(tx)

	// #191: claim the WIP record inside the tx. The row lock serializes against
	// Lock/Complete; false = locked (or missing — told apart by the read below).
	claimed, err := svc.ClaimRecord(r.Context(), recordID)
	if err != nil {
		serverError(w, "could not save record", err)
		return
	}

	got, err := svc.GetRecord(r.Context(), recordID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	record := testRecord(got)
	if !claimed {
		http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
		return
	}

	// Form context for resolving {form.X} tokens when snapshotting (#487).
	hdr, err := svc.GetFormHeader(r.Context(), record.FormID)
	if err != nil && err != sql.ErrNoRows {
		serverError(w, "query error", err) // a failed statement aborts the Postgres tx
		return
	}
	form := testForm(hdr)

	// Load steps for pass_fail computation and INSERT snapshots.
	steps, err := h.loadSteps(r.Context(), svc, record.FormID)
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	// Load existing results keyed by form_row_id â€" used for change detection and UPDATE vs INSERT.
	// SpecMin/SpecMax/PFType carry the frozen snapshot so edits re-evaluate P/F against the spec
	// the record was taken under, not the live definition (#487).
	existing := map[int]records.ResultRow{}
	saved, err := svc.ListResults(r.Context(), recordID)
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	for _, x := range saved {
		existing[x.FormRowID] = x
	}

	for key, vals := range r.Form {
		if !strings.HasPrefix(key, "result_") {
			continue
		}
		testID, err := strconv.Atoi(strings.TrimPrefix(key, "result_"))
		if err != nil {
			continue
		}
		step, ok := steps[testID]
		if !ok {
			continue
		}
		result := strings.TrimSpace(vals[0])
		comment := strings.TrimSpace(r.FormValue(fmt.Sprintf("comment_%d", testID)))

		if prev, exists := existing[testID]; exists {
			// Only UPDATE if result or comment actually changed
			if result != prev.Result || comment != prev.Comment {
				// Re-evaluate against the frozen snapshot, falling back to the live def for
				// pre-#487 rows whose snapshot columns are empty.
				pfStep := step
				if prev.SpecMin != "" || prev.SpecMax != "" || prev.PfType != "" {
					pfStep = &models.TestStep{SpecMin: prev.SpecMin, SpecMax: prev.SpecMax, PFType: prev.PfType}
				}
				var passFail *bool
				if result != "" {
					passFail = models.ComputePassFail(result, pfStep)
				}
				// #251: insert into TestResultHistory here when per-result change history is added
				if err := svc.UpdateResultValue(r.Context(), prev.ID, result, comment, passFail); err != nil {
					serverError(w, "could not save result", err)
					return
				}
			}
		} else if result != "" || comment != "" {
			// Legacy path: a pre-#487 record's previously-unrecorded row gets its first value.
			// Materialize it now as a frozen snapshot, identical in shape to creation-time
			// materialization ({id} kept for render-time resolution; self/record/form baked).
			bakeStepTokens(step, &record, &form)
			var passFail *bool
			if result != "" {
				passFail = models.ComputePassFail(result, step)
			}
			if err := svc.InsertResult(r.Context(), recordID, testID, resultDef(step), &result, &comment, passFail); err != nil {
				serverError(w, "could not save result", err)
				return
			}
		}
	}

	recordType := strings.TrimSpace(r.FormValue("record_type"))
	instrumentType := strings.TrimSpace(r.FormValue("instrument_type"))
	notes := strings.TrimSpace(r.FormValue("notes")) // #870: record-level session remark

	// #677: lot/build linkage saves with the record's other metadata (blank clears it).
	lotArg, buildArg, linkErr := h.recordLinkageArgs(r, record.PartNumberID)
	if linkErr != nil {
		log.Printf("invalid lot/build selection: %v", linkErr)
		http.Error(w, "invalid lot or build selection", http.StatusBadRequest)
		return
	}

	// #745: a serial/lot_serial part mints (or, on retest, re-links) the serialized
	// unit this record refers to when saved with provenance (a picked build or lot).
	// The record then points at the unit via unit_id and leaves lot_id/build_id NULL —
	// lot/build are read through the unit (Q8 FK-consistency invariant).
	pt, e := svc.GetPartTracking(r.Context(), record.PartNumberID)
	if e != nil && e != sql.ErrNoRows {
		http.Error(w, "could not read part tracking mode: "+e.Error(), http.StatusInternalServerError)
		return
	}
	trackingMode := pt.TrackingMode

	var rd *time.Time
	if rdStr := r.FormValue("record_date"); rdStr != "" {
		if t, e := time.Parse("2006-01-02T15:04", rdStr); e == nil {
			rd = &t
		} else if t, e := time.Parse("2006-01-02", rdStr); e == nil {
			rd = &t
		}
	}

	// #747: build-at-test-time. When the inline build panel was submitted, build this
	// part in the same transaction and use the new build (and its output lot) as the
	// record's provenance below. Both build workflows converge on the same end state:
	// the standalone Build tab (build-first) and this test-time build.
	// #867: a serial/lot_serial record IS the one unit under test, so it always builds
	// qty=1. A lot/none record has no single unit (Q8: whole-lot/batch testing
	// enumerates no individual units) — it builds a user-entered qty instead.
	if fv(r, "build_panel") == "1" {
		// Don't re-build a record already linked to a build/unit — a second build would
		// silently re-consume stock and orphan itself (the unit upsert reuses the
		// existing unit by serial and would not reference the new build).
		if record.UnitID != nil || record.BuildID != nil {
			http.Error(w, "This record is already linked to a build.", http.StatusBadRequest)
			return
		}
		qty := 1.0
		if models.TracksSerials(trackingMode) {
			// A serial/lot_serial part must carry a serial before building — otherwise
			// the unit can't be minted and the build would consume stock for a unit
			// that never exists, landing lot/build straight on the record (violating Q8).
			if record.SerialNumber == "" {
				http.Error(w, "Enter a serial number before building this unit.", http.StatusBadRequest)
				return
			}
		} else {
			parsedQty, qerr := parseBuildQty(r)
			if qerr != nil {
				log.Printf("invalid build quantity: %v", qerr)
				http.Error(w, "Invalid quantity", http.StatusBadRequest)
				return
			}
			qty = parsedQty
		}
		outputLotTracked := models.TracksLots(trackingMode)
		lines, lerr := h.loadBuildLines(r.Context(), record.PartNumberID)
		if lerr != nil {
			serverError(w, "could not load BOM", lerr)
			return
		}
		if len(lines) == 0 {
			http.Error(w, "This part has no BOM, so there is nothing to build.", http.StatusBadRequest)
			return
		}
		lotPicks, pickErr := h.collectLotPicks(r, lines)
		if pickErr != "" {
			http.Error(w, pickErr, http.StatusBadRequest)
			return
		}
		buildDate := h.userNow(r)
		if rd != nil {
			buildDate = *rd
		}
		bID, outLot, berr := h.performBuild(r, tx, record.PartNumberID, outputLotTracked, qty, buildDate, "", lines, lotPicks)
		if berr != nil {
			serverError(w, "could not build", berr)
			return
		}
		buildArg = &bID // the new build is this unit's provenance, overriding any dropdown pick
		if outputLotTracked {
			lotArg = &outLot
		}
	}

	// #872: the lot-note box on this page contributes an append-only delta — only the new
	// text is posted and appendLotNote concatenates server-side, so two testers with this
	// page open for a whole session both land their line instead of one overwriting the
	// other's page-load copy. Placed here so a note can follow a lot the build above just
	// created, and while lotArg still holds the lot — the Q8 reset below nils it out.
	if lotNote := strings.TrimSpace(r.FormValue("lot_note")); lotNote != "" {
		if lotArg != nil {
			username := ""
			if u := h.currentUser(r); u != nil {
				username = u.Username
			}
			if err := h.appendLotNote(r.Context(), tx, *lotArg, lotNote, username); err != nil {
				serverError(w, "could not save lot note", err)
				return
			}
		}
	}

	var unitArg *int
	if record.UnitID != nil {
		// Already linked to a unit — reuse it rather than re-deriving from
		// serial_number. A unit's serial can be edited after the fact (#799, Part →
		// Units), which would otherwise desync it from record.SerialNumber; re-deriving
		// by serial on every save would then silently mint a duplicate unit and orphan
		// the original (#876). There is no UI to change a record's serial_number after
		// creation, so the record<->unit link, once set, is authoritative.
		unitArg = record.UnitID
	} else if models.TracksSerials(trackingMode) && record.SerialNumber != "" && (buildArg != nil || lotArg != nil) {
		// Find or lazily create the unit for (part, serial) (#745, Q5): a retest re-links the existing
		// unit, and provenance is set only on creation. A test-minted unit should carry at least one of
		// build/lot, so the upsert is skipped when both are nil (an app rule, not a DB CHECK: #799 dropped
		// CK_unit_provenance since a manual unit legitimately has neither).
		uid, uerr := inventory.New(tx).UpsertTestUnit(r.Context(), record.PartNumberID, record.SerialNumber, buildArg, lotArg)
		if uerr != nil {
			serverError(w, "could not record unit", uerr)
			return
		}
		unitArg = &uid
	}
	if unitArg != nil {
		lotArg, buildArg = nil, nil // Q8: provenance lives on the unit, not the record
	}
	if err := svc.UpdateRecordAfterSave(r.Context(), recordID, records.RecordSave{
		RecordDate: rd, RecordType: recordType, Notes: notes, InstrumentType: instrumentType,
		LotID: lotArg, BuildID: buildArg, UnitID: unitArg,
	}); err != nil {
		serverError(w, "could not save record", err)
		return
	}
	if err := tx.Commit(); err != nil {
		serverError(w, "commit error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
}

// ResyncRecord — POST /records/{id}/resync
// Re-pulls the current (live) definition into the record's frozen snapshot (#487): refreshes the
// snapshot definition fields (incl. type/hide_formula/default_result) on every existing row,
// materializes rows for steps added since creation, recomputes pass_fail against the current limits,
// and refreshes the record's test_order. Locked records are rejected. Rows for steps removed/archived
// from the form are left untouched, preserving their historical result.
func (h *Handler) ResyncRecord(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		serverError(w, "could not start transaction", err)
		return
	}
	defer tx.Rollback()
	svc := records.New(tx)

	// Claim the WIP record first, like SaveResults (#191): the row lock serializes against
	// Lock/Complete and a concurrent save, so the reads below can't go stale before the writes.
	claimed, err := svc.ClaimRecord(r.Context(), recordID)
	if err != nil {
		serverError(w, "resync error", err)
		return
	}
	got, err := svc.GetRecord(r.Context(), recordID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	if !claimed {
		http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
		return
	}
	record := testRecord(got)

	hdr, err := svc.GetFormHeader(r.Context(), record.FormID)
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	form := testForm(hdr)

	// Live definition (full) and the record's existing snapshot rows.
	steps, err := h.loadSteps(r.Context(), svc, record.FormID)
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	existing := map[int]int{}                  // form_row_id -> result row id
	curResults := map[int]*models.TestResult{} // form_row_id -> recorded value (for {id} P/F resolution)
	saved, err := svc.ListResults(r.Context(), recordID)
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	for _, x := range saved {
		existing[x.FormRowID] = x.ID
		curResults[x.FormRowID] = &models.TestResult{ID: x.ID, TestID: x.FormRowID, Result: x.Result}
	}

	// Walk the form's CURRENT order so newly added steps are materialized too.
	for _, tid := range form.OrderedTestIDs() {
		step, ok := steps[tid]
		if !ok {
			continue
		}
		if step.Archived {
			continue
		}
		if step.Type == 0 && !stepAppliesToRecord(step.InstrumentTypes, record.InstrumentType) {
			continue
		}
		bakeStepTokens(step, &record, &form)

		// P/F against the refreshed spec; resolve {id} bounds against the record's own results.
		var passFail *bool
		if res, ok := curResults[tid]; ok && res.Result != "" {
			pfStep := *step
			pfStep.SpecMin = substituteRefs(step.SpecMin, curResults, steps, &record, &form)
			pfStep.SpecMax = substituteRefs(step.SpecMax, curResults, steps, &record, &form)
			passFail = models.ComputePassFail(res.Result, &pfStep)
		}

		if rowID, ok := existing[tid]; ok {
			if err := svc.RefreshResult(r.Context(), rowID, resultDef(step), passFail); err != nil {
				serverError(w, "resync error", err)
				return
			}
		} else {
			// Step added to the form since this record was created — materialize an empty row.
			if err := svc.InsertResult(r.Context(), recordID, tid, resultDef(step), nil, nil, nil); err != nil {
				serverError(w, "resync error", err)
				return
			}
		}
	}

	// Refresh the record's step-order snapshot so newly added steps appear on edit/view.
	// Re-pulling the live definition also re-captures the form's current revision (#260):
	// the snapshot now represents that revision.
	if err := svc.ResyncRecordHeader(r.Context(), recordID, form.TestOrder, form.Revision); err != nil {
		serverError(w, "resync error", err)
		return
	}

	if err := tx.Commit(); err != nil {
		serverError(w, "resync error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
}

// formPN is a selectable part number for the new-form / duplicate-form PN picker.
type formPN struct {
	PartNumberID int
	PartNumber   string
	Description  string
}

// formPNList returns FORM-category PNs that don't already have an active form.
// Used by both NewForm and DuplicateForm to populate the PN picker.
func (h *Handler) formPNList(ctx context.Context) ([]formPN, error) {
	rows, err := h.records().ListFormPartOptions(ctx)
	if err != nil {
		return nil, err
	}
	var list []formPN
	for _, p := range rows {
		list = append(list, formPN{PartNumberID: p.ID, PartNumber: p.PartNumber, Description: p.Description})
	}
	return list, nil
}

// copyFormSteps copies all test steps from sourceID into newFormID (svc runs on the caller's tx)
// and sets test_order on the new form. Returns an error on any failure (a missing source included).
func (h *Handler) copyFormSteps(ctx context.Context, svc *records.Service, sourceID, newFormID int) error {
	src, err := svc.GetFormHeader(ctx, sourceID)
	if err != nil {
		return err
	}

	// Walk steps in source test_order sequence; ids that aren't the source's steps are skipped.
	form := testForm(src)
	orderedIDs := form.OrderedTestIDs()
	idParts := make([]string, 0, len(orderedIDs))
	for _, oldID := range orderedIDs {
		newStepID, err := svc.CopyStep(ctx, sourceID, oldID, newFormID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		idParts = append(idParts, strconv.Itoa(newStepID))
	}
	return svc.SetFormTestOrder(ctx, newFormID, strings.Join(idParts, ","))
}

// NewForm — GET /forms/new
func (h *Handler) NewForm(w http.ResponseWriter, r *http.Request) {
	pns, err := h.formPNList(r.Context())
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	// Load existing active forms for the "copy steps from" dropdown.
	rows, err := h.records().ListSourceForms(r.Context())
	if err != nil {
		serverError(w, "query error", err)
		return
	}
	var sourceForms []models.TestForm
	for _, f := range rows {
		sourceForms = append(sourceForms, models.TestForm{ID: f.ID, PartNumber: f.PartNumber, Description: f.Description})
	}

	h.renderRecords(w, r, "form_new.html", map[string]any{
		"PNs":         pns,
		"SourceForms": sourceForms,
		"CSRFToken":   h.csrfToken(w, r),
		"ActiveTab":   "records",
		"TestMode":    h.cfg().TestMode,
	})
}

// CreateForm — POST /forms/new
// Inserts a form row and redirects to its definition edit page.
// If source_id is provided, copies all steps from that form.
func (h *Handler) CreateForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	pnid, err := strconv.Atoi(r.FormValue("pnid"))
	if err != nil || pnid <= 0 {
		http.Error(w, "invalid part number", http.StatusBadRequest)
		return
	}

	// Verify the PNID is a valid active FORM-category part number.
	if ok, err := h.records().IsFormPart(r.Context(), pnid); err != nil || !ok {
		http.Error(w, "invalid part number", http.StatusBadRequest)
		return
	}

	sourceID, _ := strconv.Atoi(r.FormValue("source_id")) // 0 = blank form

	tx, err := h.beginTx(r.Context())
	if err != nil {
		serverError(w, "tx error", err)
		return
	}
	svc := records.New(tx)

	// If copying from a source, carry over form-level settings.
	newID, err := svc.InsertForm(r.Context(), pnid, sourceID)
	if err != nil {
		tx.Rollback()
		serverError(w, "create error", err)
		return
	}

	if sourceID > 0 {
		if err := h.copyFormSteps(r.Context(), svc, sourceID, newID); err != nil {
			tx.Rollback()
			serverError(w, "copy error", err)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		serverError(w, "commit error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/def/edit", newID), http.StatusSeeOther)
}

// DuplicateForm — GET /forms/{id}/duplicate
func (h *Handler) DuplicateForm(w http.ResponseWriter, r *http.Request) {
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

	stepCount, _ := h.records().CountFormSteps(r.Context(), formID)

	pns, err := h.formPNList(r.Context())
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	h.renderRecords(w, r, "form_duplicate.html", map[string]any{
		"Form":      form,
		"StepCount": stepCount,
		"PNs":       pns,
		"CSRFToken": h.csrfToken(w, r),
		"ActiveTab": "records",
		"TestMode":  h.cfg().TestMode,
	})
}

// CreateDuplicate — POST /forms/{id}/duplicate
// Copies all steps from the source form into a new form with the chosen PN.
func (h *Handler) CreateDuplicate(w http.ResponseWriter, r *http.Request) {
	sourceID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	pnid, err := strconv.Atoi(r.FormValue("pnid"))
	if err != nil || pnid <= 0 {
		http.Error(w, "invalid part number", http.StatusBadRequest)
		return
	}

	// Verify the PNID is a valid active FORM-category part number.
	if ok, err := h.records().IsFormPart(r.Context(), pnid); err != nil || !ok {
		http.Error(w, "invalid part number", http.StatusBadRequest)
		return
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		serverError(w, "tx error", err)
		return
	}
	svc := records.New(tx)

	// Carry over form-level settings from the source form.
	newFormID, err := svc.InsertForm(r.Context(), pnid, sourceID)
	if err != nil {
		tx.Rollback()
		serverError(w, "insert error", err)
		return
	}

	if err := h.copyFormSteps(r.Context(), svc, sourceID, newFormID); err != nil {
		tx.Rollback()
		serverError(w, "copy error", err)
		return
	}

	if err := tx.Commit(); err != nil {
		serverError(w, "commit error", err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/def/edit", newFormID), http.StatusSeeOther)
}

// TestReport – GET /forms/{id}/tests/{testID}/report
// Shows all recorded results for a single test step across every active record of the form.
func (h *Handler) TestReport(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	testID, err := strconv.Atoi(chi.URLParam(r, "testID"))
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

	step, err := h.records().GetStep(r.Context(), testID)
	if err == sql.ErrNoRows || (err == nil && step.FormID != formID) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	h.renderRecords(w, r, "test_report.html", map[string]any{
		"Form":      form,
		"Step":      step,
		"ActiveTab": "records",
		"TestMode":  h.cfg().TestMode,
	})
}

// TestReportRows — GET /api/forms/{id}/tests/{testID}/report/rows
// JSON rows for the test report's shared client-side table: every active record's
// recorded result for one test step, in the default report order.
func (h *Handler) TestReportRows(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	testID, err := strconv.Atoi(chi.URLParam(r, "testID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	start := time.Now()

	format, err := h.records().GetStepFormat(r.Context(), testID, formID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	type row struct {
		ID           int    `json:"id"`
		SN           string `json:"sn"`
		SNPN         string `json:"snPN"`
		PartNumberID int    `json:"pnId"`
		Date         string `json:"date"`
		ResultDate   string `json:"resultDate"`
		Result       string `json:"result"`
		PassFail     string `json:"pf"`
		Comment      string `json:"comment"`
	}

	results, err := h.records().ListStepResults(r.Context(), formID, testID)
	if err != nil {
		serverError(w, "query error", err)
		return
	}

	out := make([]row, 0)
	for _, res := range results {
		rec := row{ID: res.ID, SN: res.SerialNumber, SNPN: res.SubjectPartNumber, PartNumberID: res.PartID,
			Comment: res.Comment, Result: applyResultFormat(res.Result, format)}
		if res.RecordDate != nil {
			rec.Date = res.RecordDate.Format("2006-01-02 15:04")
		}
		if res.UpdatedAt != nil {
			rec.ResultDate = res.UpdatedAt.Format("2006-01-02 15:04")
		}
		switch {
		case res.PassFail == nil:
			rec.PassFail = "—"
		case *res.PassFail:
			rec.PassFail = "PASS"
		default:
			rec.PassFail = "FAIL"
		}
		out = append(out, rec)
	}
	log.Printf("[rows] report form=%d test=%d: %d rows in %v", formID, testID, len(out), time.Since(start))
	writeJSON(w, out)
}
