package main

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"arx/arx_go/models"
)

// Step expression engine (#468): the one place that decides what a test step's definition fields resolve
// to on a record. Save, view, print, resync, the lock snapshot and the definition view all go through it.
//
// Column rules:
//
//	Parameter, Specification   tokens, incl. self {nom}/{min}/{max}/{units}; headings too
//	spec_units                 tokens
//	spec_nom                   tokens; "=" math; "query:" (picker, first row = nominal); "List:" (picker)
//	spec_min / spec_max        tokens + {nom}/{units}; "=" math; "query:" (first row value)
//	default_result             tokens; "=" math; an unfilled {id} means no default
//	hide                       tokens; an unfilled {id} reads as ""
//
// Tokens: {id} = that step's result, falling back to its resolved nominal; {record.X}; {form.X}.
// Nothing runs a query here: query values come in through stepCtx.queries (run by the caller outside any
// tx) or, when that has no entry, from the evaluated values the last save stored (stepCtx.stored).

// stepToken matches every token a step field may hold.
var stepToken = regexp.MustCompile(`\{(\d+|record\.\w+|form\.\w+|nom|min|max|units)\}`)

// stepEvals are a step's evaluated min/nom/max as stored on its result row by the last save ("" = none).
type stepEvals struct{ Min, Nom, Max string }

func (e stepEvals) get(field string) string {
	switch field {
	case "min":
		return e.Min
	case "nom":
		return e.Nom
	}
	return e.Max
}

// stepCtx is what a record gives its steps to resolve against.
type stepCtx struct {
	record  *models.TestRecord
	form    *models.TestForm
	values  map[int]string    // step id → result (every row on the record, "" when unfilled); nil = no record (definition view)
	stored  map[int]stepEvals // step id → evaluated values stored by the last save
	queries map[string]string // resolved query: spec → first-row value ("" = ran, no row); entries win over stored
	keepIDs bool              // leave {id} tokens for the edit page's live JS (RawSpecNom/RawDefault)
	// rerunQueries (save) reports every needed query as pending even when a stored value exists, so the
	// caller runs it afresh; a query that then fails still falls back to the stored value.
	rerunQueries bool
}

// resolvedStep is a step's definition with every resolvable token, math expression and query value applied.
// A field that can't fully resolve keeps its partially-resolved text.
type resolvedStep struct {
	Parameter, Specification, SpecNom, SpecMin, SpecMax, SpecUnits, DefaultResult string
	Evals                                                                         stepEvals // fully resolved min/nom/max ("" = unresolved or blank) — what a save stores
	Hidden                                                                        bool
}

// fieldVal is one resolved field: ok when fully resolved to a value (no tokens, directives or math left).
type fieldVal struct {
	text string
	ok   bool
}

type stepResolver struct {
	c       stepCtx
	steps   map[int]*models.TestStep
	memo    map[int]*resolvedStep
	noms    map[int]fieldVal
	busy    map[int]bool    // nominals being resolved — a cross-step cycle resolves to nothing
	pending map[string]bool // query specs needed but not yet run
}

// newStepResolver resolves the given steps (keyed by id) lazily on demand. steps must not be mutated while
// the resolver is in use.
func newStepResolver(steps map[int]*models.TestStep, c stepCtx) *stepResolver {
	return &stepResolver{c: c, steps: steps, memo: map[int]*resolvedStep{}, noms: map[int]fieldVal{},
		busy: map[int]bool{}, pending: map[string]bool{}}
}

// rawView is a resolver over the same steps that leaves {id} tokens for the edit page's JS.
func (r *stepResolver) rawView() *stepResolver {
	c := r.c
	c.keepIDs = true
	return newStepResolver(r.steps, c)
}

// pendingQueries lists the query specs resolution needed that neither stepCtx.queries nor stored values had.
func (r *stepResolver) pendingQueries() []string {
	out := make([]string, 0, len(r.pending))
	for q := range r.pending {
		out = append(out, q)
	}
	slices.Sort(out)
	return out
}

// step resolves one step; nil when the id isn't in the step set.
func (r *stepResolver) step(id int) *resolvedStep {
	if rs := r.memo[id]; rs != nil {
		return rs
	}
	s := r.steps[id]
	if s == nil {
		return nil
	}
	units := r.subst(s.SpecUnits, idNominal, nil)
	nomUnits := func(name string) (string, bool) {
		switch name {
		case "units":
			return units, true
		case "nom":
			return r.selfText(r.nominal(id))
		}
		return "", false
	}
	min := r.evalField(id, "min", s.SpecMin, nomUnits)
	max := r.evalField(id, "max", s.SpecMax, nomUnits)
	all := func(name string) (string, bool) {
		switch name {
		case "min":
			return r.selfText(min)
		case "max":
			return r.selfText(max)
		}
		return nomUnits(name)
	}

	rs := &resolvedStep{
		Parameter:     r.subst(s.Parameter, idNominal, all),
		Specification: r.subst(s.Specification, idNominal, all),
		SpecMin:       min.text,
		SpecMax:       max.text,
		SpecUnits:     units,
		DefaultResult: r.defaultResult(s.DefaultResult, all),
		Hidden:        hideFormulaHides(r.subst(s.HideFormula, idHide, nil)),
	}
	if nom := strings.TrimSpace(s.SpecNom); isPickerSpec(nom) {
		// The picker directive, with its params resolved. Its nominal is only resolved when something
		// asks for {nom}, so a plain picker never needs its query run on save.
		rs.SpecNom = r.subst(nom, idNominal, onlyUnits(units))
	} else {
		rs.SpecNom = r.nominal(id).text
	}
	if v, ok := r.noms[id]; ok && v.ok {
		rs.Evals.Nom = v.text
	}
	if min.ok {
		rs.Evals.Min = min.text
	}
	if max.ok {
		rs.Evals.Max = max.text
	}
	r.memo[id] = rs
	return rs
}

// passFail evaluates a value against a resolved step's bounds (nil for an empty value).
func (rs *resolvedStep) passFail(value, pfType string) *bool {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return models.ComputePassFail(value, &models.TestStep{PFType: pfType, SpecMin: rs.SpecMin, SpecMax: rs.SpecMax})
}

func isPickerSpec(s string) bool {
	return strings.HasPrefix(s, "query:") || strings.HasPrefix(s, "List:")
}

func onlyUnits(units string) func(string) (string, bool) {
	return func(name string) (string, bool) { return units, name == "units" }
}

// nominal resolves a step's spec_nom to its value (memoized). A List: picker has no nominal; a query:
// nominal is the query's first row.
func (r *stepResolver) nominal(id int) fieldVal {
	if v, ok := r.noms[id]; ok {
		return v
	}
	s := r.steps[id]
	if s == nil || r.busy[id] {
		return fieldVal{}
	}
	if strings.HasPrefix(strings.TrimSpace(s.SpecNom), "List:") {
		r.noms[id] = fieldVal{}
		return fieldVal{}
	}
	r.busy[id] = true
	v := r.evalField(id, "nom", s.SpecNom, onlyUnits(r.subst(s.SpecUnits, idNominal, nil)))
	delete(r.busy, id)
	r.noms[id] = v
	return v
}

// evalField resolves a spec_min/nom/max value: tokens, then a query: directive or "=" math.
func (r *stepResolver) evalField(id int, field, raw string, self func(string) (string, bool)) fieldVal {
	t := strings.TrimSpace(r.subst(raw, idNominal, self))
	switch {
	case strings.HasPrefix(t, "query:"):
		if r.c.keepIDs || stepToken.MatchString(t) {
			return fieldVal{text: t}
		}
		if v, ran := r.c.queries[t]; ran {
			return fieldVal{text: orText(v, t), ok: v != ""}
		}
		if r.c.rerunQueries {
			r.pending[t] = true
		}
		// Not run (or it failed): the last save's value stands in.
		if v := r.c.stored[id].get(field); v != "" {
			return fieldVal{text: v, ok: true}
		}
		r.pending[t] = true
		return fieldVal{text: t}
	case strings.HasPrefix(t, "="):
		if !stepToken.MatchString(t) {
			if v, ok := evalMath(t[1:]); ok {
				return fieldVal{text: formatMathResult(v), ok: true}
			}
		}
		return fieldVal{text: t}
	}
	return fieldVal{text: t, ok: !stepToken.MatchString(t)}
}

func orText(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// selfText is what a {nom}/{min}/{max} token becomes: the resolved value, or — in the edit page's raw view
// only — the unresolved text, so its {id} tokens still reach the live JS (an "=" expression is inlined
// in parentheses so it stays valid math).
func (r *stepResolver) selfText(v fieldVal) (string, bool) {
	switch {
	case v.ok:
		return v.text, true
	case !r.c.keepIDs || v.text == "" || strings.HasPrefix(v.text, "query:"):
		return "", false
	case strings.HasPrefix(v.text, "="):
		return "(" + v.text[1:] + ")", true
	}
	return v.text, true
}

// defaultResult resolves default_result: plain text as-is, a leading "=" evaluated as math (#132). Any
// unresolved token (e.g. an unfilled {id}) means no default; the raw view keeps it for the live JS.
func (r *stepResolver) defaultResult(raw string, self func(string) (string, bool)) string {
	d := r.subst(raw, idDefault, self)
	t := strings.TrimSpace(d)
	if stepToken.MatchString(t) {
		if r.c.keepIDs {
			return d
		}
		return ""
	}
	if strings.HasPrefix(t, "=") {
		if v, ok := evalMath(t[1:]); ok {
			return formatMathResult(v)
		}
		if r.c.keepIDs {
			return d
		}
		return ""
	}
	return d
}

// idMode is how an {id} token whose step has no result resolves.
type idMode int

const (
	idNominal idMode = iota // fall back to that step's resolved nominal
	idDefault               // stay unresolved (no default from nominals)
	idHide                  // read as "" when the step is on the record, as the edit page shows it
)

// subst replaces tokens in s. self supplies {nom}/{min}/{max}/{units} (nil = leave them). Unresolvable
// tokens are left as-is.
func (r *stepResolver) subst(s string, mode idMode, self func(string) (string, bool)) string {
	if !strings.Contains(s, "{") {
		return s
	}
	return stepToken.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		switch {
		case strings.HasPrefix(inner, "record."):
			return recordToken(inner, r.c.record, m)
		case strings.HasPrefix(inner, "form."):
			return formToken(inner, r.c.form, m)
		case inner == "nom" || inner == "min" || inner == "max" || inner == "units":
			if self != nil {
				if v, ok := self(inner); ok {
					return v
				}
			}
			return m
		}
		if r.c.keepIDs {
			return m
		}
		id, _ := strconv.Atoi(inner)
		v, onRecord := r.c.values[id]
		if v != "" {
			return v
		}
		switch mode {
		case idHide:
			if onRecord {
				return ""
			}
		case idNominal:
			if n := r.nominal(id); n.ok {
				return n.text
			}
		}
		return m
	})
}

// recordToken resolves a {record.X} token; m (the token itself) when unknown or there's no record.
//   - {record.type}      → record's Type (record_type field)
//   - {record.pn}        → record's unit-under-test part number (subject_part_number)
//   - {record.sn}        → record's serial number
//   - {record.pndesc}    → record's unit-under-test description (subject_pn_description / description)
//   - {record.date}      → record's test date (MM/DD/YYYY) — date only, safe for SQL format 101
//   - {record.datetime}  → record's test date + time (MM/DD/YYYY H:MM AM/PM)
func recordToken(inner string, record *models.TestRecord, m string) string {
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

// formToken resolves a {form.X} token; m when unknown or there's no form.
func formToken(inner string, form *models.TestForm, m string) string {
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

// hideFormulaHides reports whether an already-resolved hide formula hides its step:
//
//	""  or  "SHOW"   → visible
//	"HIDE"           → hidden
//	"lhs=rhs"        → hidden when equal (case-insensitive, trimmed)
//	"lhs!=rhs"       → hidden when not equal
//
// Anything else shows. An unresolved token compares as its own text, so "=" never matches it.
func hideFormulaHides(resolved string) bool {
	resolved = strings.TrimSpace(resolved)
	switch {
	case resolved == "" || strings.EqualFold(resolved, "SHOW"):
		return false
	case strings.EqualFold(resolved, "HIDE"):
		return true
	}
	// Check != before = to avoid splitting on the = within !=.
	if before, after, ok := strings.Cut(resolved, "!="); ok {
		return !strings.EqualFold(strings.TrimSpace(before), strings.TrimSpace(after))
	}
	if before, after, ok := strings.Cut(resolved, "="); ok {
		return strings.EqualFold(strings.TrimSpace(before), strings.TrimSpace(after))
	}
	return false
}

// recordStepSet is the step set a saved record resolves over: the live definition (so an {id} can fall
// back to a step that isn't on the record), overridden by the record's own frozen snapshot rows (#487).
func recordStepSet(results map[int]*models.TestResult, liveSteps map[int]*models.TestStep) map[int]*models.TestStep {
	steps := make(map[int]*models.TestStep, len(liveSteps)+len(results))
	for id, s := range liveSteps {
		steps[id] = s
	}
	for id, res := range results {
		steps[id] = stepFromResult(id, res)
	}
	return steps
}

// recordStepCtx is a saved record's resolution context: its own results and stored evaluated values.
func recordStepCtx(results map[int]*models.TestResult, record *models.TestRecord, form *models.TestForm) stepCtx {
	c := stepCtx{record: record, form: form, values: map[int]string{}, stored: map[int]stepEvals{}}
	for id, res := range results {
		c.values[id] = res.Result
		c.stored[id] = stepEvals{Min: res.EvalMin, Nom: res.EvalNom, Max: res.EvalMax}
	}
	return c
}

// applyResolved writes a resolved step onto a render row (whose Step is the row's own copy). Templates show
// the frozen result's parameter/specification when one exists, so those go onto a copy of the result too —
// the shared result map entry stays untouched.
func applyResolved(row *models.ResultRow, rs *resolvedStep) {
	if rs == nil {
		return
	}
	s := row.Step
	s.Parameter, s.Specification, s.SpecNom = rs.Parameter, rs.Specification, rs.SpecNom
	s.SpecMin, s.SpecMax, s.SpecUnits, s.DefaultResult = rs.SpecMin, rs.SpecMax, rs.SpecUnits, rs.DefaultResult
	if row.Result != nil {
		res := *row.Result
		res.Parameter, res.Specification = rs.Parameter, rs.Specification
		row.Result = &res
	}
}
