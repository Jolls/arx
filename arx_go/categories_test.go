package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	arxbase "arx/internal/config"
)

// TestSettingsCategoriesSave_NoDatabaseRedirects covers the h.database() == nil
// short-circuit: with no DB connected, SettingsCategoriesSave must redirect to
// /settings without attempting any app_config write (which would nil-pointer or
// error against a nil db).
func TestSettingsCategoriesSave_NoDatabaseRedirects(t *testing.T) {
	h := New(nil, nil, &arxbase.Config{}, templatesFS, nil)

	vals := url.Values{"cat_count": {"1"}, "code_0": {"BUY"}, "label_0": {"Purchased"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/categories/save", nil)
	req.PostForm = vals

	rec := httptest.NewRecorder()
	h.SettingsCategoriesSave(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("SettingsCategoriesSave(no db): status %d, want %d. body: %s", rec.Code, http.StatusFound, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/settings" {
		t.Errorf("SettingsCategoriesSave(no db): redirect Location = %q, want \"/settings\"", loc)
	}
}
