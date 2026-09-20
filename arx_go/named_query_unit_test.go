package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPINamedQuery_BadSpecPrefix(t *testing.T) {
	h := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/named-query?spec=badprefix:foo", nil)
	rec := httptest.NewRecorder()
	h.APINamedQuery(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// The named-query editor persists and runs caller-supplied SQL. isSafeQuery
// blocks writes but not reads, so without an admin gate any authenticated user
// could SELECT from any table the app DB user can reach — including password
// hashes (#103). Both editor routes must reject a non-admin.
//
// GET /api/named-query is deliberately not covered here: it resolves a stored,
// admin-curated query by name and is used by spec_nom auto-fill for every user.
func TestNamedQuerySettings_AdminOnly(t *testing.T) {
	routes := []struct {
		name    string
		handler func(*Handler) func(http.ResponseWriter, *http.Request)
	}{
		{"save", func(h *Handler) func(http.ResponseWriter, *http.Request) { return h.SettingsNamedQueryRowSave }},
		{"test", func(h *Handler) func(http.ResponseWriter, *http.Request) { return h.SettingsNamedQueryTest }},
	}

	callers := []struct {
		name string
		user *User
	}{
		{"anonymous", nil},
		{"non-admin", &User{ID: 8002, Username: "tester"}},
	}

	for _, rt := range routes {
		for _, c := range callers {
			t.Run(rt.name+"/"+c.name, func(t *testing.T) {
				h := testHandler()
				req := httptest.NewRequest(http.MethodPost, "/settings/named-queries/"+rt.name, nil)
				if c.user != nil {
					req = req.WithContext(context.WithValue(req.Context(), ctxUserKey, c.user))
				}
				rec := httptest.NewRecorder()
				rt.handler(h)(rec, req)

				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
				}
				// The settings page parses every response with r.json(), so the
				// rejection has to keep the JSON error shape, not fall back to
				// requireAdmin's plain-text 403.
				var body map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("response body is not JSON: %v (%q)", err, rec.Body.String())
				}
				if body["error"] == "" {
					t.Errorf("expected an error message, got %v", body)
				}
			})
		}

		t.Run(rt.name+"/admin passes the gate", func(t *testing.T) {
			h := testHandler()
			req := httptest.NewRequest(http.MethodPost, "/settings/named-queries/"+rt.name, nil)
			req = req.WithContext(context.WithValue(req.Context(),
				ctxUserKey, &User{ID: 8001, Username: "admin", IsAdmin: true}))
			rec := httptest.NewRecorder()
			rt.handler(h)(rec, req)

			// testHandler has no DB, so an admin falls through to the
			// not-connected check. Anything other than 403 proves the admin
			// gate let the request past.
			if rec.Code == http.StatusForbidden {
				t.Errorf("admin was rejected with 403")
			}
		})
	}
}
