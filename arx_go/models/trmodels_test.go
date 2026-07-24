package models

import (
	"reflect"
	"testing"
)

func TestParseIDList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []int
	}{
		{"empty", "", nil},
		{"basic list", "1,2,3", []int{1, 2, 3}},
		{"whitespace trimmed", " 1 , 2 ,3 ", []int{1, 2, 3}},
		{"empty token between commas skipped", "1,,3", []int{1, 3}},
		{"both tokens empty", ",", nil},
		{"zero id silently dropped", "0,5", []int{5}},
		{"non-numeric token dropped", "abc,5", []int{5}},
		{"partial-numeric token dropped entirely", "12abc,5", []int{5}},
		{"duplicates not deduplicated", "5,5,5", []int{5, 5, 5}},
		{"negative sign not supported", "-1", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseIDList(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseIDList(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestOrderedTestIDs(t *testing.T) {
	if got, want := (&TestForm{TestOrder: "3,1,2"}).OrderedTestIDs(), []int{3, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("OrderedTestIDs() = %v, want %v (order must be preserved, not sorted)", got, want)
	}
	if got := (&TestForm{TestOrder: ""}).OrderedTestIDs(); got != nil {
		t.Errorf("OrderedTestIDs() with empty TestOrder = %v, want nil", got)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestComputePassFail(t *testing.T) {
	tests := []struct {
		name  string
		value string
		step  *TestStep
		want  *bool
	}{
		{"nil step", "5", nil, nil},
		{"comment type always passes regardless of value", "anything", &TestStep{PFType: "comment"}, boolPtr(true)},
		{"filled type empty is missing", "", &TestStep{PFType: "filled"}, nil},
		{"filled type non-empty passes", "x", &TestStep{PFType: "filled"}, boolPtr(true)},
		{"filled type whitespace-only is missing", "   ", &TestStep{PFType: "filled"}, nil},
		{"attach type empty is missing", "", &TestStep{PFType: "attach"}, nil},
		{"attach type non-empty passes", "img.png", &TestStep{PFType: "attach"}, boolPtr(true)},
		{"pf_type case-insensitive", "x", &TestStep{PFType: "FILLED"}, boolPtr(true)},
		{"range empty value is missing", "", &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}, nil},
		{"empty pf_type routes to range behavior", "5", &TestStep{SpecMin: "1", SpecMax: "10"}, boolPtr(true)},
		{"range no bounds is unevaluatable", "5", &TestStep{PFType: "range"}, nil},
		{"range non-numeric value is unevaluatable", "abc", &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}, nil},
		{"range within bounds passes", "5", &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}, boolPtr(true)},
		{"range below min fails", "0", &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}, boolPtr(false)},
		{"range above max fails", "11", &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}, boolPtr(false)},
		{"range at min boundary passes", "1", &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}, boolPtr(true)},
		{"range at max boundary passes", "10", &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}, boolPtr(true)},
		{"range min only, value above passes", "100", &TestStep{PFType: "range", SpecMin: "1"}, boolPtr(true)},
		{"range min only, value below fails", "0", &TestStep{PFType: "range", SpecMin: "1"}, boolPtr(false)},
		{"range max only, value below passes", "0", &TestStep{PFType: "range", SpecMax: "10"}, boolPtr(true)},
		{"range max only, value above fails", "11", &TestStep{PFType: "range", SpecMax: "10"}, boolPtr(false)},
		{"range value is trimmed", "  5  ", &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}, boolPtr(true)},
		{"range bound is trimmed", "5", &TestStep{PFType: "range", SpecMin: " 1 ", SpecMax: " 10 "}, boolPtr(true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputePassFail(tt.value, tt.step)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("ComputePassFail(%q, %+v) = %v, want %v", tt.value, tt.step, got, tt.want)
			}
			if got != nil && *got != *tt.want {
				t.Errorf("ComputePassFail(%q, %+v) = %v, want %v", tt.value, tt.step, *got, *tt.want)
			}
		})
	}
}

func TestHasSnapshot(t *testing.T) {
	tests := []struct {
		name   string
		result *TestResult
		want   bool
	}{
		{"nil result", nil, false},
		{"all snapshot fields empty", &TestResult{}, false},
		{"pf_type set", &TestResult{PFType: "range"}, true},
		{"spec_min set", &TestResult{SpecMin: "1"}, true},
		{"spec_max set", &TestResult{SpecMax: "10"}, true},
		{"spec_nom set", &TestResult{SpecNom: "5"}, true},
		{"spec_units set", &TestResult{SpecUnits: "V"}, true},
		{"format set", &TestResult{Format: "0.00"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.HasSnapshot(); got != tt.want {
				t.Errorf("HasSnapshot() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEffectiveValue(t *testing.T) {
	tests := []struct {
		name string
		row  ResultRow
		want string
	}{
		{"nil step and result", ResultRow{}, ""},
		{"result value used when present", ResultRow{Result: &TestResult{Result: "5"}, Step: &TestStep{DefaultResult: "0"}}, "5"},
		{"falls back to step default when result empty", ResultRow{Result: &TestResult{Result: ""}, Step: &TestStep{DefaultResult: "0"}}, "0"},
		{"falls back to step default when no result", ResultRow{Result: nil, Step: &TestStep{DefaultResult: "0"}}, "0"},
		{"no step, no result value", ResultRow{Result: &TestResult{Result: ""}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.row.EffectiveValue(); got != tt.want {
				t.Errorf("EffectiveValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEffectiveParameter(t *testing.T) {
	tests := []struct {
		name string
		row  ResultRow
		want string
	}{
		{"nil step and result", ResultRow{}, ""},
		{"result snapshot used when present", ResultRow{Result: &TestResult{Parameter: "Voltage"}, Step: &TestStep{Parameter: "Old"}}, "Voltage"},
		{"falls back to step when result parameter empty", ResultRow{Result: &TestResult{Parameter: ""}, Step: &TestStep{Parameter: "Voltage"}}, "Voltage"},
		{"falls back to step when no result", ResultRow{Result: nil, Step: &TestStep{Parameter: "Voltage"}}, "Voltage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.row.EffectiveParameter(); got != tt.want {
				t.Errorf("EffectiveParameter() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEffectiveSpec(t *testing.T) {
	tests := []struct {
		name string
		row  ResultRow
		want string
	}{
		{"nil step and result", ResultRow{}, ""},
		{"result snapshot used when present", ResultRow{Result: &TestResult{Specification: "1-10V"}, Step: &TestStep{Specification: "Old"}}, "1-10V"},
		{"falls back to step when result spec empty", ResultRow{Result: &TestResult{Specification: ""}, Step: &TestStep{Specification: "1-10V"}}, "1-10V"},
		{"falls back to step when no result", ResultRow{Result: nil, Step: &TestStep{Specification: "1-10V"}}, "1-10V"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.row.EffectiveSpec(); got != tt.want {
				t.Errorf("EffectiveSpec() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCalcPF(t *testing.T) {
	tests := []struct {
		name string
		row  ResultRow
		want string
	}{
		{"heading row is blank", ResultRow{Level: 1, Step: &TestStep{PFType: "range", SpecMin: "1"}}, ""},
		{"nil step is blank", ResultRow{Step: nil}, ""},
		{"comment type passes with no value", ResultRow{Step: &TestStep{PFType: "comment"}}, "PASS"},
		{"filled type empty is missing", ResultRow{Step: &TestStep{PFType: "filled"}}, "MISSING"},
		{"attach type empty is missing", ResultRow{Step: &TestStep{PFType: "attach"}}, "MISSING"},
		{"range type with bounds and no value is missing", ResultRow{Step: &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}}, "MISSING"},
		{"range type with no bounds and no value is blank", ResultRow{Step: &TestStep{PFType: "range"}}, ""},
		{"range within bounds passes", ResultRow{Result: &TestResult{Result: "5"}, Step: &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}}, "PASS"},
		{"range outside bounds fails", ResultRow{Result: &TestResult{Result: "15"}, Step: &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}}, "FAIL"},
		{"non-numeric value with bounds is blank (unevaluatable)", ResultRow{Result: &TestResult{Result: "abc"}, Step: &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10"}}, ""},
		{"falls back to step default when no result", ResultRow{Step: &TestStep{PFType: "range", SpecMin: "1", SpecMax: "10", DefaultResult: "5"}}, "PASS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.row.CalcPF(); got != tt.want {
				t.Errorf("CalcPF() = %q, want %q", got, tt.want)
			}
		})
	}
}
