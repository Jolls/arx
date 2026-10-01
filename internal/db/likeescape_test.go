package db

import "testing"

func TestEscapeLike(t *testing.T) {
	for in, want := range map[string]string{
		"%":     `\%`,
		"_":     `\_`,
		"50%":   "50%",
		"A_1":   "A_1",
		"%%":    "%%",
		`a\b`:   `a\\b`,
		"plain": "plain",
		"":      "",
	} {
		if got := EscapeLike(in); got != want {
			t.Errorf("EscapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}
