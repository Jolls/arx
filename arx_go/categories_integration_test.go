//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"arx/arx_go/models"
)

// withRestoredPartCategories snapshots the current app_config part_categories
// row (an empty string if the row doesn't exist) and restores it via
// appConfigSet after the test, so this test suite never permanently changes
// ArxDev's configured categories. loadPartCategories treats a restored "" the
// same as a never-set row (falls back to DefaultCategories), so this is safe
// even if the row didn't originally exist.
func withRestoredPartCategories(t *testing.T, h *Handler) {
	t.Helper()
	ctx := context.Background()
	orig, _ := h.appConfigGet(ctx, partCategoriesKey)
	t.Cleanup(func() {
		if err := h.appConfigSet(context.Background(), partCategoriesKey, orig); err != nil {
			t.Errorf("restore part_categories after test: %v", err)
		}
	})
}

// postCategoriesSave builds a POST /settings/categories/save request with a
// URL-encoded body, mirroring postSettings in settings_save_test.go.
func postCategoriesSave(vals url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/settings/categories/save", strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// readPersistedCategories queries app_config directly (bypassing h.partCategories)
// and unmarshals the stored JSON, to assert on what's actually in the DB rather
// than only the in-memory cache SettingsCategoriesSave refreshes as a side effect.
func readPersistedCategories(t *testing.T, h *Handler) []models.Category {
	t.Helper()
	var raw string
	err := h.DB().QueryRowContext(context.Background(),
		`SELECT setting_value FROM `+h.cfg().AppConfigTable()+` WHERE setting_key=@p1`, partCategoriesKey,
	).Scan(&raw)
	if err != nil {
		t.Fatalf("SELECT part_categories from app_config: %v", err)
	}
	var cats []models.Category
	if err := json.Unmarshal([]byte(raw), &cats); err != nil {
		t.Fatalf("unmarshal persisted part_categories JSON %q: %v", raw, err)
	}
	return cats
}

// TestIntegration_SettingsCategoriesSave_PersistsRows covers issue #820: posting
// indexed category rows persists exactly those rows to app_config (uppercasing
// code, trimming code/label, and mapping each "1" tab flag to true / anything
// else to false), refreshes h.partCategories as a side effect, and a fresh
// loadPartCategories call (simulating a cold cache, e.g. process restart)
// reflects the same persisted rows — proving the DB write, not just the
// in-request cache refresh.
func TestIntegration_SettingsCategoriesSave_PersistsRows(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	withRestoredPartCategories(t, h)

	vals := url.Values{
		"cat_count":   {"2"},
		"code_0":      {"tst1"},
		"label_0":     {"  Test One  "},
		"bom_0":       {"1"},
		"orders_0":    {"1"},
		"pricing_0":   {"0"},
		"mfgparts_0":  {"1"},
		"suppliers_0": {""},
		"inventory_0": {"1"},
		"code_1":      {"TST2"},
		"label_1":     {"Test Two"},
		// row 1: no tab flags posted at all -> all false
	}
	want := []models.Category{
		{Code: "TST1", Label: "Test One", CategoryTabs: models.CategoryTabs{
			BOM: true, Orders: true, Pricing: false, MfgParts: true, Suppliers: false, Inventory: true,
		}},
		{Code: "TST2", Label: "Test Two", CategoryTabs: models.CategoryTabs{}},
	}

	rec := httptest.NewRecorder()
	h.SettingsCategoriesSave(rec, postCategoriesSave(vals))

	if rec.Code != http.StatusFound {
		t.Fatalf("SettingsCategoriesSave: status %d, want %d. body: %s", rec.Code, http.StatusFound, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/settings" {
		t.Errorf("SettingsCategoriesSave: redirect Location = %q, want \"/settings\"", loc)
	}

	if got := h.st().partCategories; !reflect.DeepEqual(got, want) {
		t.Errorf("h.partCategories after save = %+v, want %+v", got, want)
	}

	if got := readPersistedCategories(t, h); !reflect.DeepEqual(got, want) {
		t.Errorf("persisted app_config part_categories = %+v, want %+v", got, want)
	}

	// Simulate a cold cache (fresh process) to prove the DB write itself, not
	// just the in-request h.loadPartCategories refresh, is what's asserted.
	h.update(func(s *runtimeState) { s.partCategories = nil })
	h.loadPartCategories(context.Background())
	if got := h.st().partCategories; !reflect.DeepEqual(got, want) {
		t.Errorf("loadPartCategories after fresh call = %+v, want %+v", got, want)
	}
}

// TestIntegration_SettingsCategoriesSave_DropsEmptyCodeRows covers the
// documented "rows with an empty code are dropped" behavior: a blank/whitespace
// code_<i> at any index is skipped entirely (including its label/tabs), and
// the surrounding valid rows are persisted contiguously.
func TestIntegration_SettingsCategoriesSave_DropsEmptyCodeRows(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	withRestoredPartCategories(t, h)

	vals := url.Values{
		"cat_count": {"3"},
		"code_0":    {"one"},
		"label_0":   {"One"},
		"code_1":    {"   "}, // whitespace-only -> dropped
		"label_1":   {"Should Be Dropped"},
		"bom_1":     {"1"},
		"code_2":    {"two"},
		"label_2":   {"Two"},
	}
	want := []models.Category{
		{Code: "ONE", Label: "One"},
		{Code: "TWO", Label: "Two"},
	}

	rec := httptest.NewRecorder()
	h.SettingsCategoriesSave(rec, postCategoriesSave(vals))

	if rec.Code != http.StatusFound {
		t.Fatalf("SettingsCategoriesSave(drop-empty): status %d, want %d. body: %s", rec.Code, http.StatusFound, rec.Body.String())
	}

	if got := readPersistedCategories(t, h); !reflect.DeepEqual(got, want) {
		t.Errorf("persisted app_config part_categories = %+v, want %+v (empty-code row 1 should be dropped)", got, want)
	}
}
