package main

import (
	"testing"

	"arx/arx_go/models"
)

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
		{"", "anything", true},     // no filter
		{"A,B", "", true},          // no record type
		{"A,B", "b", true},         // case-insensitive match
		{" A , B ", "a", true},     // trimmed match
		{"A,B", "C", false},        // no match
		{"cal", "CAL", true},       // case fold
	}
	for _, c := range cases {
		if got := stepAppliesToRecord(c.instrumentTypes, c.recordType); got != c.want {
			t.Errorf("stepAppliesToRecord(%q, %q) = %v, want %v",
				c.instrumentTypes, c.recordType, got, c.want)
		}
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
