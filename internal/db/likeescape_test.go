package db

import "testing"

func TestEscapeLike(t *testing.T) {
	for in, want := range map[string]string{
		"50%":   `50\%`,
		"A_1":   `A\_1`,
		"A*1":   "A%1",
		"*":     "%",
		`a\b`:   `a\\b`,
		"plain": "plain",
		"":      "",
	} {
		if got := EscapeLike(in); got != want {
			t.Errorf("EscapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}
