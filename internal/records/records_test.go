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
