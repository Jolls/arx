package main

import (
	"testing"

	"arx/arx_go/models"
)

// resolveOne resolves step id over steps with ctx c.
func resolveOne(t *testing.T, steps map[int]*models.TestStep, c stepCtx, id int) *resolvedStep {
	t.Helper()
	rs := newStepResolver(steps, c).step(id)
	if rs == nil {
		t.Fatalf("step %d not resolved", id)
	}
	return rs
}

func pfString(pf *bool) string {
	switch {
	case pf == nil:
		return "nil"
	case *pf:
		return "PASS"
	}
	return "FAIL"
}

// The #468 example: bounds derived from the nominal render as numbers and drive P/F.
func TestResolveStep_MathBoundsFromNominal(t *testing.T) {
	steps := map[int]*models.TestStep{1: {ID: 1, SpecNom: "5", SpecMin: "={nom}*0.9", SpecMax: "={nom}*1.1", SpecUnits: "mW",
		Specification: "{nom}{units} ( {min}{units} - {max}{units})"}}
	rs := resolveOne(t, steps, stepCtx{values: map[int]string{}}, 1)
	if rs.Specification != "5mW ( 4.5mW - 5.5mW)" {
		t.Errorf("Specification = %q", rs.Specification)
	}
	if rs.Evals != (stepEvals{Min: "4.5", Nom: "5", Max: "5.5"}) {
		t.Errorf("Evals = %+v", rs.Evals)
	}
	for value, want := range map[string]string{"5.2": "PASS", "6": "FAIL", "4.5": "PASS", "abc": "nil", "": "nil"} {
		if got := pfString(rs.passFail(value, "range")); got != want {
			t.Errorf("passFail(%q) = %s, want %s", value, got, want)
		}
	}
}

// {id} in a bound uses the record's value, else the referenced step's resolved nominal; an
// unresolvable bound leaves P/F blank instead of passing.
func TestResolveStep_CrossStepBounds(t *testing.T) {
	steps := map[int]*models.TestStep{
		10: {ID: 10, SpecNom: "=2*2"},
		11: {ID: 11, SpecMax: "={10}+1"},
		12: {ID: 12, SpecMax: "{99}"},
	}
	c := stepCtx{values: map[int]string{10: "7", 11: "", 12: ""}}
	if got := resolveOne(t, steps, c, 11).SpecMax; got != "8" {
		t.Errorf("max with value = %q, want 8", got)
	}
	c.values[10] = ""
	if got := resolveOne(t, steps, c, 11).SpecMax; got != "5" {
		t.Errorf("max from nominal fallback = %q, want 5", got)
	}
	rs := resolveOne(t, steps, c, 12)
	if rs.SpecMax != "{99}" || rs.Evals.Max != "" || rs.passFail("1", "range") != nil {
		t.Errorf("unresolvable bound = %q evals %+v pf %s", rs.SpecMax, rs.Evals, pfString(rs.passFail("1", "range")))
	}
}

func TestResolveStep_Default(t *testing.T) {
	steps := map[int]*models.TestStep{
		15: {ID: 15, SpecNom: "3"},
		16: {ID: 16, SpecMax: "9", DefaultResult: "=min({15}, {max})"},
		17: {ID: 17, DefaultResult: "{record.sn}"},
	}
	rec := &models.TestRecord{SerialNumber: "SN-1"}
	c := stepCtx{record: rec, values: map[int]string{15: "12"}}
	if got := resolveOne(t, steps, c, 16).DefaultResult; got != "9" {
		t.Errorf("default = %q, want 9", got)
	}
	c.values[15] = ""
	if got := resolveOne(t, steps, c, 16).DefaultResult; got != "" {
		t.Errorf("default with unfilled {15} = %q, want none (no default from nominals)", got)
	}
	if got := resolveOne(t, steps, c, 17).DefaultResult; got != "SN-1" {
		t.Errorf("plain-text default = %q", got)
	}
}

func TestLintStep(t *testing.T) {
	ids := map[int]bool{1: true, 15: true}
	cases := []struct {
		name string
		step models.TestStep
		want string // "" = no warning; else "field: msg"
	}{
		{"clean", models.TestStep{SpecNom: "5", SpecMin: "={nom}*0.9", SpecMax: "query:lim(@pn={record.pn})", Specification: "{nom} {min}-{max}{units}"}, ""},
		{"query in spec_max ok, in specification not", models.TestStep{Specification: "query:x"}, "specification: query: only runs in spec_nom, spec_min and spec_max"},
		{"=query", models.TestStep{SpecMax: "=query:x(@a=1)"}, "spec_max: = math can't run a query; start the value with query: instead"},
		{"query not leading", models.TestStep{SpecMax: "max query:x"}, "spec_max: query: must start the value"},
		{"query params need @", models.TestStep{SpecNom: "query:x(pn=1)"}, "spec_nom: Params need @: @pn"},
		{"query missing paren", models.TestStep{SpecNom: "query:x(@pn=1"}, "spec_nom: Malformed query: (missing closing parenthesis?)"},
		{"math in parameter", models.TestStep{Parameter: "=1+1"}, "parameter: = math only works in spec_nom, spec_min, spec_max and default_result"},
		{"bad math", models.TestStep{DefaultResult: "=min({15},"}, "default_result: Invalid math expression"},
		{"divide by token ok", models.TestStep{DefaultResult: "=10/({15}-1)"}, ""},
		{"non-numeric bound", models.TestStep{SpecMin: "5mW"}, "spec_min: Not a number; P/F will be blank"},
		{"token bound ok", models.TestStep{SpecMin: "{15}"}, ""},
		{"unknown step", models.TestStep{Parameter: "after {99}"}, "parameter: No step {99} on this form"},
		{"unknown token", models.TestStep{Specification: "{record.serial}"}, "specification: Unknown token {record.serial}"},
		{"self token not in bounds", models.TestStep{SpecMax: "={min}+1"}, "spec_max: {min} isn't available in spec_max"},
		{"List in bound", models.TestStep{SpecMax: "List:a;b"}, "spec_max: List: only works in spec_nom"},
		{"List: as plain text is fine", models.TestStep{Parameter: "List: connector pins checked"}, ""},
		{"heading ignores spec fields", models.TestStep{Type: 1, Parameter: "{1} heading", SpecMax: "junk"}, ""},
		{"hide comparison ok", models.TestStep{HideFormula: "{15}=N/A"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ""
			if w := lintStep(&c.step, ids); len(w) > 0 {
				got = w[0].Field + ": " + w[0].Msg
				if len(w) > 1 {
					t.Errorf("extra warnings: %+v", w[1:])
				}
			}
			if got != c.want {
				t.Errorf("lint = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveStep_QueryBounds(t *testing.T) {
	spec := "query:limit(@pn=PN-1)"
	steps := map[int]*models.TestStep{1: {ID: 1, SpecMax: "query:limit(@pn={record.pn})", Specification: "<= {max}"}}
	rec := &models.TestRecord{SerialNumberPN: "PN-1"}

	// Not run, nothing stored: pending, the bound unresolved.
	res := newStepResolver(steps, stepCtx{record: rec, values: map[int]string{1: "3"}})
	rs := res.step(1)
	if got := res.pendingQueries(); len(got) != 1 || got[0] != spec {
		t.Fatalf("pending = %v", got)
	}
	if rs.SpecMax != spec || rs.Specification != "<= {max}" || rs.passFail("3", "range") != nil {
		t.Errorf("unrun query bound = %q / %q", rs.SpecMax, rs.Specification)
	}

	// The last save's stored value stands in (view/print/resync).
	c := stepCtx{record: rec, values: map[int]string{1: "3"}, stored: map[int]stepEvals{1: {Max: "2.5"}}}
	rs = resolveOne(t, steps, c, 1)
	if rs.Specification != "<= 2.5" || pfString(rs.passFail("3", "range")) != "FAIL" {
		t.Errorf("stored query bound = %q pf %s", rs.Specification, pfString(rs.passFail("3", "range")))
	}

	// A query that ran wins over the stored value; one that ran with no row leaves it unresolved.
	c.queries = map[string]string{spec: "4"}
	if rs = resolveOne(t, steps, c, 1); rs.Evals.Max != "4" || pfString(rs.passFail("3", "range")) != "PASS" {
		t.Errorf("run query bound = %+v", rs.Evals)
	}
	c.queries = map[string]string{spec: ""}
	if rs = resolveOne(t, steps, c, 1); rs.SpecMax != spec || rs.Evals.Max != "" {
		t.Errorf("empty query bound = %q %+v", rs.SpecMax, rs.Evals)
	}

	// A save asks for the query even with a stored value; if it doesn't run (it failed), the stored value stands.
	c.queries, c.rerunQueries = nil, true
	res = newStepResolver(steps, c)
	if rs = res.step(1); len(res.pendingQueries()) != 1 || rs.Evals.Max != "2.5" {
		t.Errorf("rerun: pending %v evals %+v, want the query requested and 2.5 kept", res.pendingQueries(), rs.Evals)
	}
}

// A query: spec_nom stays the picker directive; its nominal (first row) is only needed — and its query
// only requested — when something references {nom}.
func TestResolveStep_QueryNominalIsLazy(t *testing.T) {
	picker := map[int]*models.TestStep{1: {ID: 1, SpecNom: "query:sns(@form={form.id})", Specification: "pick one"}}
	f := &models.TestForm{ID: 7}
	res := newStepResolver(picker, stepCtx{form: f, values: map[int]string{}})
	if rs := res.step(1); rs.SpecNom != "query:sns(@form=7)" || len(res.pendingQueries()) != 0 {
		t.Errorf("picker SpecNom = %q pending %v", rs.SpecNom, res.pendingQueries())
	}

	picker[1].Specification = "nominal {nom}"
	c := stepCtx{form: f, values: map[int]string{}, queries: map[string]string{"query:sns(@form=7)": "SN-9"}}
	rs := resolveOne(t, picker, c, 1)
	if rs.Specification != "nominal SN-9" || rs.SpecNom != "query:sns(@form=7)" || rs.Evals.Nom != "SN-9" {
		t.Errorf("demanded nominal = %q / %q / %+v", rs.Specification, rs.SpecNom, rs.Evals)
	}
}

func TestResolveStep_ListNominal(t *testing.T) {
	steps := map[int]*models.TestStep{1: {ID: 1, SpecNom: "List:Yes;No", Specification: "{nom}"}, 2: {ID: 2, Parameter: "after {1}"}}
	c := stepCtx{values: map[int]string{1: ""}}
	if rs := resolveOne(t, steps, c, 1); rs.SpecNom != "List:Yes;No" || rs.Specification != "{nom}" {
		t.Errorf("List step = %q / %q", rs.SpecNom, rs.Specification)
	}
	if got := resolveOne(t, steps, c, 2).Parameter; got != "after {1}" {
		t.Errorf("{id} of an unfilled List step = %q, want the token kept (no nominal)", got)
	}
}

func TestResolveStep_CrossStepCycle(t *testing.T) {
	steps := map[int]*models.TestStep{1: {ID: 1, SpecNom: "={2}+1"}, 2: {ID: 2, SpecNom: "={1}+1"}}
	rs := resolveOne(t, steps, stepCtx{values: map[int]string{}}, 1)
	if rs.Evals.Nom != "" {
		t.Errorf("cyclic nominal resolved to %q", rs.Evals.Nom)
	}
}

func TestResolveStep_TokensAndHeadings(t *testing.T) {
	steps := map[int]*models.TestStep{
		269: {ID: 269},
		300: {ID: 300, Type: 1, Parameter: "{269}: Laser SN"},
		301: {ID: 301, Parameter: "{record.sn} on {form.pn}", Specification: "{record.type}"},
	}
	c := stepCtx{record: &models.TestRecord{SerialNumber: "42", RecordType: "Re-Test"}, form: &models.TestForm{PartNumber: "PN-1"},
		values: map[int]string{269: "520nm"}}
	if got := resolveOne(t, steps, c, 300).Parameter; got != "520nm: Laser SN" {
		t.Errorf("heading Parameter = %q", got)
	}
	if rs := resolveOne(t, steps, c, 301); rs.Parameter != "42 on PN-1" || rs.Specification != "Re-Test" {
		t.Errorf("record/form tokens = %q / %q", rs.Parameter, rs.Specification)
	}
	if got := resolveOne(t, steps, stepCtx{}, 301).Parameter; got != "{record.sn} on {form.pn}" {
		t.Errorf("no context keeps tokens: %q", got)
	}
}

// Conditional step visibility (#257), as the record editor's JS shows it: an unfilled {id} reads as "".
func TestResolveStep_Hide(t *testing.T) {
	record := &models.TestRecord{RecordType: "Re-Test"}
	cases := []struct {
		name    string
		formula string
		want    bool // true = hidden
	}{
		{"blank shows", "", false},
		{"SHOW shows", "SHOW", false},
		{"HIDE hides", "HIDE", true},
		{"case-insensitive HIDE", "hide", true},
		{"eq match hides", "{12}=N/A", true},
		{"eq mismatch shows", "{12}=PASS", false},
		{"eq is case-insensitive", "{12}=n/a", true},
		{"neq match shows", "{12}!=N/A", false},
		{"neq mismatch hides", "{12}!=PASS", true},
		{"record token eq hides", "{record.type}=Re-Test", true},
		{"unresolvable step ref shows", "{99}=N/A", false},
		{"whitespace tolerated", "  {12} = N/A ", true},
		{"unfilled step reads empty", "{13}=", true},
		{"unfilled step is not its nominal", "{13}=N/A", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			steps := map[int]*models.TestStep{1: {ID: 1, HideFormula: c.formula}, 13: {ID: 13, SpecNom: "N/A"}}
			ctx := stepCtx{record: record, values: map[int]string{12: "N/A", 13: ""}}
			if got := resolveOne(t, steps, ctx, 1).Hidden; got != c.want {
				t.Errorf("hide %q = %v, want %v", c.formula, got, c.want)
			}
		})
	}
}

// A frozen result row displays its saved parameter/specification (EffectiveParameter/EffectiveSpec), so the
// resolved text lands on a copy of the result — the shared map entry stays untouched.
func TestApplyResolved(t *testing.T) {
	results := map[int]*models.TestResult{
		269: {TestID: 269, Result: "520nm"},
		300: {TestID: 300, Parameter: "{269}: Laser SN", Specification: "max for {269}"},
	}
	res := newStepResolver(recordStepSet(results, nil), recordStepCtx(results, &models.TestRecord{}, &models.TestForm{}))
	row := models.ResultRow{Step: stepFromResult(300, results[300]), Result: results[300]}
	applyResolved(&row, res.step(300))
	if got := row.EffectiveParameter(); got != "520nm: Laser SN" {
		t.Errorf("EffectiveParameter = %q", got)
	}
	if got := row.EffectiveSpec(); got != "max for 520nm" {
		t.Errorf("EffectiveSpec = %q", got)
	}
	if results[300].Parameter != "{269}: Laser SN" {
		t.Errorf("shared result map entry was mutated: %q", results[300].Parameter)
	}
}
