package main

import "testing"

func TestValidateFolderStub(t *testing.T) {
	cases := []struct {
		input   string
		wantErr bool
	}{
		{"", false},
		{"ACME", false},
		{"ACME-01", false},
		{".", true},
		{"..", true},
		{"..\\evil", true},
		{"../evil", true},
		{"a/b", true},
		{"a\\b", true},
		{"a:b", true},
		{"a*b", true},
		{"a?b", true},
		{"a\"b", true},
		{"a<b", true},
		{"a>b", true},
		{"a|b", true},
	}
	for _, c := range cases {
		err := validateFolderStub(c.input)
		if (err != nil) != c.wantErr {
			t.Errorf("validateFolderStub(%q) error = %v, wantErr %v", c.input, err, c.wantErr)
		}
	}
}
