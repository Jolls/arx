package records

import "testing"

func TestUnitRecordSummaryState(t *testing.T) {
	cases := []struct {
		name string
		in   UnitRecordSummary
		want string
	}{
		{"any failure wins", UnitRecordSummary{Passed: 3, Failed: 1}, "Fail"},
		{"passes only", UnitRecordSummary{Passed: 2}, "Pass"},
		{"nothing evaluated", UnitRecordSummary{}, "Not evaluated"},
	}
	for _, c := range cases {
		if got := c.in.State(); got != c.want {
			t.Errorf("%s: State() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFormatSerial(t *testing.T) {
	for _, c := range []struct {
		prefix string
		pad, n int
		want   string
	}{
		{"", 0, 7, "7"},
		{"SK-", 4, 42, "SK-0042"},
		{"SK-", 4, 1000, "SK-1000"},
		{"SK-", 4, 12345, "SK-12345"}, // pad is a minimum width, not a cap
		{"", 3, 5, "005"},
		{"SK-", 0, 5, "SK-5"},
	} {
		if got := FormatSerial(c.prefix, c.pad, c.n); got != c.want {
			t.Errorf("FormatSerial(%q, %d, %d) = %q, want %q", c.prefix, c.pad, c.n, got, c.want)
		}
	}
}
