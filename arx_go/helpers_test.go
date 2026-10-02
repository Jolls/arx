package main

import (
	"os"
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
		{"rfq", true}, // RFQ is in-progress (#270)
		{"draft", true},
		{"open", true},
		{"sent", true},
		{"partially_received", true},
		{"closed", false},
		{"cancelled", false},
		{"", false},
		{"DRAFT", false}, // case-sensitive
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

func TestNullableDecimal(t *testing.T) {
	// Empty, junk, ParseFloat-style specials and out-of-range exponents all read as nil;
	// "1e2000000000" parses under decimal.NewFromString but would hang the next Add/String.
	for _, in := range []string{"", "abc", "inf", "NaN", "1e2000000000", "1e-2000000000", "1e16"} {
		if got := nullableDecimal(in); got != nil {
			t.Errorf("nullableDecimal(%q) = %v, want nil", in, got)
		}
	}
	for in, want := range map[string]float64{" 2.5 ": 2.5, "0": 0, "1e3": 1000, "0.00000001": 0.00000001} {
		if got := nullableDecimal(in); got == nil || !got.Equal(d(want)) {
			t.Errorf("nullableDecimal(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := parseDecimal("1e2000000000"); err == nil {
		t.Error("parseDecimal accepted an out-of-range exponent")
	}
}

func TestRowLineTotal(t *testing.T) {
	rows := map[string]polRow{
		"1": {Qty: "2", Cost: "10.00"}, // 20
		"2": {Qty: "1.5", Cost: "4"},   // 6
		"3": {Qty: "bad", Cost: "5"},   // qty unparseable -> 0
		"4": {Qty: "3", Cost: ""},      // cost empty -> 0
	}
	got := rowLineTotal(rows)
	if !got.Equal(d(26)) {
		t.Errorf("rowLineTotal = %s, want 26", got)
	}
	if total := rowLineTotal(map[string]polRow{}); !total.IsZero() {
		t.Errorf("rowLineTotal(empty) = %s, want 0", total)
	}
}

// A symlink inside root that points outside it must be rejected (#117).
func TestSafePath_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	for _, splat := range []string{"link", "link/secret.txt", "link/not-yet-created.txt"} {
		if got, ok := safePath(root, splat); ok {
			t.Errorf("safePath(root, %q) = %q, ok=true, want rejected", splat, got)
		}
	}
	if err := os.Symlink(filepath.Join(outside, "missing.txt"), filepath.Join(root, "dangling")); err == nil {
		if got, ok := safePath(root, "dangling"); ok {
			t.Errorf("safePath(root, \"dangling\") = %q, ok=true, want rejected", got)
		}
	}
	if _, ok := safePath(root, "new-upload.txt"); !ok {
		t.Error("nonexistent leaf directly under root should be allowed")
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
