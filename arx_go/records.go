package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
)

// refToken matches {123} step-ID tokens, {record.field}, and {form.field} context tokens.
var refToken = regexp.MustCompile(`\{(\d+|record\.\w+|form\.\w+)\}`)

// stepAppliesToRecord returns true if the step should be shown for the given instrument type.
// Empty instrument_types on the step means the step applies to all records.
// Empty instrument_type on the record means no filtering — show all steps.
func stepAppliesToRecord(instrumentTypes, recordType string) bool {
	if instrumentTypes == "" || recordType == "" {
		return true
	}
	for _, t := range strings.Split(instrumentTypes, ",") {
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
// renders entirely from these — the live test_definition is never consulted for a materialized row.
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

// substituteRefs replaces tokens in s:
//   - {123}              → recorded result for step 123, falling back to that step's spec_nom
//   - {record.type}      → record's Type (comments field)
//   - {record.pn}        → record's unit-under-test part number (serial_number_pn)
//   - {record.sn}        → record's serial number
//   - {record.pndesc}    → record's unit-under-test description (serial_number_pn_desc / title)
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
				return record.Comments
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
				return strconv.Itoa(form.PNID)
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
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE pn.category = 'FORM' AND pn.is_active = 1 AND f.is_active = 1
		ORDER BY pn.part_number ASC`,
		h.cfg.FormsTable(), h.cfg.PartsTable()))
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var forms []models.TestForm
	for rows.Next() {
		var f models.TestForm
		if err := rows.Scan(&f.ID, &f.PNID, &f.Locked, &f.PartNumber, &f.Title); err != nil {
			continue
		}
		forms = append(forms, f)
	}

	h.renderTR(w, r, "index.html", map[string]any{
		"Forms":    forms,
		"ActiveTab": "records",
		"TestMode": h.cfg.TestMode,
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
	var form models.TestForm
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), formID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Default to WIP-only; ?wip=false shows all.
	wipOnly := r.URL.Query().Get("wip") != "false"

	query := fmt.Sprintf(`
		SELECT id, form_id, COALESCE(part_number_id,0), serial_number, serial_number_pn, serial_number_pn_desc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_active, test_order
		FROM %s
		WHERE form_id = @p1 AND is_active = 1`, h.cfg.RecordsTable())
	if wipOnly {
		query += " AND is_locked = 0"
	}
	// serial_number + 0 forces numeric sort (same trick as Ruby Arel version)
	query += " ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC"

	rows, err := h.queryContext(r.Context(), query, formID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var records []models.TestRecord
	for rows.Next() {
		var rec models.TestRecord
		if err := rows.Scan(
			&rec.ID, &rec.FormID, &rec.PartNumberID, &rec.SerialNumber, &rec.SerialNumberPN,
			&rec.SerialNumberDesc, &rec.RecordDate, &rec.Comments,
			&rec.InstrumentType, &rec.Locked, &rec.Active, &rec.TestOrder,
		); err != nil {
			continue
		}
		records = append(records, rec)
	}

	h.renderTR(w, r, "records_index.html", map[string]any{
		"Form":     form,
		"Records":  records,
		"WIPOnly":  wipOnly,
		"ActiveTab": "records",
		"TestMode": h.cfg.TestMode,
	})
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

	var form models.TestForm
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), formID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	stepRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, parameter, specification, default_result, hide_formula, COALESCE(type,0) AS type,
		       spec_min, spec_max, pf_type,
		       archived, archive_id, revision, category, sheet_name, spec_units, spec_nom,
		       instrument_types, format, comment,
		       created_at, updated_at
		FROM %s WHERE form_id = @p1`, h.cfg.StepsTable()), formID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer stepRows.Close()

	stepsMap := map[int]*models.TestStep{}
	for stepRows.Next() {
		var s models.TestStep
		var archiveID, revision sql.NullInt32
		var (
			param, spec, defaultResult, hideFormula     sql.NullString
			specMin, specMax, pfType                    sql.NullString
			category, sheetName, specUnits, specNom     sql.NullString
			instrumentTypes, format                     sql.NullString
			stepComment                                 sql.NullString
		)
		if err := stepRows.Scan(
			&s.ID, &s.FormID, &param, &spec, &defaultResult, &hideFormula, &s.Type,
			&specMin, &specMax, &pfType,
			&s.Archived, &archiveID, &revision, &category, &sheetName,
			&specUnits, &specNom, &instrumentTypes,
			&format, &stepComment,
			&s.StepCreatedAt, &s.StepUpdatedAt,
		); err != nil {
			continue
		}
		s.Parameter = param.String
		s.Specification = spec.String
		s.DefaultResult = defaultResult.String
		s.HideFormula = hideFormula.String
		s.SpecMin = specMin.String
		s.SpecMax = specMax.String
		s.PFType = pfType.String
		if archiveID.Valid {
			s.ArchiveID = int(archiveID.Int32)
		}
		if revision.Valid {
			s.Revision = int(revision.Int32)
		}
		s.Category = category.String
		s.SheetName = sheetName.String
		s.SpecUnits = specUnits.String
		s.SpecNom = specNom.String
		s.InstrumentTypes = instrumentTypes.String
		s.Format = format.String
		s.StepComment = stepComment.String
		stepsMap[s.ID] = &s
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

	// Load history timestamps for timeline dots.
	type HistoryPoint struct {
		At      time.Time
		Count   int
		PctLeft float64 // position along timeline bar (5â€"95%)
	}
	hRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT CAST(changed_at AS DATE) AS day, COUNT(DISTINCT test_id) AS cnt
		FROM %s
		WHERE test_id IN (SELECT id FROM %s WHERE form_id = @p1)
		GROUP BY CAST(changed_at AS DATE) ORDER BY day ASC`,
		h.cfg.TestDefinitionHistoryTable(), h.cfg.StepsTable()), formID)
	var histPoints []HistoryPoint
	if err == nil {
		defer hRows.Close()
		for hRows.Next() {
			var hp HistoryPoint
			if err := hRows.Scan(&hp.At, &hp.Count); err == nil {
				histPoints = append(histPoints, hp)
			}
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

	h.renderTR(w, r, "form_def.html", map[string]any{
		"Form":        form,
		"Steps":       steps,
		"HasArchived": hasArchived,
		"HistPoints":  histPoints,
		"CSRFToken":   h.csrfToken(w, r),
		"ActiveTab":   "records",
		"TestMode":    h.cfg.TestMode,
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
	atStr := r.URL.Query().Get("at")
	at, err := time.Parse("2006-01-02", atStr)
	if err != nil {
		http.Error(w, "bad at param", http.StatusBadRequest)
		return
	}

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

	// see FUTURE_GOALS.md (records index query refactor)
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT t.id,
		       CASE WHEN h.test_id IS NOT NULL THEN COALESCE(h.type,0)            ELSE COALESCE(t.type,0)            END,
		       CASE WHEN h.test_id IS NOT NULL THEN COALESCE(h.parameter,'')      ELSE COALESCE(t.parameter,'')      END,
		       CASE WHEN h.test_id IS NOT NULL THEN COALESCE(h.spec_nom,'')       ELSE COALESCE(t.spec_nom,'')       END,
		       CASE WHEN h.test_id IS NOT NULL THEN COALESCE(h.spec_min,'')       ELSE COALESCE(t.spec_min,'')       END,
		       CASE WHEN h.test_id IS NOT NULL THEN COALESCE(h.spec_max,'')       ELSE COALESCE(t.spec_max,'')       END,
		       CASE WHEN h.test_id IS NOT NULL THEN COALESCE(h.spec_units,'')     ELSE COALESCE(t.spec_units,'')     END,
		       CASE WHEN h.test_id IS NOT NULL THEN COALESCE(h.pf_type,'')        ELSE COALESCE(t.pf_type,'')        END,
		       CASE WHEN h.test_id IS NOT NULL THEN COALESCE(h.default_result,'') ELSE COALESCE(t.default_result,'') END,
		       CASE WHEN h.test_id IS NOT NULL THEN COALESCE(h.hide_formula,'')   ELSE COALESCE(t.hide_formula,'')   END,
		       CASE WHEN h.test_id IS NOT NULL THEN 1 ELSE 0 END
		FROM %s t
		LEFT JOIN (
		    SELECT test_id, type, parameter, spec_nom, spec_min, spec_max,
		           spec_units, pf_type, default_result, hide_formula
		    FROM %s
		    WHERE CAST(changed_at AS DATE) = CAST(@p2 AS DATE)
		) h ON h.test_id = t.id
		WHERE t.form_id = @p1`,
		h.cfg.StepsTable(), h.cfg.TestDefinitionHistoryTable()),
		formID, at)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var steps []stepState
	for rows.Next() {
		var s stepState
		var changed int
		if err := rows.Scan(&s.ID, &s.Type, &s.Parameter,
			&s.SpecNom, &s.SpecMin, &s.SpecMax,
			&s.SpecUnits, &s.PFType, &s.DefaultResult, &s.HideFormula, &changed,
		); err != nil {
			continue
		}
		s.Changed = changed == 1
		steps = append(steps, s)
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

	var form models.TestForm
	var recordTypes, instrumentTypes sql.NullString
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, pn.part_number, pn.title,
		       f.record_types, f.instrument_types
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), formID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title,
			&recordTypes, &instrumentTypes)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	form.RecordTypes = recordTypes.String
	form.InstrumentTypes = instrumentTypes.String

	// Load raw step values â€" no substituteRefs, we want to edit the actual stored values.
	stepRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, COALESCE(type,0) AS type, parameter, specification, spec_nom, spec_min, spec_max, spec_units,
		       pf_type, default_result, hide_formula, archived, category, sheet_name,
		       instrument_types, format, comment
		FROM %s WHERE form_id = @p1`, h.cfg.StepsTable()), formID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer stepRows.Close()

	stepsMap := map[int]*models.TestStep{}
	for stepRows.Next() {
		var s models.TestStep
		var param, spec, specNom, specMin, specMax, specUnits sql.NullString
		var pfType, defaultResult, hideFormula, category, sheetName sql.NullString
		var instrumentTypes, format, comment sql.NullString
		if err := stepRows.Scan(
			&s.ID, &s.Type, &param, &spec, &specNom, &specMin, &specMax, &specUnits,
			&pfType, &defaultResult, &hideFormula, &s.Archived, &category, &sheetName,
			&instrumentTypes, &format, &comment,
		); err != nil {
			continue
		}
		s.Parameter = param.String
		s.Specification = spec.String
		s.SpecNom = specNom.String
		s.SpecMin = specMin.String
		s.SpecMax = specMax.String
		s.SpecUnits = specUnits.String
		s.PFType = pfType.String
		s.DefaultResult = defaultResult.String
		s.HideFormula = hideFormula.String
		s.Category = category.String
		s.SheetName = sheetName.String
		s.InstrumentTypes = instrumentTypes.String
		s.Format = format.String
		s.StepComment = comment.String
		stepsMap[s.ID] = &s
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

	h.renderTR(w, r, "form_def_edit.html", map[string]any{
		"Form":        form,
		"Steps":       steps,
		"HasArchived": hasArchived,
		"CSRFToken":   h.csrfToken(w, r),
		"ActiveTab":   "records",
		"TestMode":    h.cfg.TestMode,
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
	nullOrVal := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}

	// Collect step IDs from submitted original_ fields.
	stepIDs := map[int]bool{}
	for key := range r.Form {
		if strings.HasPrefix(key, "original_parameter_") {
			if id, err := strconv.Atoi(strings.TrimPrefix(key, "original_parameter_")); err == nil {
				stepIDs[id] = true
			}
		}
	}

	// Open a transaction so SET CONTEXT_INFO (connection-scoped) is seen by the
	// trg_test_definition_history trigger on every UPDATE in this batch.
	tx, err := h.beginTx(r.Context())
	if err != nil {
		http.Error(w, "could not start transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if u := h.currentUser(r); u != nil {
		tx.ExecContext(r.Context(), "SET CONTEXT_INFO @p1", []byte(u.Username))
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

		tx.ExecContext(r.Context(), fmt.Sprintf(`
			UPDATE %s SET
			  type=@p1, parameter=@p2, specification=@p3,
			  spec_nom=@p4, spec_min=@p5, spec_max=@p6, spec_units=@p7,
			  pf_type=@p8, default_result=@p9, hide_formula=@p10,
			  category=@p11, sheet_name=@p12, instrument_types=@p13,
			  format=@p14, comment=@p15, updated_at=GETDATE()
			WHERE id=@p16 AND form_id=@p17`, h.cfg.StepsTable()),
			stepType,
			sid(id, "parameter"), sid(id, "specification"),
			nullOrVal(sid(id, "spec_nom")),
			nullOrVal(sid(id, "spec_min")), nullOrVal(sid(id, "spec_max")),
			nullOrVal(sid(id, "spec_units")),
			nullOrVal(sid(id, "pf_type")),
			nullOrVal(sid(id, "default_result")),
			nullOrVal(hideFormula),
			nullOrVal(sid(id, "category")), nullOrVal(sid(id, "sheet_name")),
			nullOrVal(sid(id, "instrument_types")),
			nullOrVal(sid(id, "format")),
			nullOrVal(sid(id, "comment")),
			id, formID,
		)
	}

	// Parse and INSERT new rows submitted via new_row[IDX][field] inputs.
	type newRowData struct {
		Type             string
		Parameter        string
		Specification    string
		SpecNom          string
		SpecMin          string
		SpecMax          string
		SpecUnits        string
		PFType           string
		DefaultResult    string
		Hide             string
		Category         string
		SheetName        string
		InstrumentTypes  string
		Format           string
		Comment          string
	}
	parsedNewRows := map[string]newRowData{}
	for key, vals := range r.Form {
		if !strings.HasPrefix(key, "new_row[") {
			continue
		}
		rest := key[len("new_row["):]
		sep := strings.Index(rest, "][")
		if sep < 0 {
			continue
		}
		idx := rest[:sep]
		field := strings.TrimSuffix(rest[sep+2:], "]")
		val := ""
		if len(vals) > 0 {
			val = strings.TrimSpace(vals[0])
		}
		row := parsedNewRows[idx]
		switch field {
		case "type":              row.Type = val
		case "parameter":         row.Parameter = val
		case "specification":     row.Specification = val
		case "spec_nom":          row.SpecNom = val
		case "spec_min":          row.SpecMin = val
		case "spec_max":          row.SpecMax = val
		case "spec_units":        row.SpecUnits = val
		case "pf_type":           row.PFType = val
		case "default_result":    row.DefaultResult = val
		case "hide":              row.Hide = val
		case "category":          row.Category = val
		case "sheet_name":        row.SheetName = val
		case "instrument_types":  row.InstrumentTypes = val
		case "format":            row.Format = val
		case "comment":           row.Comment = val
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
		hideFormula := row.Hide
		var newID int
		if err2 := tx.QueryRowContext(r.Context(), fmt.Sprintf(`
			INSERT INTO %s
			  (form_id, type, parameter, specification, spec_nom, spec_min, spec_max, spec_units,
			   pf_type, default_result, hide_formula, category, sheet_name, instrument_types,
			   format, comment, created_at, updated_at)
			OUTPUT INSERTED.id
			VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16,GETDATE(),GETDATE())`,
			h.cfg.StepsTable()),
			formID, stepType, row.Parameter, nullOrVal(row.Specification),
			nullOrVal(row.SpecNom), nullOrVal(row.SpecMin), nullOrVal(row.SpecMax),
			nullOrVal(row.SpecUnits), nullOrVal(row.PFType), nullOrVal(row.DefaultResult),
			nullOrVal(hideFormula), nullOrVal(row.Category), nullOrVal(row.SheetName),
			nullOrVal(row.InstrumentTypes), nullOrVal(row.Format), nullOrVal(row.Comment),
		).Scan(&newID); err2 != nil {
			log.Printf("insert new test step: %v", err2)
			continue
		}
		newIDMap[idx] = newID
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "could not save steps: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Update Forms.test_order if the order changed or new rows were added.
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
				if strings.HasPrefix(p, "new_") {
					idx := strings.TrimPrefix(p, "new_")
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
		for _, p := range strings.Split(stepOrder, ",") {
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

		var currentOrder string
		h.queryRowContext(r.Context(), fmt.Sprintf(
			"SELECT COALESCE(test_order,'') FROM %s WHERE id=@p1", h.cfg.FormsTable()),
			formID).Scan(&currentOrder)
		if normalizeOrder(stepOrder) != normalizeOrder(currentOrder) {
			h.execContext(r.Context(), fmt.Sprintf(
				"UPDATE %s SET test_order=@p1 WHERE id=@p2", h.cfg.FormsTable()),
				normalizeOrder(stepOrder), formID)
		}
	}

	// Update form-level record_types and instrument_types if changed.
	newRecordTypes := strings.TrimSpace(r.FormValue("record_types"))
	newInstrTypes := strings.TrimSpace(r.FormValue("instrument_types"))
	if newRecordTypes != r.FormValue("original_record_types") ||
		newInstrTypes != r.FormValue("original_instrument_types") {
		h.execContext(r.Context(), fmt.Sprintf(
			"UPDATE %s SET record_types=@p1, instrument_types=@p2 WHERE id=@p3",
			h.cfg.FormsTable()),
			nullOrVal(newRecordTypes), nullOrVal(newInstrTypes), formID)
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/def", formID), http.StatusSeeOther)
}

// loadSteps loads all test_definition rows for a form keyed by id, with every field used for
// rendering and token resolution. Used as the live-definition fallback for un-materialized rows.
func (h *Handler) loadSteps(ctx context.Context, formID int) (map[int]*models.TestStep, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT id, form_id, parameter, specification, default_result, hide_formula, COALESCE(type,0) AS type,
		       spec_min, spec_max, pf_type,
		       archived, archive_id, revision, category, sheet_name, spec_units, spec_nom,
		       instrument_types, format, comment,
		       created_at, updated_at
		FROM %s WHERE form_id = @p1`, h.cfg.StepsTable()), formID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := map[int]*models.TestStep{}
	for rows.Next() {
		var s models.TestStep
		var archiveID, revision sql.NullInt32
		var param, spec, defaultResult, hideFormula sql.NullString
		var specMin, specMax, pfType sql.NullString
		var category, sheetName, specUnits, specNom sql.NullString
		var instrumentTypes, format, stepComment sql.NullString
		if err := rows.Scan(
			&s.ID, &s.FormID, &param, &spec, &defaultResult, &hideFormula, &s.Type,
			&specMin, &specMax, &pfType,
			&s.Archived, &archiveID, &revision, &category, &sheetName,
			&specUnits, &specNom, &instrumentTypes,
			&format, &stepComment,
			&s.StepCreatedAt, &s.StepUpdatedAt,
		); err != nil {
			continue
		}
		s.Parameter = param.String
		s.Specification = spec.String
		s.DefaultResult = defaultResult.String
		s.HideFormula = hideFormula.String
		s.SpecMin = specMin.String
		s.SpecMax = specMax.String
		s.PFType = pfType.String
		if archiveID.Valid {
			s.ArchiveID = int(archiveID.Int32)
		}
		if revision.Valid {
			s.Revision = int(revision.Int32)
		}
		s.Category = category.String
		s.SheetName = sheetName.String
		s.SpecUnits = specUnits.String
		s.SpecNom = specNom.String
		s.InstrumentTypes = instrumentTypes.String
		s.Format = format.String
		s.StepComment = stepComment.String
		steps[s.ID] = &s
	}
	return steps, nil
}

// loadRecordResults loads the materialized snapshot rows for a record, keyed by test_id (#487).
func (h *Handler) loadRecordResults(ctx context.Context, recordID int) (map[int]*models.TestResult, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT id, record_id, test_id,
		       COALESCE(parameter,''), COALESCE(specification,''), COALESCE(result,''),
		       pass_fail, COALESCE(comment,''),
		       COALESCE(spec_min,''), COALESCE(spec_nom,''), COALESCE(spec_max,''),
		       COALESCE(spec_units,''), COALESCE(pf_type,''), COALESCE(format,''),
		       COALESCE(type,0), COALESCE(hide_formula,''), COALESCE(default_result,''),
		       updated_at
		FROM %s WHERE record_id = @p1`, h.cfg.ResultsTable()), recordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := map[int]*models.TestResult{}
	for rows.Next() {
		var res models.TestResult
		if err := rows.Scan(&res.ID, &res.RecordID, &res.TestID, &res.Parameter,
			&res.Specification, &res.Result, &res.PassFail, &res.Comment,
			&res.SpecMin, &res.SpecNom, &res.SpecMax, &res.SpecUnits, &res.PFType, &res.Format,
			&res.Type, &res.HideFormula, &res.DefaultResult, &res.UpdatedAt); err != nil {
			continue
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
func (h *Handler) loadFrozenRows(ctx context.Context, record *models.TestRecord, form *models.TestForm) ([]models.ResultRow, map[int]*models.TestResult, map[int]*models.TestStep, error) {
	results, err := h.loadRecordResults(ctx, record.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	liveSteps, err := h.loadSteps(ctx, form.ID)
	if err != nil {
		return nil, nil, nil, err
	}

	// Synthetic step map for {id} spec_nom fallback: prefer the frozen snapshot nominal.
	refSteps := map[int]*models.TestStep{}
	for tid, st := range liveSteps {
		refSteps[tid] = st
	}
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
			if evaluateHide(res.HideFormula, results, refSteps, record, form) {
				continue
			}
			rows = append(rows, models.ResultRow{Step: stepFromResult(tid, res), Result: res, Level: res.Type})
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
		if evaluateHide(step.HideFormula, results, refSteps, record, form) {
			continue
		}
		if !stepAppliesToRecord(step.InstrumentTypes, record.InstrumentType) {
			continue
		}
		bakeStepTokens(step, record, form)
		rows = append(rows, models.ResultRow{Step: step, Result: nil, Level: step.Type})
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

	var record models.TestRecord
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, COALESCE(part_number_id,0), serial_number, serial_number_pn, serial_number_pn_desc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_active, test_order
		FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.PartNumberID, &record.SerialNumber, &record.SerialNumberPN,
			&record.SerialNumberDesc, &record.RecordDate, &record.Comments,
			&record.InstrumentType, &record.Locked, &record.Active, &record.TestOrder)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var form models.TestForm
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), record.FormID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build the frozen rows from the materialized snapshot (live def is only a legacy fallback).
	resultRows, results, refSteps, err := h.loadFrozenRows(r.Context(), &record, &form)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
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

	// Prev/next record IDs within this form, same ordering as the records list.
	var prevID, nextID int
	h.queryRowContext(r.Context(), fmt.Sprintf(`
		WITH ordered AS (
			SELECT id,
			       LAG(id)  OVER (ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC) AS prev_id,
			       LEAD(id) OVER (ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC) AS next_id
			FROM %s WHERE form_id = @p1 AND is_active = 1
		)
		SELECT COALESCE(prev_id, 0), COALESCE(next_id, 0) FROM ordered WHERE id = @p2`,
		h.cfg.RecordsTable()), record.FormID, recordID).Scan(&prevID, &nextID)

	var imageRows []models.ResultRow
	for _, row := range resultRows {
		if row.Level == 0 && imageResult(row.EffectiveValue()) {
			imageRows = append(imageRows, row)
		}
	}

	h.renderTR(w, r, "records_show.html", map[string]any{
		"Form":      form,
		"Record":    record,
		"Rows":      resultRows,
		"ImageRows": imageRows,
		"PrevID":    prevID,
		"NextID":    nextID,
		"CSRFToken": h.csrfToken(w, r),
		"ActiveTab": "records",
		"TestMode":  h.cfg.TestMode,
		"DebugMode": h.cfg.DebugMode,
	})
}

// RecordPrint — GET /records/{id}/print
func (h *Handler) RecordPrint(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var record models.TestRecord
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, COALESCE(part_number_id,0), serial_number, serial_number_pn, serial_number_pn_desc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_active, test_order
		FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.PartNumberID, &record.SerialNumber, &record.SerialNumberPN,
			&record.SerialNumberDesc, &record.RecordDate, &record.Comments,
			&record.InstrumentType, &record.Locked, &record.Active, &record.TestOrder)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var form models.TestForm
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), record.FormID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build the frozen rows from the materialized snapshot (live def is only a legacy fallback).
	resultRows, results, refSteps, err := h.loadFrozenRows(r.Context(), &record, &form)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
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
		if row.Level == 0 && imageResult(row.EffectiveValue()) {
			imageRows = append(imageRows, row)
		}
	}

	h.renderPrintTR(w, "record_print.html", map[string]any{
		"Form":      form,
		"Record":    record,
		"Rows":      resultRows,
		"ImageRows": imageRows,
		"ActiveTab": "records",
		"TestMode":  h.cfg.TestMode,
	})
}

// BOMPart is one row from the form's BOM (PL → PN join).
type BOMPart struct {
	PNID       int
	PartNumber string
	Title      string
}

// NewRecord — GET /forms/{id}/records/new
func (h *Handler) NewRecord(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var form models.TestForm
	var recordTypes, instrumentTypes sql.NullString
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, pn.part_number, pn.title, f.record_types, f.instrument_types
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), formID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title, &recordTypes, &instrumentTypes)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	form.RecordTypes = recordTypes.String
	form.InstrumentTypes = instrumentTypes.String

	// BOM lookup: parts listed under the form's own part number in PL.
	var bomParts []BOMPart
	bomRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT PN.id, PN.part_number, PN.title
		FROM %s PL JOIN %s PN ON PL.component_part_id = PN.id
		WHERE PL.parent_part_id = @p1
		ORDER BY PN.title`,
		h.cfg.BOMTable(), h.cfg.PartsTable()), form.PNID)
	if err == nil {
		defer bomRows.Close()
		for bomRows.Next() {
			var p BOMPart
			if bomRows.Scan(&p.PNID, &p.PartNumber, &p.Title) == nil {
				bomParts = append(bomParts, p)
			}
		}
	}

	// Next serial number: max numeric SN + 1, defaulting to 1 if none exist.
	// MAX()+1 SN is not safe under concurrent creates — see FUTURE_GOALS.md (serial number sequence)
	var nextSN sql.NullInt64
	h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT COALESCE(MAX(TRY_CAST(serial_number AS INT)) + 1, 1)
		FROM %s WHERE form_id = @p1`, h.cfg.RecordsTable()), formID).Scan(&nextSN)

	nextSNStr := "1"
	if nextSN.Valid {
		nextSNStr = strconv.FormatInt(nextSN.Int64, 10)
	}

	h.renderTR(w, r, "record_new.html", map[string]any{
		"Form":      form,
		"BOMParts":  bomParts,
		"NextSN":    nextSNStr,
		"Today":     time.Now().Format("2006-01-02T15:04"),
		"CSRFToken": h.csrfToken(w, r),
		"ActiveTab": "records",
		"TestMode":  h.cfg.TestMode,
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

	var form models.TestForm
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, pn.part_number, pn.title
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), formID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	serialNumber := strings.TrimSpace(r.FormValue("serial_number"))
	comments := strings.TrimSpace(r.FormValue("comments"))
	instrumentType := strings.TrimSpace(r.FormValue("instrument_type"))

	// Resolve the selected BOM part into denormalized PN fields.
	var snPN, snDesc string
	var partNumberID *int
	if pnidStr := r.FormValue("bom_pnid"); pnidStr != "" {
		if pnid, convErr := strconv.Atoi(pnidStr); convErr == nil {
			var pn, title sql.NullString
			if scanErr := h.queryRowContext(r.Context(), fmt.Sprintf(`
				SELECT part_number, title FROM %s WHERE id = @p1`,
				h.cfg.PartsTable()), pnid).Scan(&pn, &title); scanErr == nil {
				snPN = pn.String
				snDesc = title.String
				partNumberID = &pnid
			}
		}
	}

	recordDate := time.Now()
	if rdStr := r.FormValue("record_date"); rdStr != "" {
		if rd, parseErr := time.Parse("2006-01-02T15:04", rdStr); parseErr == nil {
			recordDate = rd
		} else if rd, parseErr := time.Parse("2006-01-02", rdStr); parseErr == nil {
			recordDate = rd
		}
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		http.Error(w, "could not start transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var newID int
	err = tx.QueryRowContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s
		  (form_id, part_number_id, serial_number, serial_number_pn, serial_number_pn_desc,
		   comments, instrument_type, test_order, record_date, created_at, is_active, is_locked)
		OUTPUT INSERTED.id
		VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,GETDATE(),1,0)`,
		h.cfg.RecordsTable()),
		formID, partNumberID, serialNumber, snPN, snDesc, comments, instrumentType, form.TestOrder, recordDate).Scan(&newID)
	if err != nil {
		http.Error(w, "insert error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Freeze the record from the start: materialize a snapshot row per applicable step (#487).
	rec := models.TestRecord{
		ID: newID, FormID: formID, SerialNumber: serialNumber,
		SerialNumberPN: snPN, SerialNumberDesc: snDesc, Comments: comments,
		InstrumentType: instrumentType, RecordDate: &recordDate, TestOrder: form.TestOrder,
	}
	if err := h.materializeRecordSteps(r.Context(), tx, &rec, &form); err != nil {
		http.Error(w, "materialize error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "commit error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d/edit", newID), http.StatusSeeOther)
}

// materializeRecordSteps creates a frozen test_result snapshot row for every applicable step in the
// form's order at record creation (#487): headings included (type > 0); archived and non-applicable
// instrument-type data steps skipped. Self/record/form tokens are baked; {id} cross-step tokens are
// left for render-time resolution. result/comment/pass_fail start empty.
func (h *Handler) materializeRecordSteps(ctx context.Context, tx *txLogger, record *models.TestRecord, form *models.TestForm) error {
	steps, err := h.loadSteps(ctx, form.ID)
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
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s
			  (record_id, test_id, type, parameter, specification, spec_min, spec_nom, spec_max,
			   spec_units, pf_type, format, hide_formula, default_result, updated_at)
			VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,GETDATE())`,
			h.cfg.ResultsTable()),
			record.ID, tid, step.Type, step.Parameter, step.Specification,
			step.SpecMin, step.SpecNom, step.SpecMax, step.SpecUnits, step.PFType, step.Format,
			step.HideFormula, step.DefaultResult); err != nil {
			return err
		}
	}
	return nil
}

// EditRecord — GET /records/{id}/edit
func (h *Handler) EditRecord(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var record models.TestRecord
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, COALESCE(part_number_id,0), serial_number, serial_number_pn, serial_number_pn_desc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_active, test_order
		FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.PartNumberID, &record.SerialNumber, &record.SerialNumberPN,
			&record.SerialNumberDesc, &record.RecordDate, &record.Comments,
			&record.InstrumentType, &record.Locked, &record.Active, &record.TestOrder)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if record.Locked {
		http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
		return
	}

	var form models.TestForm
	var editInstrumentTypes sql.NullString
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, pn.part_number, pn.title, f.instrument_types
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), record.FormID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title, &editInstrumentTypes)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	form.InstrumentTypes = editInstrumentTypes.String

	// Build the frozen rows from the materialized snapshot (live def is only a legacy fallback).
	// Edit always works against the frozen spec; "Update to latest" is the only re-pull path (#487).
	resultRows, results, refSteps, err := h.loadFrozenRows(r.Context(), &record, &form)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
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
		}
		if row.Step.DefaultResult != "" {
			row.RawDefault = row.Step.DefaultResult
		}
		// Resolve {id} cross-step tokens against the record's own results (frozen).
		resolveStepRefs(row.Step, results, refSteps, &record, &form)
	}

	h.renderTR(w, r, "record_edit.html", map[string]any{
		"Form":      form,
		"Record":    record,
		"Rows":      resultRows,
		"CSRFToken": h.csrfToken(w, r),
		"ActiveTab": "records",
		"TestMode":  h.cfg.TestMode,
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

	res, err := h.execContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET is_locked=1, updated_at=GETDATE() WHERE id=@p1 AND is_locked=0",
		h.cfg.RecordsTable()), recordID)
	if err != nil {
		http.Error(w, "lock error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if n, _ := res.RowsAffected(); n > 0 {
		h.execContext(r.Context(), fmt.Sprintf(
			"INSERT INTO %s (test_record_id, event_type, username, event_date) VALUES (@p1, 'locked', @p2, GETDATE())",
			h.cfg.RecordEventsTable()), recordID, username)
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
}

// UnlockRecord — POST /records/{id}/unlock
// Requires a comment, sets locked=0, and writes an 'unlocked' event to record_events.
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

	username := ""
	if u := h.currentUser(r); u != nil {
		username = u.Username
	}

	res, err := h.execContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET is_locked=0, updated_at=GETDATE() WHERE id=@p1 AND is_locked=1",
		h.cfg.RecordsTable()), recordID)
	if err != nil {
		http.Error(w, "unlock error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if n, _ := res.RowsAffected(); n > 0 {
		h.execContext(r.Context(), fmt.Sprintf(
			"INSERT INTO %s (test_record_id, event_type, username, event_date, comments) VALUES (@p1, 'unlocked', @p2, GETDATE(), @p3)",
			h.cfg.RecordEventsTable()), recordID, username, comment)
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
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

	res, err := h.execContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET is_locked=1 WHERE id=@p1 AND is_locked=0",
		h.cfg.FormsTable()), formID)
	if err != nil {
		http.Error(w, "lock error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if n, _ := res.RowsAffected(); n > 0 {
		h.execContext(r.Context(), fmt.Sprintf(
			"INSERT INTO %s (form_id, event_type, username, event_date) VALUES (@p1, 'locked', @p2, GETDATE())",
			h.cfg.FormEventsTable()), formID, username)
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

	res, err := h.execContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET is_locked=0 WHERE id=@p1 AND is_locked=1",
		h.cfg.FormsTable()), formID)
	if err != nil {
		http.Error(w, "unlock error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if n, _ := res.RowsAffected(); n > 0 {
		h.execContext(r.Context(), fmt.Sprintf(
			"INSERT INTO %s (form_id, event_type, username, event_date, comments) VALUES (@p1, 'unlocked', @p2, GETDATE(), @p3)",
			h.cfg.FormEventsTable()), formID, username, comment)
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/def", formID), http.StatusSeeOther)
}

// ArchiveStep — POST /forms/{id}/tests/{testID}/archive
// Toggles test_definition.archived for a single step. Form field "archived"=1 archives,
// anything else unarchives. No hard delete. Wrapped in a transaction so SET CONTEXT_INFO
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
	archived := 0
	if r.FormValue("archived") == "1" {
		archived = 1
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		http.Error(w, "could not start transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if u := h.currentUser(r); u != nil {
		tx.ExecContext(r.Context(), "SET CONTEXT_INFO @p1", []byte(u.Username))
	}

	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET archived=@p1, updated_at=GETDATE() WHERE id=@p2 AND form_id=@p3",
		h.cfg.StepsTable()), archived, testID, formID); err != nil {
		http.Error(w, "archive error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "archive error: "+err.Error(), http.StatusInternalServerError)
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

	var record models.TestRecord
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, COALESCE(part_number_id,0), serial_number, serial_number_pn, serial_number_pn_desc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_active, test_order
		FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.PartNumberID, &record.SerialNumber, &record.SerialNumberPN,
			&record.SerialNumberDesc, &record.RecordDate, &record.Comments,
			&record.InstrumentType, &record.Locked, &record.Active, &record.TestOrder)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if record.Locked {
		http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
		return
	}

	// Form context for resolving {form.X} tokens when snapshotting (#487).
	var form models.TestForm
	h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, pn.part_number
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), record.FormID).
		Scan(&form.ID, &form.PNID, &form.PartNumber)

	// Load steps for pass_fail computation and INSERT snapshots.
	stepRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, parameter, specification, default_result, hide_formula, COALESCE(type,0) AS type,
		       spec_min, spec_max, pf_type, spec_units, spec_nom, archived
		FROM %s WHERE form_id = @p1`, h.cfg.StepsTable()), record.FormID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer stepRows.Close()

	steps := map[int]*models.TestStep{}
	for stepRows.Next() {
		var s models.TestStep
		var param, spec, defaultResult, hideFormula sql.NullString
		var specMin, specMax, pfType, specUnits, specNom sql.NullString
		if err := stepRows.Scan(
			&s.ID, &s.FormID, &param, &spec, &defaultResult, &hideFormula, &s.Type,
			&specMin, &specMax, &pfType, &specUnits, &specNom, &s.Archived,
		); err != nil {
			continue
		}
		s.Parameter = param.String
		s.Specification = spec.String
		s.DefaultResult = defaultResult.String
		s.HideFormula = hideFormula.String
		s.SpecMin = specMin.String
		s.SpecMax = specMax.String
		s.PFType = pfType.String
		s.SpecUnits = specUnits.String
		s.SpecNom = specNom.String
		steps[s.ID] = &s
	}

	// Load existing results keyed by test_id â€" used for change detection and UPDATE vs INSERT.
	// SpecMin/SpecMax/PFType carry the frozen snapshot so edits re-evaluate P/F against the spec
	// the record was taken under, not the live definition (#487).
	type savedResult struct {
		ID      int
		Result  string
		Comment string
		SpecMin string
		SpecMax string
		PFType  string
	}
	existing := map[int]savedResult{}
	exRows, err := h.queryContext(r.Context(), fmt.Sprintf(
		"SELECT id, test_id, result, comment, COALESCE(spec_min,''), COALESCE(spec_max,''), COALESCE(pf_type,'') FROM %s WHERE record_id = @p1", h.cfg.ResultsTable()), recordID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer exRows.Close()
	for exRows.Next() {
		var rid, tid int
		var result, comment sql.NullString
		var specMin, specMax, pfType string
		if err := exRows.Scan(&rid, &tid, &result, &comment, &specMin, &specMax, &pfType); err != nil {
			continue
		}
		existing[tid] = savedResult{ID: rid, Result: result.String, Comment: comment.String,
			SpecMin: specMin, SpecMax: specMax, PFType: pfType}
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
				if prev.SpecMin != "" || prev.SpecMax != "" || prev.PFType != "" {
					pfStep = &models.TestStep{SpecMin: prev.SpecMin, SpecMax: prev.SpecMax, PFType: prev.PFType}
				}
				var passFail *bool
				if result != "" {
					passFail = models.ComputePassFail(result, pfStep)
				}
				// #251: insert into TestResultHistory here when per-result change history is added
				h.execContext(r.Context(), fmt.Sprintf(`
					UPDATE %s SET result=@p1, comment=@p2, pass_fail=@p3, updated_at=GETDATE()
					WHERE id=@p4`, h.cfg.ResultsTable()),
					result, comment, passFail, prev.ID)
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
			h.execContext(r.Context(), fmt.Sprintf(`
				INSERT INTO %s
				  (record_id, test_id, result, comment, pass_fail, type,
				   parameter, specification, spec_min, spec_nom, spec_max, spec_units, pf_type, format,
				   hide_formula, default_result, updated_at)
				VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16,GETDATE())`,
				h.cfg.ResultsTable()),
				recordID, testID,
				result, comment, passFail, step.Type,
				step.Parameter, step.Specification,
				step.SpecMin, step.SpecNom, step.SpecMax, step.SpecUnits, step.PFType, step.Format,
				step.HideFormula, step.DefaultResult)
		}
	}

	comments := strings.TrimSpace(r.FormValue("comments"))
	instrumentType := strings.TrimSpace(r.FormValue("instrument_type"))
	if rdStr := r.FormValue("record_date"); rdStr != "" {
		var rd time.Time
		var parseErr error
		if rd, parseErr = time.Parse("2006-01-02T15:04", rdStr); parseErr != nil {
			rd, parseErr = time.Parse("2006-01-02", rdStr)
		}
		if parseErr == nil {
			h.execContext(r.Context(), fmt.Sprintf(
				"UPDATE %s SET record_date=@p1, comments=@p2, instrument_type=@p3, updated_at=GETDATE() WHERE id=@p4",
				h.cfg.RecordsTable()), rd, comments, instrumentType, recordID)
		} else {
			h.execContext(r.Context(), fmt.Sprintf(
				"UPDATE %s SET comments=@p1, instrument_type=@p2, updated_at=GETDATE() WHERE id=@p3",
				h.cfg.RecordsTable()), comments, instrumentType, recordID)
		}
	} else {
		h.execContext(r.Context(), fmt.Sprintf(
			"UPDATE %s SET comments=@p1, instrument_type=@p2, updated_at=GETDATE() WHERE id=@p3",
			h.cfg.RecordsTable()), comments, instrumentType, recordID)
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

	var record models.TestRecord
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, COALESCE(part_number_id,0), serial_number, serial_number_pn, serial_number_pn_desc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_active, test_order
		FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.PartNumberID, &record.SerialNumber, &record.SerialNumberPN,
			&record.SerialNumberDesc, &record.RecordDate, &record.Comments,
			&record.InstrumentType, &record.Locked, &record.Active, &record.TestOrder)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if record.Locked {
		http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
		return
	}

	var form models.TestForm
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, COALESCE(f.test_order,''), pn.part_number
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), record.FormID).
		Scan(&form.ID, &form.PNID, &form.TestOrder, &form.PartNumber); err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Live definition (full) and the record's existing snapshot rows.
	steps, err := h.loadSteps(r.Context(), record.FormID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	existing := map[int]int{}                  // test_id -> test_result row id
	curResults := map[int]*models.TestResult{} // test_id -> recorded value (for {id} P/F resolution)
	resRows, err := h.queryContext(r.Context(), fmt.Sprintf(
		"SELECT id, test_id, COALESCE(result,'') FROM %s WHERE record_id = @p1", h.cfg.ResultsTable()), recordID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for resRows.Next() {
		var id, tid int
		var result string
		if err := resRows.Scan(&id, &tid, &result); err != nil {
			continue
		}
		existing[tid] = id
		curResults[tid] = &models.TestResult{ID: id, TestID: tid, Result: result}
	}
	resRows.Close()

	tx, err := h.beginTx(r.Context())
	if err != nil {
		http.Error(w, "could not start transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

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
			if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
				UPDATE %s SET type=@p1, parameter=@p2, specification=@p3, spec_min=@p4, spec_nom=@p5, spec_max=@p6,
				              spec_units=@p7, pf_type=@p8, format=@p9, hide_formula=@p10, default_result=@p11,
				              pass_fail=@p12, updated_at=GETDATE()
				WHERE id=@p13`, h.cfg.ResultsTable()),
				step.Type, step.Parameter, step.Specification, step.SpecMin, step.SpecNom, step.SpecMax,
				step.SpecUnits, step.PFType, step.Format, step.HideFormula, step.DefaultResult,
				passFail, rowID); err != nil {
				http.Error(w, "resync error: "+err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			// Step added to the form since this record was created — materialize an empty row.
			if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
				INSERT INTO %s
				  (record_id, test_id, type, parameter, specification, spec_min, spec_nom, spec_max,
				   spec_units, pf_type, format, hide_formula, default_result, updated_at)
				VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,GETDATE())`,
				h.cfg.ResultsTable()),
				recordID, tid, step.Type, step.Parameter, step.Specification,
				step.SpecMin, step.SpecNom, step.SpecMax, step.SpecUnits, step.PFType, step.Format,
				step.HideFormula, step.DefaultResult); err != nil {
				http.Error(w, "resync error: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	// Refresh the record's step-order snapshot so newly added steps appear on edit/view.
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET test_order=@p1, updated_at=GETDATE() WHERE id=@p2", h.cfg.RecordsTable()),
		form.TestOrder, recordID); err != nil {
		http.Error(w, "resync error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "resync error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
}

// formPN is a selectable part number for the new-form / duplicate-form PN picker.
type formPN struct {
	PNID       int
	PartNumber string
	Title      string
}

// formPNList returns FORM-category PNs that don't already have an active form.
// Used by both NewForm and DuplicateForm to populate the PN picker.
func (h *Handler) formPNList(ctx context.Context) ([]formPN, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT id, part_number, title
		FROM %s
		WHERE category = 'FORM' AND is_active = 1
		  AND NOT EXISTS (
		      SELECT 1 FROM %s WHERE part_number_id = %s.id AND is_active = 1
		  )
		ORDER BY part_number`,
		h.cfg.PartsTable(), h.cfg.FormsTable(), h.cfg.PartsTable()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []formPN
	for rows.Next() {
		var pn formPN
		if err := rows.Scan(&pn.PNID, &pn.PartNumber, &pn.Title); err != nil {
			return nil, err
		}
		list = append(list, pn)
	}
	return list, nil
}

// copyFormSteps copies all test steps from sourceID into newFormID (within tx)
// and sets test_order on the new form. Returns an error on any failure.
func (h *Handler) copyFormSteps(ctx context.Context, tx *sql.Tx, sourceID, newFormID int) error {
	var sourceOrder string
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		"SELECT COALESCE(test_order,'') FROM %s WHERE id=@p1", h.cfg.FormsTable()), sourceID).
		Scan(&sourceOrder); err != nil {
		return err
	}

	stepRows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT id, COALESCE(type,0), parameter, specification, spec_nom, spec_min, spec_max,
		       spec_units, pf_type, default_result, hide_formula,
		       category, sheet_name, instrument_types, format, comment,
		       archive_id, revision
		FROM %s WHERE form_id=@p1`, h.cfg.StepsTable()), sourceID)
	if err != nil {
		return err
	}
	defer stepRows.Close()

	type stepRow struct {
		ID            int
		Type          int
		Parameter     sql.NullString
		Specification sql.NullString
		SpecNom       sql.NullString
		SpecMin       sql.NullString
		SpecMax       sql.NullString
		SpecUnits     sql.NullString
		PFType        sql.NullString
		DefaultResult sql.NullString
		HideFormula   sql.NullString
		Category      sql.NullString
		SheetName     sql.NullString
		InstrTypes    sql.NullString
		Format        sql.NullString
		Comment       sql.NullString
		ArchiveID     sql.NullInt64
		Revision      sql.NullInt64
	}
	stepsMap := map[int]stepRow{}
	for stepRows.Next() {
		var s stepRow
		if err := stepRows.Scan(
			&s.ID, &s.Type, &s.Parameter, &s.Specification,
			&s.SpecNom, &s.SpecMin, &s.SpecMax, &s.SpecUnits,
			&s.PFType, &s.DefaultResult, &s.HideFormula,
			&s.Category, &s.SheetName, &s.InstrTypes, &s.Format, &s.Comment,
			&s.ArchiveID, &s.Revision,
		); err != nil {
			return err
		}
		stepsMap[s.ID] = s
	}

	// Walk steps in source test_order sequence.
	var src models.TestForm
	src.TestOrder = sourceOrder
	orderedIDs := src.OrderedTestIDs()

	newIDs := make([]int, 0, len(orderedIDs))
	for _, oldID := range orderedIDs {
		s, ok := stepsMap[oldID]
		if !ok {
			continue
		}
		var newStepID int
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`
			INSERT INTO %s
			  (form_id, type, parameter, specification, spec_nom, spec_min, spec_max,
			   spec_units, pf_type, default_result, hide_formula,
			   category, sheet_name, instrument_types, format, comment,
			   archive_id, revision)
			OUTPUT INSERTED.id
			VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16,@p17,@p18)`,
			h.cfg.StepsTable()),
			newFormID, s.Type, s.Parameter.String, s.Specification.String,
			s.SpecNom, s.SpecMin, s.SpecMax, s.SpecUnits,
			s.PFType, s.DefaultResult, s.HideFormula,
			s.Category, s.SheetName, s.InstrTypes, s.Format, s.Comment,
			s.ArchiveID, s.Revision,
		).Scan(&newStepID); err != nil {
			return err
		}
		newIDs = append(newIDs, newStepID)
	}

	idParts := make([]string, len(newIDs))
	for i, id := range newIDs {
		idParts[i] = strconv.Itoa(id)
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf(
		"UPDATE %s SET test_order=@p1 WHERE id=@p2", h.cfg.FormsTable()),
		strings.Join(idParts, ","), newFormID)
	return err
}

// NewForm — GET /forms/new
func (h *Handler) NewForm(w http.ResponseWriter, r *http.Request) {
	pns, err := h.formPNList(r.Context())
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Load existing active forms for the "copy steps from" dropdown.
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, pn.part_number, pn.title
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id
		WHERE f.is_active = 1 ORDER BY pn.part_number`,
		h.cfg.FormsTable(), h.cfg.PartsTable()))
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var sourceForms []models.TestForm
	for rows.Next() {
		var f models.TestForm
		if err := rows.Scan(&f.ID, &f.PartNumber, &f.Title); err != nil {
			continue
		}
		sourceForms = append(sourceForms, f)
	}

	h.renderTR(w, r, "form_new.html", map[string]any{
		"PNs":         pns,
		"SourceForms": sourceForms,
		"CSRFToken":   h.csrfToken(w, r),
		"ActiveTab":   "records",
		"TestMode":    h.cfg.TestMode,
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
	var exists int
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(
		"SELECT COUNT(1) FROM %s WHERE id=@p1 AND category='FORM' AND is_active=1",
		h.cfg.PartsTable()), pnid).Scan(&exists); err != nil || exists == 0 {
		http.Error(w, "invalid part number", http.StatusBadRequest)
		return
	}

	sourceID, _ := strconv.Atoi(r.FormValue("source_id")) // 0 = blank form

	// If copying from a source, carry over form-level settings.
	var srcRecordTypes, srcInstrTypes sql.NullString
	if sourceID > 0 {
		h.queryRowContext(r.Context(), fmt.Sprintf(
			"SELECT record_types, instrument_types FROM %s WHERE id=@p1",
			h.cfg.FormsTable()), sourceID).Scan(&srcRecordTypes, &srcInstrTypes)
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "tx error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var newID int
	if err := tx.QueryRowContext(r.Context(), fmt.Sprintf(
		"INSERT INTO %s (part_number_id, is_active, is_locked, test_order, record_types, instrument_types) OUTPUT INSERTED.id VALUES (@p1, 1, 0, '', @p2, @p3)",
		h.cfg.FormsTable()), pnid, srcRecordTypes, srcInstrTypes).Scan(&newID); err != nil {
		tx.Rollback()
		http.Error(w, "create error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if sourceID > 0 {
		if err := h.copyFormSteps(r.Context(), tx, sourceID, newID); err != nil {
			tx.Rollback()
			http.Error(w, "copy error: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "commit error: "+err.Error(), http.StatusInternalServerError)
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

	var form models.TestForm
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, pn.part_number, pn.title
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), formID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var stepCount int
	h.queryRowContext(r.Context(), fmt.Sprintf(
		"SELECT COUNT(1) FROM %s WHERE form_id=@p1", h.cfg.StepsTable()), formID).Scan(&stepCount)

	pns, err := h.formPNList(r.Context())
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderTR(w, r, "form_duplicate.html", map[string]any{
		"Form":      form,
		"StepCount": stepCount,
		"PNs":       pns,
		"CSRFToken": h.csrfToken(w, r),
		"ActiveTab": "records",
		"TestMode":  h.cfg.TestMode,
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
	var exists int
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(
		"SELECT COUNT(1) FROM %s WHERE id=@p1 AND category='FORM' AND is_active=1",
		h.cfg.PartsTable()), pnid).Scan(&exists); err != nil || exists == 0 {
		http.Error(w, "invalid part number", http.StatusBadRequest)
		return
	}

	// Carry over form-level settings from the source form.
	var srcRecordTypes, srcInstrTypes sql.NullString
	h.queryRowContext(r.Context(), fmt.Sprintf(
		"SELECT record_types, instrument_types FROM %s WHERE id=@p1",
		h.cfg.FormsTable()), sourceID).Scan(&srcRecordTypes, &srcInstrTypes)

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "tx error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var newFormID int
	if err := tx.QueryRowContext(r.Context(), fmt.Sprintf(
		"INSERT INTO %s (part_number_id, is_active, is_locked, test_order, record_types, instrument_types) OUTPUT INSERTED.id VALUES (@p1, 1, 0, '', @p2, @p3)",
		h.cfg.FormsTable()), pnid, srcRecordTypes, srcInstrTypes).Scan(&newFormID); err != nil {
		tx.Rollback()
		http.Error(w, "insert error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.copyFormSteps(r.Context(), tx, sourceID, newFormID); err != nil {
		tx.Rollback()
		http.Error(w, "copy error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "commit error: "+err.Error(), http.StatusInternalServerError)
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

	var form models.TestForm
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), formID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	type stepMeta struct {
		ID            int
		FormID        int
		Parameter     string
		Specification string
		SpecUnits     string
		Format        string
	}
	var step stepMeta
	var param, spec, specUnits, format sql.NullString
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, COALESCE(parameter,''), COALESCE(specification,''),
		       COALESCE(spec_units,''), COALESCE(format,'')
		FROM %s WHERE id = @p1`, h.cfg.StepsTable()), testID).
		Scan(&step.ID, &step.FormID, &param, &spec, &specUnits, &format)
	if err == sql.ErrNoRows || (err == nil && step.FormID != formID) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	step.Parameter = param.String
	step.Specification = spec.String
	step.SpecUnits = specUnits.String
	step.Format = format.String

	type reportRow struct {
		RecordID        int
		SerialNumber    string
		SerialPN        string
		PartNumberID    int
		RecordDate      time.Time
		Locked          bool
		Result          string
		PassFail        sql.NullBool
		Comment         string
		ResultUpdatedAt *time.Time
	}

	resRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT trec.id, trec.serial_number, COALESCE(trec.serial_number_pn,''),
		       COALESCE(trec.part_number_id,0),
		       trec.record_date, trec.is_locked,
		       COALESCE(res.result,''), res.pass_fail, COALESCE(res.comment,''),
		       res.updated_at
		FROM %s res
		JOIN %s trec ON res.record_id = trec.id
		WHERE res.test_id = @p1 AND trec.form_id = @p2 AND trec.is_active = 1
		ORDER BY TRY_CAST(trec.serial_number AS INT) DESC, trec.record_date DESC`,
		h.cfg.ResultsTable(), h.cfg.RecordsTable()), testID, formID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resRows.Close()

	var rows []reportRow
	for resRows.Next() {
		var row reportRow
		if err := resRows.Scan(
			&row.RecordID, &row.SerialNumber, &row.SerialPN, &row.PartNumberID,
			&row.RecordDate, &row.Locked,
			&row.Result, &row.PassFail, &row.Comment,
			&row.ResultUpdatedAt,
		); err != nil {
			continue
		}
		rows = append(rows, row)
	}

	h.renderTR(w, r, "test_report.html", map[string]any{
		"Form":      form,
		"Step":      step,
		"Rows":      rows,
		"ActiveTab": "records",
		"TestMode":  h.cfg.TestMode,
	})
}
