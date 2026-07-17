package config

import "testing"

func TestTokenizeUserPath(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\alice`)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"under profile", `C:\Users\alice\OneDrive - Acme\POs`, `%USERPROFILE%\OneDrive - Acme\POs`},
		{"case-insensitive prefix", `c:\users\ALICE\OneDrive - Acme\POs`, `%USERPROFILE%\OneDrive - Acme\POs`},
		{"forward slashes", `C:/Users/alice/OneDrive - Acme/POs`, `%USERPROFILE%\OneDrive - Acme\POs`},
		{"doubled backslashes", `C:\\Users\\alice\\OneDrive - Acme\\POs`, `%USERPROFILE%\OneDrive - Acme\POs`},
		{"outside profile", `C:\DocControl`, `C:\DocControl`},
		{"unc share", `\\server\share\POs`, `\\server\share\POs`},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TokenizeUserPath(c.in); got != c.want {
				t.Errorf("TokenizeUserPath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeSeparators(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:/Users/alice/POs`, `C:\Users\alice\POs`},
		{`C:\\Users\\alice\\POs`, `C:\Users\alice\POs`},
		{`\\server\share\POs`, `\\server\share\POs`},
		{`\\server\\share\\POs`, `\\server\share\POs`},
	}
	for _, c := range cases {
		if got := normalizeSeparators(c.in); got != c.want {
			t.Errorf("normalizeSeparators(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpandUserPath(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\bob`)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"tokenized", `%USERPROFILE%\OneDrive - Acme\POs`, `C:\Users\bob\OneDrive - Acme\POs`},
		{"case-insensitive token", `%userprofile%\OneDrive - Acme\POs`, `C:\Users\bob\OneDrive - Acme\POs`},
		{"no token", `C:\DocControl`, `C:\DocControl`},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExpandUserPath(c.in); got != c.want {
				t.Errorf("ExpandUserPath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestExpandUserPath_UnresolvableTokenLeftLiteral(t *testing.T) {
	t.Setenv("USERPROFILE", "")
	in := `%USERPROFILE%\OneDrive - Acme\POs`
	if got := ExpandUserPath(in); got != in {
		t.Errorf("ExpandUserPath(%q) = %q, want unchanged", in, got)
	}
}

func TestTokenizeExpandRoundTrip(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\carol`)
	path := `C:\Users\carol\OneDrive - Acme\POs`
	if got := ExpandUserPath(TokenizeUserPath(path)); got != path {
		t.Errorf("round trip = %q, want %q", got, path)
	}
}
