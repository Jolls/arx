package main

import (
	"testing"

	"arx/arx_go/models"
)

func TestIsAutoSerial(t *testing.T) {
	cases := []struct {
		submitted string
		suggested string
		want      bool
	}{
		{"42", "42", true},   // exact match → auto
		{"42", "43", false},  // differing value → override
		{" 42 ", "42", true}, // whitespace differences that trim to equal → true
		{"42", " 42 ", true}, // whitespace on suggested side
		{"", "42", false},    // empty submitted vs non-empty suggested → false
		{"", "", true},       // both empty → true
	}
	for _, c := range cases {
		if got := isAutoSerial(c.submitted, c.suggested); got != c.want {
			t.Errorf("isAutoSerial(%q, %q) = %v, want %v", c.submitted, c.suggested, got, c.want)
		}
	}
}

func TestIsSafeQuery(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{"SELECT * FROM PN", true},
		{"  select id from forms  ", true}, // lowercase + leading space
		{"", false},
		{"DELETE FROM PN", false},
		{"UPDATE PN SET x = 1", false},
		{"INSERT INTO PN VALUES (1)", false},
		{"SELECT 1; DROP TABLE PN", false},          // semicolon
		{"SELECT * FROM PN WHERE x = 1; --", false}, // trailing statement
		{"EXEC sp_who", false},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", false}, // must start with SELECT
		// word-boundary: column names containing keyword substrings must not be blocked
		{"SELECT created_at FROM log", true},
		{"SELECT updated_at FROM records", true},
		{"SELECT alternate FROM parts", true},
		// EXECUTE is still blocked
		{"SELECT EXECUTE sp_something", false},
		// SELECT-prefixed writes / admin / DoS must be rejected
		{"SELECT * INTO junk FROM parts", false},     // table-creating write
		{"SELECT 1 WAITFOR DELAY '00:00:10'", false}, // DoS
		{"SELECT * FROM parts MERGE x", false},
		{"SELECT 1; GRANT SELECT TO app", false},
		{"SELECT 1 DBCC CHECKDB", false},
	}
	for _, c := range cases {
		if got := isSafeQuery(c.query); got != c.want {
			t.Errorf("isSafeQuery(%q) = %v, want %v", c.query, got, c.want)
		}
	}
}

func TestParseQuerySpec(t *testing.T) {
	t.Run("name only", func(t *testing.T) {
		name, params := parseQuerySpec("query:operators")
		if name != "operators" {
			t.Errorf("name = %q, want operators", name)
		}
		if len(params) != 0 {
			t.Errorf("params = %v, want empty", params)
		}
	})

	t.Run("name with params", func(t *testing.T) {
		name, params := parseQuerySpec("query:lookup(@id=5,@type=cal)")
		if name != "lookup" {
			t.Errorf("name = %q, want lookup", name)
		}
		if params["id"] != "5" || params["type"] != "cal" {
			t.Errorf("params = %v, want {id:5 type:cal}", params)
		}
	})

	t.Run("without query prefix", func(t *testing.T) {
		name, params := parseQuerySpec("plain(@a=1)")
		if name != "plain" || params["a"] != "1" {
			t.Errorf("got name=%q params=%v", name, params)
		}
	})

	t.Run("whitespace trimmed", func(t *testing.T) {
		name, params := parseQuerySpec("query:bar( @x = 5 )")
		if name != "bar" || params["x"] != "5" {
			t.Errorf("got name=%q params=%v", name, params)
		}
	})

	t.Run("malformed param skipped", func(t *testing.T) {
		_, params := parseQuerySpec("query:foo(@a=1,@bogus)")
		if len(params) != 1 || params["a"] != "1" {
			t.Errorf("params = %v, want only {a:1}", params)
		}
	})
}

func TestStepAppliesToRecord(t *testing.T) {
	cases := []struct {
		instrumentTypes, recordType string
		want                        bool
	}{
		{"", "anything", true}, // no filter
		{"A,B", "", true},      // no record type
		{"A,B", "b", true},     // case-insensitive match
		{" A , B ", "a", true}, // trimmed match
		{"A,B", "C", false},    // no match
		{"cal", "CAL", true},   // case fold
	}
	for _, c := range cases {
		if got := stepAppliesToRecord(c.instrumentTypes, c.recordType); got != c.want {
			t.Errorf("stepAppliesToRecord(%q, %q) = %v, want %v",
				c.instrumentTypes, c.recordType, got, c.want)
		}
	}
}

func TestStepVisibleOnRecord(t *testing.T) {
	cases := []struct {
		name      string
		archived  bool
		hasResult bool
		want      bool
	}{
		{"active step always shown", false, false, true},
		{"active step with result shown", false, true, true},
		{"archived step without result hidden", true, false, false},
		{"archived step with result shown", true, true, true},
	}
	for _, c := range cases {
		if got := stepVisibleOnRecord(c.archived, c.hasResult); got != c.want {
			t.Errorf("%s: stepVisibleOnRecord(%v, %v) = %v, want %v",
				c.name, c.archived, c.hasResult, got, c.want)
		}
	}
}

func TestStepFromResult(t *testing.T) {
	res := &models.TestResult{
		Parameter: "P", Specification: "S", SpecMin: "1", SpecNom: "5", SpecMax: "9",
		SpecUnits: "V", PFType: "range", Format: "0.0", Type: 2, HideFormula: "H", DefaultResult: "D",
	}
	step := stepFromResult(7, res)
	if step.ID != 7 || step.Type != 2 || step.Parameter != "P" || step.Specification != "S" ||
		step.SpecMin != "1" || step.SpecNom != "5" || step.SpecMax != "9" || step.SpecUnits != "V" ||
		step.PFType != "range" || step.Format != "0.0" || step.HideFormula != "H" || step.DefaultResult != "D" {
		t.Errorf("stepFromResult did not carry the snapshot: %+v", step)
	}
}

// TestEvaluateHide guards the conditional step-visibility semantics (#257) that both the
// server (frozen-row filtering) and the record-editor JS mirror. Step {12} resolves to its
// recorded result; {record.type} resolves to the record comment.
func TestEvaluateHide(t *testing.T) {
	results := map[int]*models.TestResult{
		12: {Result: "N/A"},
	}
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := evaluateHide(c.formula, results, nil, record, nil); got != c.want {
				t.Errorf("evaluateHide(%q) = %v, want %v", c.formula, got, c.want)
			}
		})
	}
}

func TestBakeStepTokens(t *testing.T) {
	rec := &models.TestRecord{SerialNumber: "42", SerialNumberPN: "PN-1"}

	// Self + record tokens bake; {id} cross-step tokens stay for render-time resolution.
	step := &models.TestStep{
		SpecMin: "1", SpecMax: "9", SpecNom: "5", SpecUnits: "V",
		Specification: "{nom}{units} ({min}-{max}) for {record.sn} vs {3}",
	}
	bakeStepTokens(step, rec, nil)
	if step.Specification != "5V (1-9) for 42 vs {3}" {
		t.Errorf("bake should resolve self/record tokens and keep {id}: %q", step.Specification)
	}

	// A query: directive in spec_nom must NOT be baked into the spec text.
	q := &models.TestStep{SpecNom: "query:lookup(@pn={record.pn})", Specification: "{nom}"}
	bakeStepTokens(q, rec, nil)
	if q.Specification != "{nom}" {
		t.Errorf("query directive must not bake into spec text, got %q", q.Specification)
	}
	if q.SpecNom != "query:lookup(@pn=PN-1)" {
		t.Errorf("record tokens inside the query directive should still resolve, got %q", q.SpecNom)
	}
}

func TestImageResult(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"SN-123_rID-4_tID-5", true},
		{"sn-1_rid-2_tid-3", true}, // case-insensitive
		{"SN-123", false},          // missing _RID-/_TID-
		{"SN-1_rID-2", false},      // missing _tID-
		{"report.pdf", false},
		{"", false},
		{"SN123_rID45_tID6_20260703_143022.png", true}, // new no-dash shape (#587)
		{"SN123_rID45_20260703_143022.png", false},     // missing _tID
		{"SNAPSHOT_RIDGE_TIDY", false},                 // free text containing bare "_RID"/"_TID" substrings, but no digits after them
	}
	for _, c := range cases {
		if got := imageResult(c.val); got != c.want {
			t.Errorf("imageResult(%q) = %v, want %v", c.val, got, c.want)
		}
	}
}

func TestSubstituteStepSelf(t *testing.T) {
	step := &models.TestStep{SpecMin: "1", SpecMax: "9", SpecNom: "5", SpecUnits: "V"}

	t.Run("replaces all tokens", func(t *testing.T) {
		got := substituteStepSelf("{min} to {max} ({nom}) {units}", step)
		want := "1 to 9 (5) V"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("nil step returns input unchanged", func(t *testing.T) {
		in := "{min}-{max}"
		if got := substituteStepSelf(in, nil); got != in {
			t.Errorf("got %q, want %q", got, in)
		}
	})

	t.Run("no tokens returns input unchanged", func(t *testing.T) {
		in := "plain text"
		if got := substituteStepSelf(in, step); got != in {
			t.Errorf("got %q, want %q", got, in)
		}
	})
}
