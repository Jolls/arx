package main

import (
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
