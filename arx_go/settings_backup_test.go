package main

import "testing"

// The backup ZIP is handed to whoever can reach Settings, so app_config's
// credential rows must not travel in it — the same rule users.password_hash
// already follows. app_config is key/value, so this is a row-level exclusion
// rather than a column one (#104).
func TestSkipAppConfigSecret(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want bool
	}{
		{"digikey client secret is dropped", "digikey_client_secret", true},
		{"key casing is ignored", "DigiKey_Client_Secret", true},
		{"client id is not a secret", "digikey_client_id", false},
		{"shop config is kept", "attachment_categories", false},
		{"schema version is kept", "schema_version", false},
		{"company logo is kept", "company_logo", false},
		{"unknown key is kept", "something_new", false},
		{"missing setting_key is kept", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := map[string]string{"setting_value": "x"}
			if c.key != "" {
				row["setting_key"] = c.key
			}
			if got := skipAppConfigSecret(row); got != c.want {
				t.Errorf("skipAppConfigSecret(%q) = %v, want %v", c.key, got, c.want)
			}
		})
	}
}

// A driver may hand back a varchar as []byte rather than string. Rendering that
// with %v yields "[100 105 ...]", which would slip the secret row past the
// filter — cellText must normalize it first (#104).
func TestCellTextNormalizesBytes(t *testing.T) {
	if got := cellText([]byte("digikey_client_secret")); got != "digikey_client_secret" {
		t.Errorf("cellText([]byte) = %q, want the decoded string", got)
	}
	if got := cellText("digikey_client_secret"); got != "digikey_client_secret" {
		t.Errorf("cellText(string) = %q", got)
	}
	if got := cellText(42); got != "42" {
		t.Errorf("cellText(int) = %q, want %q", got, "42")
	}

	// End to end: a []byte key must still be dropped by the filter.
	row := map[string]string{"setting_key": cellText([]byte("digikey_client_secret"))}
	if !skipAppConfigSecret(row) {
		t.Error("secret row survived the filter when the key arrived as []byte")
	}
}
