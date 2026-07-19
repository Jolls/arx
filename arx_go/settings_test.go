package main

import "testing"

// TestSanitizeLandingRoute characterizes sanitizeLandingRoute's actual behavior
// (issue #758) rather than an assumed one: it strips scheme/host via
// url.RequestURI() and only rejects input that doesn't reduce to a rooted
// path. Full absolute URLs (and protocol-relative "//host" references) are
// NOT rejected here — they're reduced to a same-origin-looking path. The
// stated "//" guard in the source never actually fires for a "//host" input,
// since net/url's RequestURI() clears the path to "/" for a network-path
// reference rather than returning a leading "//". Downstream, landingRoute
// provides defense-in-depth against exactly this case (see TestLandingRoute).
func TestSanitizeLandingRoute(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{"empty", "", "", false},
		{"whitespace only", "   ", "", false},
		{"plain internal path", "/parts", "/parts", true},
		{"path with query", "/pos?f0=as", "/pos?f0=as", true},
		{"protocol-relative not rejected here", "//evil.com", "/", true},
		{"absolute URL reduced to path", "http://evil.com/x", "/x", true},
		{"absolute URL bare host reduced to root", "https://evil.com", "/", true},
		{"javascript URI rejected", "javascript:alert(1)", "", false},
		{"malformed escape rejected", "%zz", "", false},
		{"relative path without leading slash rejected", "relative/path", "", false},
		{"root path accepted", "/", "/", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := sanitizeLandingRoute(tt.input)
			if got != tt.want || ok != tt.ok {
				t.Errorf("sanitizeLandingRoute(%q) = (%q, %v), want (%q, %v)", tt.input, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestLandingRoute covers the actual post-login redirect boundary: it
// combines sanitizeLandingRoute's output (persisted as DefaultRoute) with its
// own loop/protocol-relative guards.
func TestLandingRoute(t *testing.T) {
	tests := []struct {
		name string
		user *User
		want string
	}{
		{"nil user", nil, "/parts"},
		{"empty default route", &User{DefaultRoute: ""}, "/parts"},
		{"root path loop guard", &User{DefaultRoute: "/"}, "/parts"},
		{"root path with query loop guard", &User{DefaultRoute: "/?f=1"}, "/parts"},
		{"protocol-relative defense in depth", &User{DefaultRoute: "//evil.com"}, "/parts"},
		{"valid passthrough", &User{DefaultRoute: "/pos"}, "/pos"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := landingRoute(tt.user); got != tt.want {
				t.Errorf("landingRoute(%+v) = %q, want %q", tt.user, got, tt.want)
			}
		})
	}
}
