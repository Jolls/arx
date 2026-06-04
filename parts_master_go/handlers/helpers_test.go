package handlers

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"   ", nil},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b ,c ", []string{"a", "b", "c"}}, // trims each token
		{"a,,b", []string{"a", "b"}},            // empty tokens dropped
		{",", nil},
		{"solo", []string{"solo"}},
	}
	for _, c := range cases {
		got := splitCSV(c.input)
		if len(got) != len(c.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", c.input, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitCSV(%q) = %v, want %v", c.input, got, c.want)
				break
			}
		}
	}
}

func TestStatusIsActive(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{"pending", true},
		{"placed", true},
		{"on_hold", true},
		{"closed", false},
		{"cancelled", false},
		{"", false},
		{"PENDING", false}, // case-sensitive
	}
	for _, c := range cases {
		if got := statusIsActive(c.status); got != c.want {
			t.Errorf("statusIsActive(%q) = %v, want %v", c.status, got, c.want)
		}
	}
}

func TestParseFormDate(t *testing.T) {
	if got := parseFormDate(""); got != nil {
		t.Errorf("parseFormDate(\"\") = %v, want nil", got)
	}
	if got := parseFormDate("not-a-date"); got != nil {
		t.Errorf("parseFormDate(invalid) = %v, want nil", got)
	}
	got := parseFormDate("  2024-03-05  ") // surrounding whitespace trimmed
	if got == nil {
		t.Fatalf("parseFormDate(valid) = nil, want a time")
	}
	if got.Year() != 2024 || got.Month() != 3 || got.Day() != 5 {
		t.Errorf("parseFormDate = %v, want 2024-03-05", got)
	}
}

func TestParseFormFloat(t *testing.T) {
	if got := parseFormFloat(""); got != nil {
		t.Errorf("parseFormFloat(\"\") = %v, want nil", got)
	}
	if got := parseFormFloat("abc"); got != nil {
		t.Errorf("parseFormFloat(invalid) = %v, want nil", got)
	}
	if got := parseFormFloat(" 2.5 "); got != 2.5 { // trimmed then parsed
		t.Errorf("parseFormFloat(\" 2.5 \") = %v, want 2.5", got)
	}
	if got := parseFormFloat("0"); got != 0.0 {
		t.Errorf("parseFormFloat(\"0\") = %v, want 0", got)
	}
}

func TestRowLineTotal(t *testing.T) {
	rows := map[string]polRow{
		"1": {Qty: "2", Cost: "10.00"},   // 20
		"2": {Qty: "1.5", Cost: "4"},     // 6
		"3": {Qty: "bad", Cost: "5"},     // qty unparseable -> 0
		"4": {Qty: "3", Cost: ""},        // cost empty -> 0
	}
	got := rowLineTotal(rows)
	if got != 26.0 {
		t.Errorf("rowLineTotal = %g, want 26", got)
	}
	if total := rowLineTotal(map[string]polRow{}); total != 0 {
		t.Errorf("rowLineTotal(empty) = %g, want 0", total)
	}
}

func TestSafePath(t *testing.T) {
	root := filepath.Join("some", "root")

	cases := []struct {
		name  string
		splat string
		want  string // expected returned path
	}{
		{"empty stays at root", "", root},
		{"nested path", "sub/dir/file.txt", filepath.Join(root, "sub", "dir", "file.txt")},
		{"leading slash ignored", "/etc/passwd", filepath.Join(root, "etc", "passwd")},
		{"dotdot segments stripped", "../../etc", filepath.Join(root, "etc")},
		{"interior dotdot stripped", "foo/../bar", filepath.Join(root, "foo", "bar")},
	}
	for _, c := range cases {
		got, ok := safePath(root, c.splat)
		if !ok {
			t.Errorf("%s: safePath(%q, %q) ok=false, want true", c.name, root, c.splat)
			continue
		}
		if got != c.want {
			t.Errorf("%s: safePath(%q, %q) = %q, want %q", c.name, root, c.splat, got, c.want)
		}
		if strings.Contains(got, "..") {
			t.Errorf("%s: safePath result %q still contains ..", c.name, got)
		}
	}
}
