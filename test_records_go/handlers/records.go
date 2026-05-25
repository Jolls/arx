package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/test_records_go/models"
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
//   - {123}          → recorded result for step 123, falling back to that step's spec_nom
//   - {record.type}  → record's Type (comments field)
//   - {record.pn}    → record's unit-under-test part number (serial_number_PN)
//   - {record.sn}     → record's serial number
//   - {record.pndesc} → record's unit-under-test description (serial_number_PNDesc / title)
//   - {record.date}   → record's test date (MM/DD/YYYY)
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
	h.CheckSchemaVersion(r.Context())
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT f.ID, f.PNID, f.locked, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.PNID = pn.PNID
		WHERE pn.category = 'FORM' AND pn.active = 1 AND f.active = 1
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

	h.render(w, "index.html", map[string]any{
		"Forms":    forms,
		"TestMode": h.cfg.TestMode,
	})
}

// RecordsList â€" GET /forms/{id}/records
func (h *Handler) RecordsList(w http.ResponseWriter, r *http.Request) {
	h.CheckSchemaVersion(r.Context())
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Load the form header.
	var form models.TestForm
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.ID, f.PNID, f.locked, f.test_order, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.PNID = pn.PNID
		WHERE f.ID = @p1`,
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
		SELECT ID, form_id, serial_number, serial_number_PN, serial_number_PNDesc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, locked, active, test_order
		FROM %s
		WHERE form_id = @p1 AND active = 1`, h.cfg.RecordsTable())
	if wipOnly {
		query += " AND locked = 0"
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
			&rec.ID, &rec.FormID, &rec.SerialNumber, &rec.SerialNumberPN,
			&rec.SerialNumberDesc, &rec.RecordDate, &rec.Comments,
			&rec.InstrumentType, &rec.Locked, &rec.Active, &rec.TestOrder,
		); err != nil {
			continue
		}
		records = append(records, rec)
	}

	h.render(w, "records_index.html", map[string]any{
		"Form":     form,
		"Records":  records,
		"WIPOnly":  wipOnly,
		"TestMode": h.cfg.TestMode,
	})
}

// FormDef â€" GET /forms/{id}/def
// Shows all test step definitions for a form without any result data.
// Used as a reference when creating a new test record.
func (h *Handler) FormDef(w http.ResponseWriter, r *http.Request) {
	h.CheckSchemaVersion(r.Context())
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var form models.TestForm
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.ID, f.PNID, f.locked, f.test_order, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.PNID = pn.PNID
		WHERE f.ID = @p1`,
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
		SELECT id, form_id, Parameter, Specification, default_result, hide_formula, COALESCE(type,0) AS type,
		       spec_min, spec_max, pf_type,
		       archive_id, revision, category, sheet_name, spec_units, spec_nom,
		       pf_formula, instrument_types, format, comment,
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
			pfFormula, instrumentTypes, format           sql.NullString
			stepComment                                 sql.NullString
		)
		if err := stepRows.Scan(
			&s.ID, &s.FormID, &param, &spec, &defaultResult, &hideFormula, &s.Type,
			&specMin, &specMax, &pfType,
			&archiveID, &revision, &category, &sheetName,
			&specUnits, &specNom, &pfFormula, &instrumentTypes,
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
		s.PFFormula = pfFormula.String
		s.InstrumentTypes = instrumentTypes.String
		s.Format = format.String
		s.StepComment = stepComment.String
		stepsMap[s.ID] = &s
	}

	ids := form.OrderedTestIDs()
	var steps []*models.TestStep
	for _, tid := range ids {
		step, ok := stepsMap[tid]
		if !ok {
			continue
		}
		if strings.EqualFold(step.HideFormula, "HIDE") {
			continue
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
		step.SpecNom = substituteRefs(step.SpecNom, nil, stepsMap, nil, &form)
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

	h.render(w, "form_def.html", map[string]any{
		"Form":        form,
		"Steps":       steps,
		"HistPoints":  histPoints,
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

	// TODO: this query is verbose and should be refactored (e.g. a view or stored proc).
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT t.id,
		       CASE WHEN h.test_id IS NOT NULL THEN COALESCE(h.type,0)            ELSE COALESCE(t.type,0)            END,
		       CASE WHEN h.test_id IS NOT NULL THEN COALESCE(h.Parameter,'')      ELSE COALESCE(t.Parameter,'')      END,
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
		    SELECT test_id, type, Parameter, spec_nom, spec_min, spec_max,
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
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.ID, f.PNID, f.locked, f.test_order, pn.part_number, pn.title
		FROM %s f JOIN %s pn ON f.PNID = pn.PNID WHERE f.ID = @p1`,
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

	// Load raw step values â€" no substituteRefs, we want to edit the actual stored values.
	stepRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, COALESCE(type,0) AS type, Parameter, Specification, spec_nom, spec_min, spec_max, spec_units,
		       pf_type, default_result, hide_formula, category, sheet_name,
		       instrument_types, comment
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
		var instrumentTypes, comment sql.NullString
		if err := stepRows.Scan(
			&s.ID, &s.Type, &param, &spec, &specNom, &specMin, &specMax, &specUnits,
			&pfType, &defaultResult, &hideFormula, &category, &sheetName,
			&instrumentTypes, &comment,
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
		s.StepComment = comment.String
		stepsMap[s.ID] = &s
	}

	ids := form.OrderedTestIDs()
	var steps []*models.TestStep
	for _, tid := range ids {
		if s, ok := stepsMap[tid]; ok {
			steps = append(steps, s)
		}
	}

	h.render(w, "form_def_edit.html", map[string]any{
		"Form":      form,
		"Steps":     steps,
		"CSRFToken": h.csrfToken(w, r),
		"TestMode":  h.cfg.TestMode,
	})
}

// SaveFormDef â€" POST /forms/{id}/def/edit
func (h *Handler) SaveFormDef(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !h.verifyCsrf(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
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

	for id := range stepIDs {
		stepType := 0
		if t, err := strconv.Atoi(sid(id, "type")); err == nil {
			stepType = t
		}
		origType := 0
		if t, err := strconv.Atoi(orig(id, "type")); err == nil {
			origType = t
		}
		hideFormula := ""
		if r.FormValue(fmt.Sprintf("hide_%d", id)) == "1" {
			hideFormula = "HIDE"
		}

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
			sid(id, "comment") == orig(id, "comment") {
			continue
		}

		h.execContext(r.Context(), fmt.Sprintf(`
			UPDATE %s SET
			  type=@p1, Parameter=@p2, Specification=@p3,
			  spec_nom=@p4, spec_min=@p5, spec_max=@p6, spec_units=@p7,
			  pf_type=@p8, default_result=@p9, hide_formula=@p10,
			  category=@p11, sheet_name=@p12, instrument_types=@p13,
			  comment=@p14, updated_at=GETDATE()
			WHERE id=@p15 AND form_id=@p16`, h.cfg.StepsTable()),
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
		hideFormula := ""
		if row.Hide == "1" {
			hideFormula = "HIDE"
		}
		var newID int
		if err2 := h.queryRowContext(r.Context(), fmt.Sprintf(`
			INSERT INTO %s
			  (form_id, type, Parameter, Specification, spec_nom, spec_min, spec_max, spec_units,
			   pf_type, default_result, hide_formula, category, sheet_name, instrument_types,
			   comment, created_at, updated_at)
			OUTPUT INSERTED.id
			VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,GETDATE(),GETDATE())`,
			h.cfg.StepsTable()),
			formID, stepType, row.Parameter, nullOrVal(row.Specification),
			nullOrVal(row.SpecNom), nullOrVal(row.SpecMin), nullOrVal(row.SpecMax),
			nullOrVal(row.SpecUnits), nullOrVal(row.PFType), nullOrVal(row.DefaultResult),
			nullOrVal(hideFormula), nullOrVal(row.Category), nullOrVal(row.SheetName),
			nullOrVal(row.InstrumentTypes), nullOrVal(row.Comment),
		).Scan(&newID); err2 != nil {
			log.Printf("insert new test step: %v", err2)
			continue
		}
		newIDMap[idx] = newID
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
			"SELECT COALESCE(test_order,'') FROM %s WHERE ID=@p1", h.cfg.FormsTable()),
			formID).Scan(&currentOrder)
		if normalizeOrder(stepOrder) != normalizeOrder(currentOrder) {
			h.execContext(r.Context(), fmt.Sprintf(
				"UPDATE %s SET test_order=@p1 WHERE ID=@p2", h.cfg.FormsTable()),
				normalizeOrder(stepOrder), formID)
		}
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/def", formID), http.StatusSeeOther)
}

// RecordDetail â€" GET /records/{id}
func (h *Handler) RecordDetail(w http.ResponseWriter, r *http.Request) {
	h.CheckSchemaVersion(r.Context())
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var record models.TestRecord
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT ID, form_id, serial_number, serial_number_PN, serial_number_PNDesc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, locked, active, test_order
		FROM %s WHERE ID = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.SerialNumber, &record.SerialNumberPN,
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
		SELECT f.ID, f.PNID, f.locked, f.test_order, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.PNID = pn.PNID
		WHERE f.ID = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), record.FormID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// TODO: debug query â€" remove extra columns before shipping edit UI
	// Load all test steps for this form, keyed by ID.
	stepRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, Parameter, Specification, default_result, hide_formula, COALESCE(type,0) AS type,
		       spec_min, spec_max, pf_type,
		       archive_id, revision, category, sheet_name, spec_units, spec_nom,
		       pf_formula, instrument_types, format, comment,
		       created_at, updated_at
		FROM %s WHERE form_id = @p1`, h.cfg.StepsTable()), record.FormID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer stepRows.Close()

	steps := map[int]*models.TestStep{}
	for stepRows.Next() {
		var s models.TestStep
		var (
			archiveID, revision                             sql.NullInt32
			param, spec, defaultResult, hideFormula         sql.NullString
			specMin, specMax, pfType                        sql.NullString
			category, sheetName, specUnits, specNom         sql.NullString
			pfFormula, instrumentTypes, format               sql.NullString
			stepComment                                     sql.NullString
		)
		if err := stepRows.Scan(
			&s.ID, &s.FormID, &param, &spec, &defaultResult, &hideFormula, &s.Type,
			&specMin, &specMax, &pfType,
			&archiveID, &revision, &category, &sheetName,
			&specUnits, &specNom, &pfFormula, &instrumentTypes,
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
		s.PFFormula = pfFormula.String
		s.InstrumentTypes = instrumentTypes.String
		s.Format = format.String
		s.StepComment = stepComment.String
		steps[s.ID] = &s
	}

	// Load all results for this record, keyed by test ID.
	resRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT ID, record_id, test_id,
		       COALESCE(parameter,''), COALESCE(specification,''), COALESCE(result,''),
		       pass_fail, COALESCE(comment,''), updated_at
		FROM %s WHERE record_id = @p1`, h.cfg.ResultsTable()), recordID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resRows.Close()

	results := map[int]*models.TestResult{}
	for resRows.Next() {
		var res models.TestResult
		if err := resRows.Scan(&res.ID, &res.RecordID, &res.TestID, &res.Parameter,
			&res.Specification, &res.Result, &res.PassFail, &res.Comment, &res.UpdatedAt); err != nil {
			continue
		}
		results[res.TestID] = &res
	}

	// Determine display order: record snapshot first, fall back to form order.
	ids := record.OrderedTestIDs()
	if len(ids) == 0 {
		ids = form.OrderedTestIDs()
	}

	var resultRows []models.ResultRow
	for _, tid := range ids {
		step, ok := steps[tid]
		if !ok {
			continue
		}
		level := step.Type
		if strings.EqualFold(step.HideFormula, "HIDE") {
			continue
		}
		if !stepAppliesToRecord(step.InstrumentTypes, record.InstrumentType) {
			continue
		}
		resultRows = append(resultRows, models.ResultRow{
			Step:   step,
			Result: results[tid],
			Level:  level,
		})
	}

	// Resolve {id} cross-reference tokens using recorded results, falling back to spec_nom.
	for i := range resultRows {
		row := &resultRows[i]
		if row.Level > 0 || row.Step == nil {
			continue
		}
		row.Step.Parameter = substituteRefs(row.Step.Parameter, results, steps, &record, &form)
		row.Step.Specification = substituteStepSelf(substituteRefs(row.Step.Specification, results, steps, &record, &form), row.Step)
		row.Step.SpecNom = substituteRefs(row.Step.SpecNom, results, steps, &record, &form)
		row.Step.SpecMin = substituteRefs(row.Step.SpecMin, results, steps, &record, &form)
		row.Step.SpecMax = substituteRefs(row.Step.SpecMax, results, steps, &record, &form)
		row.Step.DefaultResult = substituteStepSelf(substituteRefs(row.Step.DefaultResult, results, steps, &record, &form), row.Step)
	}

	// Prev/next record IDs within this form, same ordering as the records list.
	var prevID, nextID int
	h.queryRowContext(r.Context(), fmt.Sprintf(`
		WITH ordered AS (
			SELECT ID,
			       LAG(ID)  OVER (ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC) AS prev_id,
			       LEAD(ID) OVER (ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC) AS next_id
			FROM %s WHERE form_id = @p1 AND active = 1
		)
		SELECT COALESCE(prev_id, 0), COALESCE(next_id, 0) FROM ordered WHERE ID = @p2`,
		h.cfg.RecordsTable()), record.FormID, recordID).Scan(&prevID, &nextID)

	var imageRows []models.ResultRow
	for _, row := range resultRows {
		if row.Level == 0 && imageResult(row.EffectiveValue()) {
			imageRows = append(imageRows, row)
		}
	}

	h.render(w, "records_show.html", map[string]any{
		"Form":      form,
		"Record":    record,
		"Rows":      resultRows,
		"ImageRows": imageRows,
		"PrevID":    prevID,
		"NextID":    nextID,
		"CSRFToken": h.csrfToken(w, r),
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
		SELECT ID, form_id, serial_number, serial_number_PN, serial_number_PNDesc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, locked, active, test_order
		FROM %s WHERE ID = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.SerialNumber, &record.SerialNumberPN,
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
		SELECT f.ID, f.PNID, f.locked, f.test_order, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.PNID = pn.PNID
		WHERE f.ID = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), record.FormID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	stepRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, Parameter, Specification, default_result, hide_formula, COALESCE(type,0) AS type,
		       spec_min, spec_max, pf_type,
		       archive_id, revision, category, sheet_name, spec_units, spec_nom,
		       pf_formula, instrument_types, format, comment,
		       created_at, updated_at
		FROM %s WHERE form_id = @p1`, h.cfg.StepsTable()), record.FormID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer stepRows.Close()

	steps := map[int]*models.TestStep{}
	for stepRows.Next() {
		var s models.TestStep
		var (
			archiveID, revision                         sql.NullInt32
			param, spec, defaultResult, hideFormula     sql.NullString
			specMin, specMax, pfType                    sql.NullString
			category, sheetName, specUnits, specNom     sql.NullString
			pfFormula, instrumentTypes, format           sql.NullString
			stepComment                                 sql.NullString
		)
		if err := stepRows.Scan(
			&s.ID, &s.FormID, &param, &spec, &defaultResult, &hideFormula, &s.Type,
			&specMin, &specMax, &pfType,
			&archiveID, &revision, &category, &sheetName,
			&specUnits, &specNom, &pfFormula, &instrumentTypes,
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
		s.PFFormula = pfFormula.String
		s.InstrumentTypes = instrumentTypes.String
		s.Format = format.String
		s.StepComment = stepComment.String
		steps[s.ID] = &s
	}

	resRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT ID, record_id, test_id,
		       COALESCE(parameter,''), COALESCE(specification,''), COALESCE(result,''),
		       pass_fail, COALESCE(comment,''), updated_at
		FROM %s WHERE record_id = @p1`, h.cfg.ResultsTable()), recordID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resRows.Close()

	results := map[int]*models.TestResult{}
	for resRows.Next() {
		var res models.TestResult
		if err := resRows.Scan(&res.ID, &res.RecordID, &res.TestID, &res.Parameter,
			&res.Specification, &res.Result, &res.PassFail, &res.Comment, &res.UpdatedAt); err != nil {
			continue
		}
		results[res.TestID] = &res
	}

	ids := record.OrderedTestIDs()
	if len(ids) == 0 {
		ids = form.OrderedTestIDs()
	}

	var resultRows []models.ResultRow
	for _, tid := range ids {
		step, ok := steps[tid]
		if !ok {
			continue
		}
		if strings.EqualFold(step.HideFormula, "HIDE") {
			continue
		}
		if !stepAppliesToRecord(step.InstrumentTypes, record.InstrumentType) {
			continue
		}
		resultRows = append(resultRows, models.ResultRow{
			Step:   step,
			Result: results[tid],
			Level:  step.Type,
		})
	}

	for i := range resultRows {
		row := &resultRows[i]
		if row.Level > 0 || row.Step == nil {
			continue
		}
		row.Step.Parameter = substituteRefs(row.Step.Parameter, results, steps, &record, &form)
		row.Step.Specification = substituteStepSelf(substituteRefs(row.Step.Specification, results, steps, &record, &form), row.Step)
		row.Step.SpecNom = substituteRefs(row.Step.SpecNom, results, steps, &record, &form)
		row.Step.SpecMin = substituteRefs(row.Step.SpecMin, results, steps, &record, &form)
		row.Step.SpecMax = substituteRefs(row.Step.SpecMax, results, steps, &record, &form)
		row.Step.DefaultResult = substituteStepSelf(substituteRefs(row.Step.DefaultResult, results, steps, &record, &form), row.Step)
	}

	var imageRows []models.ResultRow
	for _, row := range resultRows {
		if row.Level == 0 && imageResult(row.EffectiveValue()) {
			imageRows = append(imageRows, row)
		}
	}

	h.renderPrint(w, "record_print.html", map[string]any{
		"Form":      form,
		"Record":    record,
		"Rows":      resultRows,
		"ImageRows": imageRows,
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
	h.CheckSchemaVersion(r.Context())
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var form models.TestForm
	var recordTypes, instrumentTypes sql.NullString
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.ID, f.PNID, f.locked, f.test_order, pn.part_number, pn.title, f.record_types, f.instrument_types
		FROM %s f JOIN %s pn ON f.PNID = pn.PNID WHERE f.ID = @p1`,
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
		SELECT PN.PNID, PN.part_number, PN.title
		FROM %s PL JOIN %s PN ON PL.PLPartID = PN.PNID
		WHERE PL.PLListID = @p1
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
	// TODO: replace with a proper per-form sequence/counter table.
	var nextSN sql.NullInt64
	h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT COALESCE(MAX(TRY_CAST(serial_number AS INT)) + 1, 1)
		FROM %s WHERE form_id = @p1`, h.cfg.RecordsTable()), formID).Scan(&nextSN)

	nextSNStr := "1"
	if nextSN.Valid {
		nextSNStr = strconv.FormatInt(nextSN.Int64, 10)
	}

	h.render(w, "record_new.html", map[string]any{
		"Form":      form,
		"BOMParts":  bomParts,
		"NextSN":    nextSNStr,
		"Today":     time.Now().Format("2006-01-02"),
		"CSRFToken": h.csrfToken(w, r),
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
	if !h.verifyCsrf(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	var form models.TestForm
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.ID, f.PNID, f.locked, f.test_order, pn.part_number, pn.title
		FROM %s f JOIN %s pn ON f.PNID = pn.PNID WHERE f.ID = @p1`,
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
				SELECT part_number, title FROM %s WHERE PNID = @p1`,
				h.cfg.PartsTable()), pnid).Scan(&pn, &title); scanErr == nil {
				snPN = pn.String
				snDesc = title.String
				partNumberID = &pnid
			}
		}
	}

	recordDate := time.Now()
	if rdStr := r.FormValue("record_date"); rdStr != "" {
		if rd, parseErr := time.Parse("2006-01-02", rdStr); parseErr == nil {
			recordDate = rd
		}
	}

	var newID int
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s
		  (form_id, part_number_id, serial_number, serial_number_PN, serial_number_PNDesc,
		   comments, instrument_type, test_order, record_date, created_at, active, locked)
		OUTPUT INSERTED.ID
		VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,GETDATE(),1,0)`,
		h.cfg.RecordsTable()),
		formID, partNumberID, serialNumber, snPN, snDesc, comments, instrumentType, form.TestOrder, recordDate).Scan(&newID)
	if err != nil {
		http.Error(w, "insert error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d/edit", newID), http.StatusSeeOther)
}

// EditRecord — GET /records/{id}/edit
func (h *Handler) EditRecord(w http.ResponseWriter, r *http.Request) {
	h.CheckSchemaVersion(r.Context())
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var record models.TestRecord
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT ID, form_id, serial_number, serial_number_PN, serial_number_PNDesc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, locked, active, test_order
		FROM %s WHERE ID = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.SerialNumber, &record.SerialNumberPN,
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
		SELECT f.ID, f.PNID, f.locked, f.test_order, pn.part_number, pn.title, f.instrument_types
		FROM %s f JOIN %s pn ON f.PNID = pn.PNID WHERE f.ID = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), record.FormID).
		Scan(&form.ID, &form.PNID, &form.Locked, &form.TestOrder, &form.PartNumber, &form.Title, &editInstrumentTypes)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	form.InstrumentTypes = editInstrumentTypes.String

	editStepRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, Parameter, Specification, default_result, hide_formula, COALESCE(type,0) AS type,
		       spec_min, spec_max, pf_type, spec_units, spec_nom
		FROM %s WHERE form_id = @p1`, h.cfg.StepsTable()), record.FormID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer editStepRows.Close()

	steps := map[int]*models.TestStep{}
	for editStepRows.Next() {
		var s models.TestStep
		var param, spec, defaultResult, hideFormula sql.NullString
		var specMin, specMax, pfType, specUnits, specNom sql.NullString
		if err := editStepRows.Scan(
			&s.ID, &s.FormID, &param, &spec, &defaultResult, &hideFormula, &s.Type,
			&specMin, &specMax, &pfType, &specUnits, &specNom,
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

	editResRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT ID, record_id, test_id,
		       COALESCE(parameter,''), COALESCE(specification,''), COALESCE(result,''),
		       pass_fail, COALESCE(comment,''), updated_at
		FROM %s WHERE record_id = @p1`, h.cfg.ResultsTable()), recordID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer editResRows.Close()

	results := map[int]*models.TestResult{}
	for editResRows.Next() {
		var res models.TestResult
		if err := editResRows.Scan(&res.ID, &res.RecordID, &res.TestID, &res.Parameter,
			&res.Specification, &res.Result, &res.PassFail, &res.Comment, &res.UpdatedAt); err != nil {
			continue
		}
		results[res.TestID] = &res
	}

	ids := record.OrderedTestIDs()
	if len(ids) == 0 {
		ids = form.OrderedTestIDs()
	}
	var resultRows []models.ResultRow
	for _, tid := range ids {
		step, ok := steps[tid]
		if !ok {
			continue
		}
		if strings.EqualFold(step.HideFormula, "HIDE") {
			continue
		}
		if !stepAppliesToRecord(step.InstrumentTypes, record.InstrumentType) {
			continue
		}
		resultRows = append(resultRows, models.ResultRow{
			Step:   step,
			Result: results[tid],
			Level:  step.Type,
		})
	}

	for i := range resultRows {
		row := &resultRows[i]
		if row.Level > 0 || row.Step == nil {
			continue
		}
		// For named-query steps, capture a partially-resolved spec (record tokens only,
		// {id} tokens left intact) so the edit page can re-resolve live as results change.
		if strings.HasPrefix(row.Step.SpecNom, "query:") {
			row.RawSpecNom = substituteRefs(row.Step.SpecNom, nil, nil, &record, &form)
		}
		// Capture partially-resolved default_result for formula evaluation in JS.
		if row.Step.DefaultResult != "" {
			row.RawDefault = substituteRefs(row.Step.DefaultResult, nil, nil, &record, &form)
		}
		row.Step.Parameter = substituteRefs(row.Step.Parameter, results, steps, &record, &form)
		row.Step.Specification = substituteStepSelf(substituteRefs(row.Step.Specification, results, steps, &record, &form), row.Step)
		row.Step.SpecNom = substituteRefs(row.Step.SpecNom, results, steps, &record, &form)
		row.Step.SpecMin = substituteRefs(row.Step.SpecMin, results, steps, &record, &form)
		row.Step.SpecMax = substituteRefs(row.Step.SpecMax, results, steps, &record, &form)
		row.Step.DefaultResult = substituteStepSelf(substituteRefs(row.Step.DefaultResult, results, steps, &record, &form), row.Step)
	}

	h.render(w, "record_edit.html", map[string]any{
		"Form":      form,
		"Record":    record,
		"Rows":      resultRows,
		"CSRFToken": h.csrfToken(w, r),
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
	if !h.verifyCsrf(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}

	// TODO (#207): replace os.Getenv("USERNAME") with authenticated app user once auth is implemented.
	username := os.Getenv("USERNAME")

	res, err := h.execContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET locked=1, updated_at=GETDATE() WHERE ID=@p1 AND locked=0",
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
	if !h.verifyCsrf(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
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

	// TODO (#207): replace os.Getenv("USERNAME") with authenticated app user once auth is implemented.
	username := os.Getenv("USERNAME")

	res, err := h.execContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET locked=0, updated_at=GETDATE() WHERE ID=@p1 AND locked=1",
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

// SaveResults â€" POST /records/{id}/edit
func (h *Handler) SaveResults(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !h.verifyCsrf(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	var record models.TestRecord
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT ID, form_id, serial_number, serial_number_PN, serial_number_PNDesc,
		       record_date, comments, COALESCE(instrument_type,'') AS instrument_type, locked, active, test_order
		FROM %s WHERE ID = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.SerialNumber, &record.SerialNumberPN,
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

	// Load steps for pass_fail computation and INSERT snapshots.
	stepRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, Parameter, Specification, default_result, hide_formula, COALESCE(type,0) AS type,
		       spec_min, spec_max, pf_type, spec_units, spec_nom
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
			&specMin, &specMax, &pfType, &specUnits, &specNom,
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
	type savedResult struct {
		ID      int
		Result  string
		Comment string
	}
	existing := map[int]savedResult{}
	exRows, err := h.queryContext(r.Context(), fmt.Sprintf(
		"SELECT ID, test_id, result, comment FROM %s WHERE record_id = @p1", h.cfg.ResultsTable()), recordID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer exRows.Close()
	for exRows.Next() {
		var rid, tid int
		var result, comment sql.NullString
		if err := exRows.Scan(&rid, &tid, &result, &comment); err != nil {
			continue
		}
		existing[tid] = savedResult{ID: rid, Result: result.String, Comment: comment.String}
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

		var passFail *bool
		if result != "" {
			passFail = models.ComputePassFail(result, step)
		}

		if prev, exists := existing[testID]; exists {
			// Only UPDATE if result or comment actually changed
			if result != prev.Result || comment != prev.Comment {
				// TODO: insert into TestResultHistory here when audit trail is added
				h.execContext(r.Context(), fmt.Sprintf(`
					UPDATE %s SET result=@p1, comment=@p2, pass_fail=@p3, updated_at=GETDATE()
					WHERE ID=@p4`, h.cfg.ResultsTable()),
					result, comment, passFail, prev.ID)
			}
		} else if result != "" || comment != "" {
			h.execContext(r.Context(), fmt.Sprintf(`
				INSERT INTO %s
				  (record_id, test_id, result, comment, pass_fail,
				   parameter, specification, spec_min, spec_nom, spec_max, spec_units, updated_at)
				VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,GETDATE())`,
				h.cfg.ResultsTable()),
				recordID, testID,
				result, comment, passFail,
				step.Parameter, step.Specification,
				step.SpecMin, step.SpecNom, step.SpecMax, step.SpecUnits)
		}
	}

	comments := strings.TrimSpace(r.FormValue("comments"))
	instrumentType := strings.TrimSpace(r.FormValue("instrument_type"))
	if rdStr := r.FormValue("record_date"); rdStr != "" {
		if rd, parseErr := time.Parse("2006-01-02", rdStr); parseErr == nil {
			h.execContext(r.Context(), fmt.Sprintf(
				"UPDATE %s SET record_date=@p1, comments=@p2, instrument_type=@p3, updated_at=GETDATE() WHERE ID=@p4",
				h.cfg.RecordsTable()), rd, comments, instrumentType, recordID)
		} else {
			h.execContext(r.Context(), fmt.Sprintf(
				"UPDATE %s SET comments=@p1, instrument_type=@p2, updated_at=GETDATE() WHERE ID=@p3",
				h.cfg.RecordsTable()), comments, instrumentType, recordID)
		}
	} else {
		h.execContext(r.Context(), fmt.Sprintf(
			"UPDATE %s SET comments=@p1, instrument_type=@p2, updated_at=GETDATE() WHERE ID=@p3",
			h.cfg.RecordsTable()), comments, instrumentType, recordID)
	}

	http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
}
