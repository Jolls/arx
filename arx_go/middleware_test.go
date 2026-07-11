package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	arxbase "arx/arxlib/config"
)

// testHandler builds a Handler with no DB and no templates — enough to exercise
// the auth and CSRF middleware, which return before any rendering.
func testHandler() *Handler {
	cfg := &arxbase.Config{}
	cfg.SessionSecret = "test-secret"
	return New(nil, nil, cfg, nil, nil)
}

// sentinel reports whether the wrapped next-handler was reached.
func sentinel(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireAuth_RedirectsWhenNoDB(t *testing.T) {
	h := testHandler() // db == nil

	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	rec := httptest.NewRecorder()
	h.RequireAuth(sentinel(&reached)).ServeHTTP(rec, req)

	if reached {
		t.Error("next handler ran; expected redirect to /settings instead")
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/settings" {
		t.Errorf("Location = %q, want /settings", loc)
	}
}

func TestRequireCsrfOnPost_RejectsBadPost(t *testing.T) {
	h := testHandler()

	cases := []struct {
		name string
		body string
	}{
		{"no token", ""},
		{"wrong token", "csrf_token=not-the-real-token"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var reached bool
			req := httptest.NewRequest(http.MethodPost, "/part/1", strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			h.RequireCsrfOnPost(sentinel(&reached)).ServeHTTP(rec, req)

			if reached {
				t.Error("next handler ran; expected 403")
			}
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}
}

func TestRequireCsrfOnPost_AllowsGet(t *testing.T) {
	h := testHandler()

	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	rec := httptest.NewRecorder()
	h.RequireCsrfOnPost(sentinel(&reached)).ServeHTTP(rec, req)

	if !reached {
		t.Error("GET was blocked; CSRF check should only apply to POST")
	}
}

func TestRequireCsrfOnPost_AllowsValidPost(t *testing.T) {
	h := testHandler()

	// Seed a token into the session and capture the resulting cookie.
	seedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	seedRec := httptest.NewRecorder()
	token := h.csrfToken(seedRec, seedReq)
	cookies := seedRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("csrfToken did not set a session cookie")
	}

	// Replay the session cookie on a POST carrying the matching token.
	var reached bool
	body := "csrf_token=" + url.QueryEscape(token)
	req := httptest.NewRequest(http.MethodPost, "/part/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.RequireCsrfOnPost(sentinel(&reached)).ServeHTTP(rec, req)

	if !reached {
		t.Errorf("valid POST was blocked; status = %d", rec.Code)
	}
}
