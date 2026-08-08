package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
//   - {record.pndesc}    → record's unit-under-test description (subject_pn_description / title)
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
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.revision, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE pn.category = 'FORM' AND pn.is_active = %s AND f.is_active = %s
		ORDER BY pn.part_number ASC`,
		h.cfg.FormsTable(), h.cfg.PartsTable(), h.dia().BoolLiteral(true), h.dia().BoolLiteral(true)))
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var forms []models.TestForm
	for rows.Next() {
		var f models.TestForm
		if err := rows.Scan(&f.ID, &f.PartNumberID, &f.IsLocked, &f.Revision, &f.PartNumber, &f.Title); err != nil {
			http.Error(w, "scan error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		forms = append(forms, f)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "rows error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderRecords(w, r, "index.html", map[string]any{
		"Forms":     forms,
		"ActiveTab": "records",
		"TestMode":  h.cfg.TestMode,
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
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, f.revision, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), formID).
		Scan(&form.ID, &form.PartNumberID, &form.IsLocked, &form.TestOrder, &form.Revision, &form.PartNumber, &form.Title)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	lockedCount, _ := strconv.Atoi(r.URL.Query().Get("locked"))

	// Distinct Type (record_type) values for this form, to populate the filter datalist.
	var typeOptions []string
	typeRows, terr := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT DISTINCT record_type FROM %s
		WHERE form_id = @p1 AND is_active = %s AND record_type <> ''
		ORDER BY record_type`, h.cfg.RecordsTable(), h.dia().BoolLiteral(true)), formID)
	if terr == nil {
		defer typeRows.Close()
		for typeRows.Next() {
			var c string
			if err := typeRows.Scan(&c); err != nil {
				log.Printf("RecordsList: type filter scan error: %v", err)
				break
			}
			typeOptions = append(typeOptions, c)
		}
		if err := typeRows.Err(); err != nil {
			log.Printf("RecordsList: type filter rows error: %v", err)
		}
	}

	h.renderRecords(w, r, "records_index.html", map[string]any{
		"Form":        form,
		"TypeOptions": typeOptions,
		"LockedCount": lockedCount,
		"CSRFToken":   h.csrfToken(w, r),
		"ActiveTab":   "records",
		"TestMode":    h.cfg.TestMode,
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

	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, COALESCE(part_id,0), serial_number, subject_part_number, subject_pn_description,
		       record_date, record_type, is_locked, is_approved, form_revision
		FROM %s
		WHERE form_id = @p1 AND is_active = %s
		ORDER BY %s DESC, record_date DESC`,
		h.cfg.RecordsTable(), h.dia().BoolLiteral(true), h.dia().TryCastInt("serial_number")), formID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := make([]row, 0)
	for rows.Next() {
		var rec row
		var recordDate *time.Time
		var formRev *int
		var locked, approved bool
		if err := rows.Scan(&rec.ID, &rec.PartNumberID, &rec.SN, &rec.SNPN, &rec.SNDesc,
			&recordDate, &rec.Type, &locked, &approved, &formRev); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if recordDate != nil {
			rec.Date = recordDate.Format("2006-01-02 15:04")
		}
		switch {
		case approved:
			rec.Status = "approved"
		case locked:
			rec.Status = "complete"
		default:
			rec.Status = "wip"
		}
		rec.FormRev = models.TestRecord{FormRevision: formRev}.FormRevLabel()
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
	FormLabel    string `json:"formLabel"` // "<form part number> — <form title>"
}

// scopedRecordsRows returns every active form_record matching whereCol = id,
// across all forms, for the Part/Lot/Unit records tables (#875). whereCol must
// be "part_id", "lot_id", or "unit_id" — always a caller-supplied constant,
// never request input.
func (h *Handler) scopedRecordsRows(ctx context.Context, whereCol string, id int) ([]scopedRecordRow, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT r.id, COALESCE(r.part_id,0), r.serial_number, r.subject_part_number, r.subject_pn_description,
		       r.record_date, r.record_type, r.is_locked, r.is_approved, r.form_revision,
		       r.form_id, fp.part_number, fp.title
		FROM %s r
		JOIN %s f ON f.id = r.form_id
		JOIN %s fp ON fp.id = f.part_number_id
		WHERE r.%s = @p1 AND r.is_active = %s
		ORDER BY r.record_date DESC`,
		h.cfg.RecordsTable(), h.cfg.FormsTable(), h.cfg.PartsTable(),
		whereCol, h.dia().BoolLiteral(true)), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]scopedRecordRow, 0)
	for rows.Next() {
		var rec scopedRecordRow
		var recordDate *time.Time
		var formRev *int
		var locked, approved bool
		var formPN, formTitle string
		if err := rows.Scan(&rec.ID, &rec.PartNumberID, &rec.SN, &rec.SNPN, &rec.SNDesc,
			&recordDate, &rec.Type, &locked, &approved, &formRev,
			&rec.FormID, &formPN, &formTitle); err != nil {
			return nil, err
		}
		if recordDate != nil {
			rec.Date = recordDate.Format("2006-01-02 15:04")
		}
		switch {
		case approved:
			rec.Status = "approved"
		case locked:
			rec.Status = "complete"
		default:
			rec.Status = "wip"
		}
		rec.FormRev = models.TestRecord{FormRevision: formRev}.FormRevLabel()
		rec.FormLabel = formPN
		if formTitle != "" {
			rec.FormLabel += " — " + formTitle
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// scopedRecordTypeOptions returns distinct non-empty record_type (Type) values for
// the filter-row datalist, scoped the same way as scopedRecordsRows.
func (h *Handler) scopedRecordTypeOptions(ctx context.Context, whereCol string, id int) ([]string, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT DISTINCT record_type FROM %s
		WHERE %s = @p1 AND is_active = %s AND record_type <> ''
		ORDER BY record_type`, h.cfg.RecordsTable(), whereCol, h.dia().BoolLiteral(true)), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
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
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, f.revision, pn.part_number, pn.title
		FROM %s f
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), formID).
		Scan(&form.ID, &form.PartNumberID, &form.IsLocked, &form.TestOrder, &form.Revision, &form.PartNumber, &form.Title)
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
			param, spec, defaultResult, hideFormula sql.NullString
			specMin, specMax, pfType                sql.NullString
			category, sheetName, specUnits, specNom sql.NullString
			instrumentTypes, format                 sql.NullString
			stepComment                             sql.NullString
		)
		if err := stepRows.Scan(
			&s.ID, &s.FormID, &param, &spec, &defaultResult, &hideFormula, &s.Type,
			&specMin, &specMax, &pfType,
			&s.Archived, &archiveID, &revision, &category, &sheetName,
			&specUnits, &specNom, &instrumentTypes,
			&format, &stepComment,
			&s.StepCreatedAt, &s.StepUpdatedAt,
		); err != nil {
			http.Error(w, "scan error: "+err.Error(), http.StatusInternalServerError)
			return
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
	if err := stepRows.Err(); err != nil {
		http.Error(w, "rows error: "+err.Error(), http.StatusInternalServerError)
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

	// Load history timestamps for timeline dots. changed_at is stamped by
	// trg_form_row_history with GETDATE() = UTC on Azure SQL, so the calendar-day
	// bucketing happens in Go in the viewing user's timezone (#847) — SQL-side
	// AT TIME ZONE would need Windows zone names, not the IANA names we store.
	type HistoryPoint struct {
		At      time.Time
		Count   int
		PctLeft float64 // position along timeline bar (5â€"95%)
	}
	loc := h.userLocation(r)
	hRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT changed_at, form_row_id
		FROM %s
		WHERE form_row_id IN (SELECT id FROM %s WHERE form_id = @p1)
		ORDER BY changed_at ASC`,
		h.cfg.FormRowHistoryTable(), h.cfg.StepsTable()), formID)
	var histPoints []HistoryPoint
	if err == nil {
		defer hRows.Close()
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
		for hRows.Next() {
			var changedAt time.Time
			var rowID int
			if err := hRows.Scan(&changedAt, &rowID); err != nil {
				log.Printf("FormDef: history scan error: %v", err)
				break
			}
			local := changedAt.In(loc)
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
		if err := hRows.Err(); err != nil {
			log.Printf("FormDef: history rows error: %v", err)
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
	// `at` is a calendar day in the viewing user's timezone (#847); changed_at is UTC
	// (GETDATE() on Azure SQL), so resolve the day to a half-open UTC range in Go rather
	// than CAST(changed_at AS DATE) — SQL Server's AT TIME ZONE wants Windows zone names,
	// not the IANA names we store.
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

	// see FUTURE_GOALS.md (records index query refactor)
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT t.id,
		       CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.type,0)            ELSE COALESCE(t.type,0)            END,
		       CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.parameter,'')      ELSE COALESCE(t.parameter,'')      END,
		       CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.spec_nom,'')       ELSE COALESCE(t.spec_nom,'')       END,
		       CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.spec_min,'')       ELSE COALESCE(t.spec_min,'')       END,
		       CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.spec_max,'')       ELSE COALESCE(t.spec_max,'')       END,
		       CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.spec_units,'')     ELSE COALESCE(t.spec_units,'')     END,
		       CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.pf_type,'')        ELSE COALESCE(t.pf_type,'')        END,
		       CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.default_result,'') ELSE COALESCE(t.default_result,'') END,
		       CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.hide_formula,'')   ELSE COALESCE(t.hide_formula,'')   END,
		       CASE WHEN h.form_row_id IS NOT NULL THEN 1 ELSE 0 END
		FROM %s t
		LEFT JOIN (
		    SELECT form_row_id, type, parameter, spec_nom, spec_min, spec_max,
		           spec_units, pf_type, default_result, hide_formula
		    FROM %s
		    WHERE changed_at >= @p2 AND changed_at < @p3
		) h ON h.form_row_id = t.id
		WHERE t.form_id = @p1`,
		h.cfg.StepsTable(), h.cfg.FormRowHistoryTable()),
		formID, dayStart.UTC(), dayEnd.UTC())
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
			http.Error(w, "scan error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.Changed = changed == 1
		steps = append(steps, s)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "rows error: "+err.Error(), http.StatusInternalServerError)
		return
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
		Scan(&form.ID, &form.PartNumberID, &form.IsLocked, &form.TestOrder, &form.PartNumber, &form.Title,
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
			http.Error(w, "scan error: "+err.Error(), http.StatusInternalServerError)
			return
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
	if err := stepRows.Err(); err != nil {
		http.Error(w, "rows error: "+err.Error(), http.StatusInternalServerError)
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
		"TestMode":     h.cfg.TestMode,
	})
}

// setAuditUser records the acting username for the current transaction so the
// trg_form_row_history trigger attributes the snapshot to them. The exact
// statement is dialect-specific (SET CONTEXT_INFO on SQL Server, a session GUC on
// Postgres). Best-effort: a failure here only leaves the history row unattributed,
// matching the prior inline behavior, which also ignored the error.
func (h *Handler) setAuditUser(ctx context.Context, tx *txLogger, username string) {
	q, arg := h.dia().SetAuditUser(username)
	tx.ExecContext(ctx, q, arg)
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
	// trg_form_row_history trigger on every UPDATE in this batch.
	tx, err := h.beginTx(r.Context())
	if err != nil {
		http.Error(w, "could not start transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if u := h.currentUser(r); u != nil {
		h.setAuditUser(r.Context(), tx, u.Username)
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

		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
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
		); err != nil {
			http.Error(w, "could not save step: "+err.Error(), http.StatusInternalServerError)
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
		hideFormula := row.Hide
		var newID int
		insertStep := h.dia().InsertReturningID(h.cfg.StepsTable(),
			`form_id, type, parameter, specification, spec_nom, spec_min, spec_max, spec_units,
			 pf_type, default_result, hide_formula, category, sheet_name, instrument_types,
			 format, comment, created_at, updated_at`,
			`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16,GETDATE(),GETDATE()`,
			false)
		if err2 := tx.QueryRowContext(r.Context(), insertStep,
			formID, stepType, row.Parameter, nullOrVal(row.Specification),
			nullOrVal(row.SpecNom), nullOrVal(row.SpecMin), nullOrVal(row.SpecMax),
			nullOrVal(row.SpecUnits), nullOrVal(row.PFType), nullOrVal(row.DefaultResult),
			nullOrVal(hideFormula), nullOrVal(row.Category), nullOrVal(row.SheetName),
			nullOrVal(row.InstrumentTypes), nullOrVal(row.Format), nullOrVal(row.Comment),
		).Scan(&newID); err2 != nil {
			http.Error(w, "could not insert new step: "+err2.Error(), http.StatusInternalServerError)
			return
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
			if _, err := h.execContext(r.Context(), fmt.Sprintf(
				"UPDATE %s SET test_order=@p1 WHERE id=@p2", h.cfg.FormsTable()),
				normalizeOrder(stepOrder), formID); err != nil {
				http.Error(w, "could not save step order: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	// Update form-level record_types and instrument_types if changed.
	newRecordTypes := strings.TrimSpace(r.FormValue("record_types"))
	newInstrTypes := strings.TrimSpace(r.FormValue("instrument_types"))
	if newRecordTypes != r.FormValue("original_record_types") ||
		newInstrTypes != r.FormValue("original_instrument_types") {
		if _, err := h.execContext(r.Context(), fmt.Sprintf(
			"UPDATE %s SET record_types=@p1, instrument_types=@p2 WHERE id=@p3",
			h.cfg.FormsTable()),
			nullOrVal(newRecordTypes), nullOrVal(newInstrTypes), formID); err != nil {
			http.Error(w, "could not save record types: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/def", formID), http.StatusSeeOther)
}

// loadSteps loads all form_row rows for a form keyed by id, with every field used for
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

// loadRecordResults loads the materialized snapshot rows for a record, keyed by form_row_id (#487).
func (h *Handler) loadRecordResults(ctx context.Context, recordID int) (map[int]*models.TestResult, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT id, form_record_id, form_row_id,
		       COALESCE(parameter,''), COALESCE(specification,''), COALESCE(result,''),
		       pass_fail, COALESCE(comment,''),
		       COALESCE(spec_min,''), COALESCE(spec_nom,''), COALESCE(spec_max,''),
		       COALESCE(spec_units,''), COALESCE(pf_type,''), COALESCE(format,''),
		       COALESCE(type,0), COALESCE(hide_formula,''), COALESCE(default_result,''),
		       updated_at
		FROM %s WHERE form_record_id = @p1`, h.cfg.ResultsTable()), recordID)
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
// includeHidden=true keeps steps whose hide_formula currently evaluates to hidden, flagging them
// ResultRow.Hidden instead of dropping them. The edit view passes true so client-side JS can toggle
// visibility live as results change; the read-only/print views pass false.
func (h *Handler) loadFrozenRows(ctx context.Context, record *models.TestRecord, form *models.TestForm, includeHidden bool) ([]models.ResultRow, map[int]*models.TestResult, map[int]*models.TestStep, error) {
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

	var record models.TestRecord
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, COALESCE(part_id,0), serial_number, subject_part_number, subject_pn_description,
		       record_date, record_type, COALESCE(notes,'') AS notes, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_approved, is_active, test_order,
		       lot_id, build_id, unit_id
		FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.PartNumberID, &record.SerialNumber, &record.SerialNumberPN,
			&record.SerialNumberDesc, &record.RecordDate, &record.RecordType, &record.Notes,
			&record.InstrumentType, &record.IsLocked, &record.IsApproved, &record.IsActive, &record.TestOrder,
			&record.LotID, &record.BuildID, &record.UnitID)
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
		Scan(&form.ID, &form.PartNumberID, &form.IsLocked, &form.TestOrder, &form.PartNumber, &form.Title)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build the frozen rows from the materialized snapshot (live def is only a legacy fallback).
	resultRows, results, refSteps, err := h.loadFrozenRows(r.Context(), &record, &form, false)
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
			       LAG(id)  OVER (ORDER BY %s DESC, record_date DESC) AS prev_id,
			       LEAD(id) OVER (ORDER BY %s DESC, record_date DESC) AS next_id
			FROM %s WHERE form_id = @p1 AND is_active = %s
		)
		SELECT COALESCE(prev_id, 0), COALESCE(next_id, 0) FROM ordered WHERE id = @p2`,
		h.dia().TryCastInt("serial_number"), h.dia().TryCastInt("serial_number"), h.cfg.RecordsTable(), h.dia().BoolLiteral(true)), record.FormID, recordID).Scan(&prevID, &nextID)

	var imageRows []models.ResultRow
	for _, row := range resultRows {
		if row.Level == 0 && isImageRow(row) {
			imageRows = append(imageRows, row)
		}
	}

	// Lifecycle audit trail (#250) — complete/approve/unlock events, oldest first.
	var events []models.RecordEvent
	eventRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_record_id, event_type, username, event_date, COALESCE(comments,'')
		FROM %s WHERE form_record_id = @p1 ORDER BY event_date ASC, id ASC`,
		h.cfg.RecordEventsTable()), recordID)
	if err != nil {
		log.Printf("RecordDetail: audit trail query error: %v", err)
	} else {
		for eventRows.Next() {
			var ev models.RecordEvent
			if err := eventRows.Scan(&ev.ID, &ev.TestRecordID, &ev.EventType, &ev.Username,
				&ev.EventDate, &ev.Comments); err != nil {
				log.Printf("RecordDetail: audit trail scan error: %v", err)
				break
			}
			events = append(events, ev)
		}
		if err := eventRows.Err(); err != nil {
			log.Printf("RecordDetail: audit trail rows error: %v", err)
		}
		eventRows.Close()
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
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
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
		"TestMode":          h.cfg.TestMode,
		"DebugMode":         h.cfg.DebugMode,
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
		SELECT id, form_id, COALESCE(part_id,0), serial_number, subject_part_number, subject_pn_description,
		       record_date, record_type, COALESCE(notes,'') AS notes, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_approved, is_active, test_order
		FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.PartNumberID, &record.SerialNumber, &record.SerialNumberPN,
			&record.SerialNumberDesc, &record.RecordDate, &record.RecordType, &record.Notes,
			&record.InstrumentType, &record.IsLocked, &record.IsApproved, &record.IsActive, &record.TestOrder)
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
		Scan(&form.ID, &form.PartNumberID, &form.IsLocked, &form.TestOrder, &form.PartNumber, &form.Title)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build the frozen rows from the materialized snapshot (live def is only a legacy fallback).
	resultRows, results, refSteps, err := h.loadFrozenRows(r.Context(), &record, &form, false)
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
		"TestMode":  h.cfg.TestMode,
	})
}

// BOMPart is one row from the form's BOM (PL → PN join).
type BOMPart struct {
	PartNumberID int
	PartNumber   string
	Title        string
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
		Scan(&form.ID, &form.PartNumberID, &form.IsLocked, &form.TestOrder, &form.PartNumber, &form.Title, &recordTypes, &instrumentTypes)
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
		h.cfg.BOMTable(), h.cfg.PartsTable()), form.PartNumberID)
	if err == nil {
		defer bomRows.Close()
		for bomRows.Next() {
			var p BOMPart
			if bomRows.Scan(&p.PartNumberID, &p.PartNumber, &p.Title) == nil {
				bomParts = append(bomParts, p)
			}
		}
	}

	// Next serial number: max numeric SN + 1, defaulting to 1 if none exist.
	// This is only a suggestion shown in the form; CreateRecord re-derives the SN
	// atomically under a lock when the user accepts it, closing the concurrent-create race (#369).
	var nextSN sql.NullInt64
	h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT COALESCE(MAX(%s) + 1, 1)
		FROM %s WHERE form_id = @p1`, h.dia().TryCastInt("serial_number"), h.cfg.RecordsTable()), formID).Scan(&nextSN)

	nextSNStr := "1"
	if nextSN.Valid {
		nextSNStr = strconv.FormatInt(nextSN.Int64, 10)
	}

	h.renderRecords(w, r, "record_new.html", map[string]any{
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
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, f.revision, pn.part_number, pn.title
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), formID).
		Scan(&form.ID, &form.PartNumberID, &form.IsLocked, &form.TestOrder, &form.Revision, &form.PartNumber, &form.Title)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	serialNumber := strings.TrimSpace(r.FormValue("serial_number"))
	suggestedSN := strings.TrimSpace(r.FormValue("suggested_serial_number"))
	recordType := strings.TrimSpace(r.FormValue("record_type"))
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

	// SN allocation is atomic when the user accepted the suggested default (#369).
	// Re-derive the next per-form SN inside the transaction under a lock so two
	// concurrent creates for the same form get N and N+1, not the same value.
	// A user-typed override (serialNumber != suggestedSN) is inserted as-is.
	if isAutoSerial(serialNumber, suggestedSN) {
		var nextSN int
		// WITH (UPDLOCK, HOLDLOCK) is a SQL Server locking hint with no textual
		// Postgres equivalent; the #369 concurrency guarantee has no Postgres
		// story yet (needs a design decision: advisory lock / SERIALIZABLE /
		// dedicated sequence) — tracked as a follow-up for the #625 cutover.
		err = tx.QueryRowContext(r.Context(), fmt.Sprintf(`
			SELECT COALESCE(MAX(%s), 0) + 1
			FROM %s WITH (UPDLOCK, HOLDLOCK) WHERE form_id = @p1`,
			h.dia().TryCastInt("serial_number"), h.cfg.RecordsTable()), formID).Scan(&nextSN)
		if err != nil {
			http.Error(w, "serial number error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		serialNumber = strconv.Itoa(nextSN)
	}

	var newID int
	insertRecord := h.dia().InsertReturningID(h.cfg.RecordsTable(),
		`form_id, part_id, serial_number, subject_part_number, subject_pn_description,
		 record_type, instrument_type, test_order, record_date, created_at, is_active, is_locked, form_revision`,
		fmt.Sprintf(`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,GETDATE(),%s,%s,@p10`,
			h.dia().BoolLiteral(true), h.dia().BoolLiteral(false)),
		false)
	err = tx.QueryRowContext(r.Context(), insertRecord,
		formID, partNumberID, serialNumber, snPN, snDesc, recordType, instrumentType, form.TestOrder, recordDate, form.Revision).Scan(&newID)
	if err != nil {
		http.Error(w, "insert error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Freeze the record from the start: materialize a snapshot row per applicable step (#487).
	rec := models.TestRecord{
		ID: newID, FormID: formID, SerialNumber: serialNumber,
		SerialNumberPN: snPN, SerialNumberDesc: snDesc, RecordType: recordType,
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

// materializeRecordSteps creates a frozen result snapshot row for every applicable step in the
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
			  (form_record_id, form_row_id, type, parameter, specification, spec_min, spec_nom, spec_max,
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
		var ulot, ubuild sql.NullInt64
		if err := h.queryRowContext(ctx, fmt.Sprintf(
			`SELECT lot_id, build_id FROM %s WHERE id = @p1`, h.cfg.UnitTable()), *record.UnitID).Scan(&ulot, &ubuild); err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if ulot.Valid {
			v := int(ulot.Int64)
			lotID = &v
		}
		if ubuild.Valid {
			v := int(ubuild.Int64)
			buildID = &v
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
	var trackingMode sql.NullString
	var bomCount int
	err := h.queryRowContext(ctx, fmt.Sprintf(`
		SELECT p.tracking_mode, (SELECT COUNT(*) FROM %s b WHERE b.parent_part_id = p.id)
		FROM %s p WHERE p.id = @p1`, h.cfg.BOMTable(), h.cfg.PartsTable()), record.PartNumberID).
		Scan(&trackingMode, &bomCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.IsLotTracked = models.TracksLots(trackingMode.String)
	t.TracksSerials = models.TracksSerials(trackingMode.String)
	t.Buildable = bomCount > 0

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
// Each returned value is the chosen id, or nil to clear the link when the field is
// blank. A non-blank id that doesn't belong to the part yields an error (surfaced as a
// 400). Unlike the build's component-lot check, a lot need not be active here — a record
// may legitimately reference a since-retired lot.
func (h *Handler) recordLinkageArgs(r *http.Request, partID int) (lotArg, buildArg interface{}, err error) {
	ctx := r.Context()
	belongs := func(table, v string) (interface{}, error) {
		id, convErr := strconv.Atoi(v)
		if convErr != nil || id <= 0 {
			return nil, fmt.Errorf("invalid selection")
		}
		var n int
		if e := h.queryRowContext(ctx, fmt.Sprintf(
			`SELECT COUNT(*) FROM %s WHERE id = @p1 AND part_id = @p2`, table), id, partID).Scan(&n); e != nil {
			return nil, e
		}
		if n != 1 {
			return nil, fmt.Errorf("selection does not belong to this record's part")
		}
		return id, nil
	}
	if v := fv(r, "lot_id"); v != "" {
		if lotArg, err = belongs(h.cfg.LotTable(), v); err != nil {
			return nil, nil, err
		}
	}
	if v := fv(r, "build_id"); v != "" {
		if buildArg, err = belongs(h.cfg.BuildTable(), v); err != nil {
			return nil, nil, err
		}
	}
	return lotArg, buildArg, nil
}

// upsertUnitForRecord finds or lazily creates the serialized unit a serial/lot_serial
// part's test record refers to (#745, Q5). The (part_id, serial) pair uniquely
// identifies the unit, so a retest — a second record with the same serial — re-links
// the existing unit rather than minting a duplicate. Provenance (buildID, lotID; each
// an int or nil, as returned by recordLinkageArgs) is set only on creation; a
// test-minted unit should always have at least one set (the Q5 invariant), so the
// caller skips the upsert when both are nil — an app-level rule only, not a DB CHECK:
// CK_unit_provenance was dropped by migrate_799_unit_source.sql (#799), since a
// `manual` unit legitimately has neither. tx-accepting so a build-at-test-time save
// (#747) can mint the unit in the same transaction as the build.
func (h *Handler) upsertUnitForRecord(ctx context.Context, tx *txLogger, partID int, serial string, buildID, lotID interface{}) (int, error) {
	var unitID int
	err := tx.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT id FROM %s WHERE part_id = @p1 AND serial_number = @p2`, h.cfg.UnitTable()), partID, serial).Scan(&unitID)
	if err == nil {
		return unitID, nil // existing unit (retest / re-save) — reuse, never duplicate
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	insertUnit := h.dia().InsertReturningID(h.cfg.UnitTable(),
		`part_id, serial_number, build_id, lot_id, source`, `@p1,@p2,@p3,@p4,'test'`, false)
	if err := tx.QueryRowContext(ctx, insertUnit, partID, serial, buildID, lotID).Scan(&unitID); err != nil {
		return 0, err
	}
	return unitID, nil
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
		SELECT id, form_id, COALESCE(part_id,0), serial_number, subject_part_number, subject_pn_description,
		       record_date, record_type, COALESCE(notes,'') AS notes, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_approved, is_active, test_order,
		       lot_id, build_id, unit_id
		FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.PartNumberID, &record.SerialNumber, &record.SerialNumberPN,
			&record.SerialNumberDesc, &record.RecordDate, &record.RecordType, &record.Notes,
			&record.InstrumentType, &record.IsLocked, &record.IsApproved, &record.IsActive, &record.TestOrder,
			&record.LotID, &record.BuildID, &record.UnitID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if record.IsLocked {
		http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
		return
	}

	var form models.TestForm
	var editInstrumentTypes sql.NullString
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, f.is_locked, f.test_order, pn.part_number, pn.title, f.instrument_types
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), record.FormID).
		Scan(&form.ID, &form.PartNumberID, &form.IsLocked, &form.TestOrder, &form.PartNumber, &form.Title, &editInstrumentTypes)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	form.InstrumentTypes = editInstrumentTypes.String

	// Build the frozen rows from the materialized snapshot (live def is only a legacy fallback).
	// Edit always works against the frozen spec; "Update to latest" is the only re-pull path (#487).
	// includeHidden=true: render conditionally-hidden steps (display:none) so JS can toggle them live (#257).
	resultRows, results, refSteps, err := h.loadFrozenRows(r.Context(), &record, &form, true)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderRecords(w, r, "record_edit.html", map[string]any{
		"Form":                form,
		"Record":              record,
		"Rows":                resultRows,
		"Trace":               trace,
		"CSRFToken":           h.csrfToken(w, r),
		"ActiveTab":           "records",
		"TestMode":            h.cfg.TestMode,
		"ImageRootConfigured": h.cfg.ImageRoot != "",
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
		status := http.StatusInternalServerError
		if errors.Is(err, errRecordNeedsLot) {
			status = http.StatusBadRequest
		}
		http.Error(w, "lock error: "+err.Error(), status)
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

	res, err := h.execContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET is_approved=%s, updated_at=GETDATE() WHERE id=@p1 AND is_locked=%s AND is_approved=%s",
		h.cfg.RecordsTable(), h.dia().BoolLiteral(true), h.dia().BoolLiteral(true), h.dia().BoolLiteral(false)), recordID)
	if err != nil {
		http.Error(w, "approve error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if n, _ := res.RowsAffected(); n > 0 {
		if _, err := h.execContext(r.Context(), fmt.Sprintf(
			"INSERT INTO %s (form_record_id, event_type, username, event_date) VALUES (@p1, 'approved', @p2, GETDATE())",
			h.cfg.RecordEventsTable()), recordID, u.Username); err != nil {
			http.Error(w, "approve error: "+err.Error(), http.StatusInternalServerError)
			return
		}
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

	// Approved records may only be unlocked by a TR reviewer.
	var isApproved bool
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(
		"SELECT is_approved FROM %s WHERE id=@p1", h.cfg.RecordsTable()), recordID).
		Scan(&isApproved); err != nil {
		http.Error(w, "unlock error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	u := h.currentUser(r)
	if isApproved && (u == nil || !u.CanApproveRecords) {
		http.Error(w, "only a TR reviewer can unlock an approved record", http.StatusForbidden)
		return
	}

	username := ""
	if u != nil {
		username = u.Username
	}

	res, err := h.execContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET is_locked=%s, is_approved=%s, updated_at=GETDATE() WHERE id=@p1 AND is_locked=%s",
		h.cfg.RecordsTable(), h.dia().BoolLiteral(false), h.dia().BoolLiteral(false), h.dia().BoolLiteral(true)), recordID)
	if err != nil {
		http.Error(w, "unlock error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if n, _ := res.RowsAffected(); n > 0 {
		if _, err := h.execContext(r.Context(), fmt.Sprintf(
			"INSERT INTO %s (form_record_id, event_type, username, event_date, comments) VALUES (@p1, 'unlocked', @p2, GETDATE(), @p3)",
			h.cfg.RecordEventsTable()), recordID, username, comment); err != nil {
			http.Error(w, "unlock error: "+err.Error(), http.StatusInternalServerError)
			return
		}
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

	// Load source record — same SELECT as RecordDetail.
	var src models.TestRecord
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, COALESCE(part_id,0), serial_number, subject_part_number, subject_pn_description,
		       record_date, record_type, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_approved, is_active, test_order, unit_id
		FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&src.ID, &src.FormID, &src.PartNumberID, &src.SerialNumber, &src.SerialNumberPN,
			&src.SerialNumberDesc, &src.RecordDate, &src.RecordType,
			&src.InstrumentType, &src.IsLocked, &src.IsApproved, &src.IsActive, &src.TestOrder, &src.UnitID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		http.Error(w, "could not start transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// INSERT new form_record; part_id is NULL when PartNumberID == 0 (mirrors CreateRecord).
	var partNumberID *int
	if src.PartNumberID != 0 {
		partNumberID = &src.PartNumberID
	}

	// A duplicate is a fresh re-test, so it captures the CURRENT form revision, not the
	// source record's frozen form_revision (#260).
	var formRevision int
	if err := tx.QueryRowContext(r.Context(), fmt.Sprintf(
		"SELECT revision FROM %s WHERE id=@p1", h.cfg.FormsTable()), src.FormID).Scan(&formRevision); err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var newID int
	// record_date is set to now — a duplicate is a fresh re-test, dated the day it's made.
	// unit_id is carried over unchanged: a retest points at the SAME serialized unit as
	// the source record (#745, design §2/§5), never a new unit row.
	insertDupRecord := h.dia().InsertReturningID(h.cfg.RecordsTable(),
		`form_id, part_id, serial_number, subject_part_number, subject_pn_description,
		 record_type, instrument_type, test_order, record_date, created_at, is_active, is_locked, is_approved, form_revision, unit_id`,
		fmt.Sprintf(`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,GETDATE(),GETDATE(),%s,%s,%s,@p9,@p10`,
			h.dia().BoolLiteral(true), h.dia().BoolLiteral(false), h.dia().BoolLiteral(false)),
		false)
	err = tx.QueryRowContext(r.Context(), insertDupRecord,
		src.FormID, partNumberID, src.SerialNumber, src.SerialNumberPN, src.SerialNumberDesc,
		src.RecordType, src.InstrumentType, src.TestOrder, formRevision, src.UnitID).Scan(&newID)
	if err != nil {
		http.Error(w, "insert error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Copy all result rows from the source record into the new record.
	resRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT form_row_id, type, parameter, specification, spec_min, spec_nom, spec_max,
		       spec_units, pf_type, format, hide_formula, default_result, result, comment, pass_fail
		FROM %s WHERE form_record_id = @p1`, h.cfg.ResultsTable()), recordID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resRows.Close()

	for resRows.Next() {
		var (
			testID                                     int
			rowType                                    int
			parameter, specification                   sql.NullString
			specMin, specNom, specMax, specUnits       sql.NullString
			pfType, format, hideFormula, defaultResult sql.NullString
			result, comment                            sql.NullString
			passFail                                   sql.NullBool
		)
		if err := resRows.Scan(
			&testID, &rowType, &parameter, &specification,
			&specMin, &specNom, &specMax, &specUnits,
			&pfType, &format, &hideFormula, &defaultResult,
			&result, &comment, &passFail,
		); err != nil {
			http.Error(w, "scan error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		var pf *bool
		if passFail.Valid {
			v := passFail.Bool
			pf = &v
		}
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
			INSERT INTO %s
			  (form_record_id, form_row_id, type, parameter, specification, spec_min, spec_nom, spec_max,
			   spec_units, pf_type, format, hide_formula, default_result, result, comment, pass_fail, updated_at)
			VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16,GETDATE())`,
			h.cfg.ResultsTable()),
			newID, testID, rowType, parameter, specification,
			specMin, specNom, specMax, specUnits,
			pfType, format, hideFormula, defaultResult,
			result, comment, pf,
		); err != nil {
			http.Error(w, "insert error: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := resRows.Err(); err != nil {
		http.Error(w, "rows error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "commit error: "+err.Error(), http.StatusInternalServerError)
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

	res, err := h.execContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET is_locked=%s, revision = revision + 1 WHERE id=@p1 AND is_locked=%s",
		h.cfg.FormsTable(), h.dia().BoolLiteral(true), h.dia().BoolLiteral(false)), formID)
	if err != nil {
		http.Error(w, "lock error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if n, _ := res.RowsAffected(); n > 0 {
		if _, err := h.execContext(r.Context(), fmt.Sprintf(
			"INSERT INTO %s (form_id, event_type, username, event_date) VALUES (@p1, 'locked', @p2, GETDATE())",
			h.cfg.FormEventsTable()), formID, username); err != nil {
			http.Error(w, "lock error: "+err.Error(), http.StatusInternalServerError)
			return
		}
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
		"UPDATE %s SET is_locked=%s WHERE id=@p1 AND is_locked=%s",
		h.cfg.FormsTable(), h.dia().BoolLiteral(false), h.dia().BoolLiteral(true)), formID)
	if err != nil {
		http.Error(w, "unlock error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if n, _ := res.RowsAffected(); n > 0 {
		if _, err := h.execContext(r.Context(), fmt.Sprintf(
			"INSERT INTO %s (form_id, event_type, username, event_date, comments) VALUES (@p1, 'unlocked', @p2, GETDATE(), @p3)",
			h.cfg.FormEventsTable()), formID, username, comment); err != nil {
			http.Error(w, "unlock error: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/def", formID), http.StatusSeeOther)
}

// ArchiveStep — POST /forms/{id}/tests/{testID}/archive
// Toggles form_row.archived for a single step. Form field "archived"=1 archives,
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
	archived := r.FormValue("archived") == "1"

	tx, err := h.beginTx(r.Context())
	if err != nil {
		http.Error(w, "could not start transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if u := h.currentUser(r); u != nil {
		h.setAuditUser(r.Context(), tx, u.Username)
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
		SELECT id, form_id, COALESCE(part_id,0), serial_number, subject_part_number, subject_pn_description,
		       record_date, record_type, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_approved, is_active, test_order,
		       unit_id, build_id
		FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.PartNumberID, &record.SerialNumber, &record.SerialNumberPN,
			&record.SerialNumberDesc, &record.RecordDate, &record.RecordType,
			&record.InstrumentType, &record.IsLocked, &record.IsApproved, &record.IsActive, &record.TestOrder,
			&record.UnitID, &record.BuildID)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if record.IsLocked {
		http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
		return
	}

	// Form context for resolving {form.X} tokens when snapshotting (#487).
	var form models.TestForm
	h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, pn.part_number
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), record.FormID).
		Scan(&form.ID, &form.PartNumberID, &form.PartNumber)

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
			http.Error(w, "scan error: "+err.Error(), http.StatusInternalServerError)
			return
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
	if err := stepRows.Err(); err != nil {
		http.Error(w, "rows error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Load existing results keyed by form_row_id â€" used for change detection and UPDATE vs INSERT.
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
		"SELECT id, form_row_id, result, comment, COALESCE(spec_min,''), COALESCE(spec_max,''), COALESCE(pf_type,'') FROM %s WHERE form_record_id = @p1", h.cfg.ResultsTable()), recordID)
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
			http.Error(w, "scan error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		existing[tid] = savedResult{ID: rid, Result: result.String, Comment: comment.String,
			SpecMin: specMin, SpecMax: specMax, PFType: pfType}
	}
	if err := exRows.Err(); err != nil {
		http.Error(w, "rows error: "+err.Error(), http.StatusInternalServerError)
		return
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
				if _, err := h.execContext(r.Context(), fmt.Sprintf(`
					UPDATE %s SET result=@p1, comment=@p2, pass_fail=@p3, updated_at=GETDATE()
					WHERE id=@p4`, h.cfg.ResultsTable()),
					result, comment, passFail, prev.ID); err != nil {
					http.Error(w, "could not save result: "+err.Error(), http.StatusInternalServerError)
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
			if _, err := h.execContext(r.Context(), fmt.Sprintf(`
				INSERT INTO %s
				  (form_record_id, form_row_id, result, comment, pass_fail, type,
				   parameter, specification, spec_min, spec_nom, spec_max, spec_units, pf_type, format,
				   hide_formula, default_result, updated_at)
				VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16,GETDATE())`,
				h.cfg.ResultsTable()),
				recordID, testID,
				result, comment, passFail, step.Type,
				step.Parameter, step.Specification,
				step.SpecMin, step.SpecNom, step.SpecMax, step.SpecUnits, step.PFType, step.Format,
				step.HideFormula, step.DefaultResult); err != nil {
				http.Error(w, "could not save result: "+err.Error(), http.StatusInternalServerError)
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
		http.Error(w, linkErr.Error(), http.StatusBadRequest)
		return
	}

	// #745: a serial/lot_serial part mints (or, on retest, re-links) the serialized
	// unit this record refers to when saved with provenance (a picked build or lot).
	// The record then points at the unit via unit_id and leaves lot_id/build_id NULL —
	// lot/build are read through the unit (Q8 FK-consistency invariant).
	var trackingMode string
	if e := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT tracking_mode FROM %s WHERE id = @p1`, h.cfg.PartsTable()), record.PartNumberID).Scan(&trackingMode); e != nil && e != sql.ErrNoRows {
		http.Error(w, "could not read part tracking mode: "+e.Error(), http.StatusInternalServerError)
		return
	}

	var rd *time.Time
	if rdStr := r.FormValue("record_date"); rdStr != "" {
		if t, e := time.Parse("2006-01-02T15:04", rdStr); e == nil {
			rd = &t
		} else if t, e := time.Parse("2006-01-02", rdStr); e == nil {
			rd = &t
		}
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		http.Error(w, "could not start transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

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
				http.Error(w, qerr.Error(), http.StatusBadRequest)
				return
			}
			qty = parsedQty
		}
		outputLotTracked := models.TracksLots(trackingMode)
		lines, lerr := h.loadBuildLines(r.Context(), record.PartNumberID)
		if lerr != nil {
			http.Error(w, "could not load BOM: "+lerr.Error(), http.StatusInternalServerError)
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
		buildDate := time.Now()
		if rd != nil {
			buildDate = *rd
		}
		bID, outLot, berr := h.performBuild(r, tx, record.PartNumberID, outputLotTracked, qty, buildDate, "", lines, lotPicks)
		if berr != nil {
			http.Error(w, "could not build: "+berr.Error(), http.StatusInternalServerError)
			return
		}
		buildArg = bID // the new build is this unit's provenance, overriding any dropdown pick
		if outputLotTracked {
			lotArg = outLot
		}
	}

	// #872: the lot-note box on this page contributes an append-only delta — only the new
	// text is posted and appendLotNote concatenates server-side, so two testers with this
	// page open for a whole session both land their line instead of one overwriting the
	// other's page-load copy. Placed here so a note can follow a lot the build above just
	// created, and while lotArg still holds the lot — the Q8 reset below nils it out.
	if lotNote := strings.TrimSpace(r.FormValue("lot_note")); lotNote != "" {
		if lotID, ok := lotArg.(int); ok {
			username := ""
			if u := h.currentUser(r); u != nil {
				username = u.Username
			}
			if err := h.appendLotNote(r.Context(), tx, lotID, lotNote, username); err != nil {
				http.Error(w, "could not save lot note: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	var unitArg interface{}
	if record.UnitID != nil {
		// Already linked to a unit — reuse it rather than re-deriving from
		// serial_number. A unit's serial can be edited after the fact (#799, Part →
		// Units), which would otherwise desync it from record.SerialNumber; re-deriving
		// by serial on every save would then silently mint a duplicate unit and orphan
		// the original (#876). There is no UI to change a record's serial_number after
		// creation, so the record<->unit link, once set, is authoritative.
		unitArg = *record.UnitID
	} else if models.TracksSerials(trackingMode) && record.SerialNumber != "" && (buildArg != nil || lotArg != nil) {
		uid, uerr := h.upsertUnitForRecord(r.Context(), tx, record.PartNumberID, record.SerialNumber, buildArg, lotArg)
		if uerr != nil {
			http.Error(w, "could not record unit: "+uerr.Error(), http.StatusInternalServerError)
			return
		}
		unitArg = uid
	}
	if unitArg != nil {
		lotArg, buildArg = nil, nil // Q8: provenance lives on the unit, not the record
	}
	if rd != nil {
		_, err = tx.ExecContext(r.Context(), fmt.Sprintf(
			"UPDATE %s SET record_date=@p1, record_type=@p2, notes=@p3, instrument_type=@p4, lot_id=@p5, build_id=@p6, unit_id=@p7, updated_at=GETDATE() WHERE id=@p8",
			h.cfg.RecordsTable()), *rd, recordType, nullableText(notes), instrumentType, lotArg, buildArg, unitArg, recordID)
	} else {
		_, err = tx.ExecContext(r.Context(), fmt.Sprintf(
			"UPDATE %s SET record_type=@p1, notes=@p2, instrument_type=@p3, lot_id=@p4, build_id=@p5, unit_id=@p6, updated_at=GETDATE() WHERE id=@p7",
			h.cfg.RecordsTable()), recordType, nullableText(notes), instrumentType, lotArg, buildArg, unitArg, recordID)
	}
	if err != nil {
		http.Error(w, "could not save record: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "commit error: "+err.Error(), http.StatusInternalServerError)
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

	var record models.TestRecord
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, form_id, COALESCE(part_id,0), serial_number, subject_part_number, subject_pn_description,
		       record_date, record_type, COALESCE(instrument_type,'') AS instrument_type, is_locked, is_approved, is_active, test_order
		FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recordID).
		Scan(&record.ID, &record.FormID, &record.PartNumberID, &record.SerialNumber, &record.SerialNumberPN,
			&record.SerialNumberDesc, &record.RecordDate, &record.RecordType,
			&record.InstrumentType, &record.IsLocked, &record.IsApproved, &record.IsActive, &record.TestOrder)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if record.IsLocked {
		http.Redirect(w, r, fmt.Sprintf("/records/%d", recordID), http.StatusSeeOther)
		return
	}

	var form models.TestForm
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT f.id, f.part_number_id, COALESCE(f.test_order,''), f.revision, pn.part_number
		FROM %s f JOIN %s pn ON f.part_number_id = pn.id WHERE f.id = @p1`,
		h.cfg.FormsTable(), h.cfg.PartsTable()), record.FormID).
		Scan(&form.ID, &form.PartNumberID, &form.TestOrder, &form.Revision, &form.PartNumber); err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Live definition (full) and the record's existing snapshot rows.
	steps, err := h.loadSteps(r.Context(), record.FormID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	existing := map[int]int{}                  // form_row_id -> result row id
	curResults := map[int]*models.TestResult{} // form_row_id -> recorded value (for {id} P/F resolution)
	resRows, err := h.queryContext(r.Context(), fmt.Sprintf(
		"SELECT id, form_row_id, COALESCE(result,'') FROM %s WHERE form_record_id = @p1", h.cfg.ResultsTable()), recordID)
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for resRows.Next() {
		var id, tid int
		var result string
		if err := resRows.Scan(&id, &tid, &result); err != nil {
			resRows.Close()
			http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		existing[tid] = id
		curResults[tid] = &models.TestResult{ID: id, TestID: tid, Result: result}
	}
	if err := resRows.Err(); err != nil {
		resRows.Close()
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
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
				  (form_record_id, form_row_id, type, parameter, specification, spec_min, spec_nom, spec_max,
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
	// Re-pulling the live definition also re-captures the form's current revision (#260):
	// the snapshot now represents that revision.
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
		"UPDATE %s SET test_order=@p1, form_revision=@p2, updated_at=GETDATE() WHERE id=@p3", h.cfg.RecordsTable()),
		form.TestOrder, form.Revision, recordID); err != nil {
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
	PartNumberID int
	PartNumber   string
	Title        string
}

// formPNList returns FORM-category PNs that don't already have an active form.
// Used by both NewForm and DuplicateForm to populate the PN picker.
func (h *Handler) formPNList(ctx context.Context) ([]formPN, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT id, part_number, title
		FROM %s
		WHERE category = 'FORM' AND is_active = %s
		  AND NOT EXISTS (
		      SELECT 1 FROM %s WHERE part_number_id = %s.id AND is_active = %s
		  )
		ORDER BY part_number`,
		h.cfg.PartsTable(), h.dia().BoolLiteral(true), h.cfg.FormsTable(), h.cfg.PartsTable(), h.dia().BoolLiteral(true)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []formPN
	for rows.Next() {
		var pn formPN
		if err := rows.Scan(&pn.PartNumberID, &pn.PartNumber, &pn.Title); err != nil {
			return nil, err
		}
		list = append(list, pn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

// copyFormSteps copies all test steps from sourceID into newFormID (within tx)
// and sets test_order on the new form. Returns an error on any failure.
func (h *Handler) copyFormSteps(ctx context.Context, tx *txLogger, sourceID, newFormID int) error {
	var sourceOrder string
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT COALESCE(test_order,'') FROM %s WHERE id=@p1", h.cfg.FormsTable()), sourceID).
		Scan(&sourceOrder); err != nil {
		return err
	}

	stepRows, err := tx.QueryContext(ctx, fmt.Sprintf(`
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
	if err := stepRows.Err(); err != nil {
		return err
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
		insertCopiedStep := h.dia().InsertReturningID(h.cfg.StepsTable(),
			`form_id, type, parameter, specification, spec_nom, spec_min, spec_max,
			 spec_units, pf_type, default_result, hide_formula,
			 category, sheet_name, instrument_types, format, comment,
			 archive_id, revision`,
			`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16,@p17,@p18`,
			false)
		if err := tx.QueryRowContext(ctx, insertCopiedStep,
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
		WHERE f.is_active = %s ORDER BY pn.part_number`,
		h.cfg.FormsTable(), h.cfg.PartsTable(), h.dia().BoolLiteral(true)))
	if err != nil {
		http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var sourceForms []models.TestForm
	for rows.Next() {
		var f models.TestForm
		if err := rows.Scan(&f.ID, &f.PartNumber, &f.Title); err != nil {
			log.Printf("NewForm: source-forms scan error: %v", err)
			break
		}
		sourceForms = append(sourceForms, f)
	}
	if err := rows.Err(); err != nil {
		log.Printf("NewForm: source-forms rows error: %v", err)
	}

	h.renderRecords(w, r, "form_new.html", map[string]any{
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
		"SELECT COUNT(1) FROM %s WHERE id=@p1 AND category='FORM' AND is_active=%s",
		h.cfg.PartsTable(), h.dia().BoolLiteral(true)), pnid).Scan(&exists); err != nil || exists == 0 {
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

	tx, err := h.beginTx(r.Context())
	if err != nil {
		http.Error(w, "tx error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var newID int
	insertForm := h.dia().InsertReturningID(h.cfg.FormsTable(),
		"part_number_id, is_active, is_locked, test_order, record_types, instrument_types",
		fmt.Sprintf("@p1, %s, %s, '', @p2, @p3", h.dia().BoolLiteral(true), h.dia().BoolLiteral(false)),
		false)
	if err := tx.QueryRowContext(r.Context(), insertForm,
		pnid, srcRecordTypes, srcInstrTypes).Scan(&newID); err != nil {
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
		Scan(&form.ID, &form.PartNumberID, &form.IsLocked, &form.TestOrder, &form.PartNumber, &form.Title)
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

	h.renderRecords(w, r, "form_duplicate.html", map[string]any{
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
		"SELECT COUNT(1) FROM %s WHERE id=@p1 AND category='FORM' AND is_active=%s",
		h.cfg.PartsTable(), h.dia().BoolLiteral(true)), pnid).Scan(&exists); err != nil || exists == 0 {
		http.Error(w, "invalid part number", http.StatusBadRequest)
		return
	}

	// Carry over form-level settings from the source form.
	var srcRecordTypes, srcInstrTypes sql.NullString
	h.queryRowContext(r.Context(), fmt.Sprintf(
		"SELECT record_types, instrument_types FROM %s WHERE id=@p1",
		h.cfg.FormsTable()), sourceID).Scan(&srcRecordTypes, &srcInstrTypes)

	tx, err := h.beginTx(r.Context())
	if err != nil {
		http.Error(w, "tx error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var newFormID int
	insertDupForm := h.dia().InsertReturningID(h.cfg.FormsTable(),
		"part_number_id, is_active, is_locked, test_order, record_types, instrument_types",
		fmt.Sprintf("@p1, %s, %s, '', @p2, @p3", h.dia().BoolLiteral(true), h.dia().BoolLiteral(false)),
		false)
	if err := tx.QueryRowContext(r.Context(), insertDupForm,
		pnid, srcRecordTypes, srcInstrTypes).Scan(&newFormID); err != nil {
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
		Scan(&form.ID, &form.PartNumberID, &form.IsLocked, &form.TestOrder, &form.PartNumber, &form.Title)
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
		SELECT trec.id, trec.serial_number, COALESCE(trec.subject_part_number,''),
		       COALESCE(trec.part_id,0),
		       trec.record_date, trec.is_locked,
		       COALESCE(res.result,''), res.pass_fail, COALESCE(res.comment,''),
		       res.updated_at
		FROM %s res
		JOIN %s trec ON res.form_record_id = trec.id
		WHERE res.form_row_id = @p1 AND trec.form_id = @p2 AND trec.is_active = %s
		ORDER BY %s DESC, trec.record_date DESC`,
		h.cfg.ResultsTable(), h.cfg.RecordsTable(), h.dia().BoolLiteral(true), h.dia().TryCastInt("trec.serial_number")), testID, formID)
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
			http.Error(w, "scan error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		rows = append(rows, row)
	}
	if err := resRows.Err(); err != nil {
		http.Error(w, "rows error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderRecords(w, r, "test_report.html", map[string]any{
		"Form":      form,
		"Step":      step,
		"Rows":      rows,
		"ActiveTab": "records",
		"TestMode":  h.cfg.TestMode,
	})
}
