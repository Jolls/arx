package config

import (
	"testing"
)

func TestTableHelpers_BareNames(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*Config) string
		want string
	}{
		{"PartsTable", (*Config).PartsTable, "PN"},
		{"POTable", (*Config).POTable, "PO"},
		{"POLineTable", (*Config).POLineTable, "POL"},
		{"AttachmentsTable", (*Config).AttachmentsTable, "FIL"},
		{"CompanyTable", (*Config).CompanyTable, "company"},
		{"AppConfigTable", (*Config).AppConfigTable, "app_config"},
	}

	for _, testMode := range []bool{false, true} {
		cfg := &Config{}
		cfg.TestMode = testMode

		for _, tc := range cases {
			got := tc.fn(cfg)
			if got != tc.want {
				t.Errorf("TestMode=%v %s(): got %q, want %q", testMode, tc.name, got, tc.want)
			}
		}
	}
}
