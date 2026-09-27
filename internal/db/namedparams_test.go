package db

import (
	"strings"
	"testing"
)

func TestRewriteNamedParams(t *testing.T) {
	q := "SELECT * FROM t WHERE pn = @pn AND item = @item AND pn2 = @pn"
	names := []string{"pn", "item"}

	// Each token is rewritten to its 1-based position in names, including
	// repeated occurrences of the same name.
	want := "SELECT * FROM t WHERE pn = $1 AND item = $2 AND pn2 = $1"
	if got := RewriteNamedParams(q, names); got != want {
		t.Errorf("RewriteNamedParams: got %q, want %q", got, want)
	}
}

func TestRewriteNamedParams_SkipsLiteralsAndComments(t *testing.T) {
	cases := []struct{ in, want string }{
		{`SELECT '@pn', "@pn", x FROM t WHERE a = @pn -- @pn`, `SELECT '@pn', "@pn", x FROM t WHERE a = $1 -- @pn`},
		{"SELECT 1 /* @pn */ WHERE a = @pn", "SELECT 1 /* @pn */ WHERE a = $1"},
		{"SELECT 'it''s @pn' WHERE a = @pn", "SELECT 'it''s @pn' WHERE a = $1"},
		{"SELECT @@ROWCOUNT WHERE a = @pn", "SELECT @@ROWCOUNT WHERE a = $1"},
		{"SELECT 1 WHERE a = @unknown", "SELECT 1 WHERE a = @unknown"},
	}
	for _, c := range cases {
		if got := RewriteNamedParams(c.in, []string{"pn", "ROWCOUNT"}); got != c.want {
			t.Errorf("RewriteNamedParams(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNamedParamRefs(t *testing.T) {
	q := `SELECT '@lit' /* @c */ FROM t WHERE b = @b AND a = @a AND b2 = @b -- @d`
	got := NamedParamRefs(q)
	if strings.Join(got, ",") != "b,a" {
		t.Errorf("NamedParamRefs = %v, want [b a]", got)
	}
}
