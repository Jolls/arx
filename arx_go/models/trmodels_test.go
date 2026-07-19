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
