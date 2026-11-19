package main

import "testing"

// TestEvalMath pins the Go evaluator to the edit page's JS evalMath behavior (app.js), which it replaces
// for server-side defaults and spec bounds (#468).
func TestEvalMath(t *testing.T) {
	ok := []struct {
		expr string
		want string
	}{
		{"1+2*3", "7"},
		{"(1+2)*3", "9"},
		{" 10 / 4 ", "2.5"},
		{"-3+5", "2"},
		{"--3", "3"},
		{"+4", "4"},
		{"5*0.9", "4.5"},
		{"5*1.1", "5.5"},
		{"0.1+0.2", "0.3"}, // 10-decimal rounding hides float noise
		{"min(3, 1, 2)", "1"},
		{"max(3, 1, 2)", "3"},
		{"abs(-2.5)", "2.5"},
		{"mod(7, 3)", "1"},
		{"mod(-7, 3)", "2"}, // divisor's sign
		{"mod(7, -3)", "-2"},
		{"round(2.5)", "3"},
		{"round(-2.5)", "-2"}, // JS Math.round: halves round up
		{"floor(-1.5)", "-2"},
		{"ceil(1.2)", "2"},
		{"sqrt(16)", "4"},
		{"pow(2, 10)", "1024"},
		{"min(4.5, max(1, 2))", "2"},
		{"-0*1", "0"},
	}
	for _, c := range ok {
		v, good := evalMath(c.expr)
		if !good {
			t.Errorf("evalMath(%q) failed, want %s", c.expr, c.want)
			continue
		}
		if got := formatMathResult(v); got != c.want {
			t.Errorf("evalMath(%q) = %s, want %s", c.expr, got, c.want)
		}
	}

	bad := []string{
		"", "1+", "(1", "1)", "1 2", ".5", "1.2.3", "abc", "alert(1)", "min(1)", "abs(1,2)", "pow(2)",
		"max()", "1/0", "sqrt(-1)", "12/29/2026", "{12}*2", "5mW", "query:x",
	}
	for _, expr := range bad {
		if v, good := evalMath(expr); good {
			t.Errorf("evalMath(%q) = %v, want failure", expr, v)
		}
	}
}
