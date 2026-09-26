//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"arx/arx_go/models"
)

// withRestoredPartCategories snapshots the part_category rows and restores them
// (and the cache) after the test, so the suite never permanently changes them.
func withRestoredPartCategories(t *testing.T, h *Handler) {
	t.Helper()
	orig, err := h.fetchPartCategories(context.Background())
	if err != nil {
		t.Fatalf("snapshot part categories: %v", err)
	}
	t.Cleanup(func() {
		if err := h.savePartCategories(context.Background(), orig); err != nil {
			t.Errorf("restore part categories after test: %v", err)
		}
		h.loadPartCategories(context.Background())
	})
}

// withRestoredAttachmentCategories does the same for attachment_category.
func withRestoredAttachmentCategories(t *testing.T, h *Handler) {
	t.Helper()
	orig := h.loadAttachmentCategories(context.Background())
	t.Cleanup(func() {
		if err := h.saveAttachmentCategories(context.Background(), orig); err != nil {
			t.Errorf("restore attachment categories after test: %v", err)
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

// categoryFormValues encodes cats as the indexed rows the Settings editor posts.
func categoryFormValues(cats []models.Category) url.Values {
	vals := url.Values{"cat_count": {strconv.Itoa(len(cats))}}
	flag := func(b bool) string {
		if b {
			return "1"
		}
		return ""
	}
	for i, c := range cats {
		n := strconv.Itoa(i)
		vals.Set("code_"+n, c.Code)
		vals.Set("label_"+n, c.Label)
		vals.Set("purchased_"+n, flag(c.Purchased))
		vals.Set("bom_"+n, flag(c.BOM))
		vals.Set("orders_"+n, flag(c.Orders))
		vals.Set("pricing_"+n, flag(c.Pricing))
		vals.Set("mfgparts_"+n, flag(c.MfgParts))
		vals.Set("suppliers_"+n, flag(c.Suppliers))
		vals.Set("inventory_"+n, flag(c.Inventory))
	}
	return vals
}

// withExtraCategory returns the default categories as form rows plus one more.
func withExtraCategory(code, label string, extra map[string]string) url.Values {
	defaults := models.DefaultCategories()
	vals := categoryFormValues(defaults)
	n := strconv.Itoa(len(defaults))
	vals.Set("cat_count", strconv.Itoa(len(defaults)+1))
	vals.Set("code_"+n, code)
	vals.Set("label_"+n, label)
	for k, v := range extra {
		vals.Set(k+"_"+n, v)
	}
	return vals
}

// readPersistedCategories reads part_category directly (bypassing the cache).
func readPersistedCategories(t *testing.T, h *Handler) []models.Category {
	t.Helper()
	rows, err := h.queryContext(context.Background(), fmt.Sprintf(
		`SELECT code, label, is_purchased, is_bom_visible, is_orders_visible, is_pricing_visible,
		        is_mfg_parts_visible, is_suppliers_visible, is_inventory_visible
		 FROM %s ORDER BY sort_order, code`, h.cfg().PartCategoryTable()))
	if err != nil {
		t.Fatalf("SELECT part_category: %v", err)
	}
	defer rows.Close()
	cats := []models.Category{}
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.Code, &c.Label, &c.Purchased, &c.BOM, &c.Orders, &c.Pricing,
			&c.MfgParts, &c.Suppliers, &c.Inventory); err != nil {
			t.Fatal(err)
		}
		cats = append(cats, c)
	}
	return cats
}

// TestIntegration_SettingsCategoriesSave_PersistsRows: posted rows (the seeded
// set plus a new one) persist to part_category — code uppercased/trimmed, label
// trimmed, "1" flags true — and a cold loadPartCategories reflects them (#820, #194).
func TestIntegration_SettingsCategoriesSave_PersistsRows(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	withRestoredPartCategories(t, h)

	vals := withExtraCategory(" tst1 ", "  Test One  ", map[string]string{"bom": "1", "inventory": "1", "pricing": "0"})
	want := append(models.DefaultCategories(), models.Category{Code: "TST1", Label: "Test One",
		CategoryTabs: models.CategoryTabs{BOM: true, Inventory: true}})

	rec := httptest.NewRecorder()
	h.SettingsCategoriesSave(rec, postCategoriesSave(vals))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/settings" {
		t.Fatalf("SettingsCategoriesSave: status %d Location %q, want 302 /settings. body: %s",
			rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if got := readPersistedCategories(t, h); !reflect.DeepEqual(got, want) {
		t.Errorf("persisted part_category = %+v, want %+v", got, want)
	}
	h.update(func(s *runtimeState) { s.partCategories = nil })
	h.loadPartCategories(context.Background())
	if got := h.st().partCategories; !reflect.DeepEqual(got, want) {
		t.Errorf("cold loadPartCategories = %+v, want %+v", got, want)
	}
}

// TestIntegration_SettingsCategoriesSave_DropsEmptyAndDuplicateCodeRows: a
// blank code row is dropped, and a repeated code keeps the first row.
func TestIntegration_SettingsCategoriesSave_DropsEmptyAndDuplicateCodeRows(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	withRestoredPartCategories(t, h)

	defaults := models.DefaultCategories()
	vals := categoryFormValues(defaults)
	n, m := strconv.Itoa(len(defaults)), strconv.Itoa(len(defaults)+1)
	vals.Set("cat_count", strconv.Itoa(len(defaults)+2))
	vals.Set("code_"+n, "   ")
	vals.Set("label_"+n, "Should Be Dropped")
	vals.Set("code_"+m, "asm")
	vals.Set("label_"+m, "Duplicate Assembly")

	rec := httptest.NewRecorder()
	h.SettingsCategoriesSave(rec, postCategoriesSave(vals))
	if rec.Code != http.StatusFound {
		t.Fatalf("SettingsCategoriesSave: status %d, want 302. body: %s", rec.Code, rec.Body.String())
	}
	if got := readPersistedCategories(t, h); !reflect.DeepEqual(got, defaults) {
		t.Errorf("persisted part_category = %+v, want the defaults unchanged", got)
	}
}

// TestIntegration_SettingsCategoriesSave_RejectsRemovingInUse: dropping a code
// that parts use (BUY) is refused with a Settings error and writes nothing.
func TestIntegration_SettingsCategoriesSave_RejectsRemovingInUse(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	withRestoredPartCategories(t, h)
	h.loadPartCategories(context.Background())
	before := h.st().partCategories

	var kept []models.Category
	for _, c := range models.DefaultCategories() {
		if c.Code != "BUY" {
			kept = append(kept, c)
		}
	}
	rec := httptest.NewRecorder()
	h.SettingsCategoriesSave(rec, postCategoriesSave(categoryFormValues(kept)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (re-rendered Settings)", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Cannot remove part categories still used by parts", "BUY ("} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if got := readPersistedCategories(t, h); !reflect.DeepEqual(got, models.DefaultCategories()) {
		t.Errorf("part_category changed on rejected save: %+v", got)
	}
	if got := h.st().partCategories; !reflect.DeepEqual(got, before) {
		t.Errorf("cache changed on rejected save")
	}
}

// TestIntegration_SettingsCategoriesSave_RejectsEmpty: saving no categories is
// refused.
func TestIntegration_SettingsCategoriesSave_RejectsEmpty(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	withRestoredPartCategories(t, h)

	rec := httptest.NewRecorder()
	h.SettingsCategoriesSave(rec, postCategoriesSave(url.Values{"cat_count": {"0"}}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "At least one part category is required.") {
		t.Errorf("status %d, want 200 with the empty-list error", rec.Code)
	}
	if got := readPersistedCategories(t, h); !reflect.DeepEqual(got, models.DefaultCategories()) {
		t.Errorf("part_category changed on rejected save: %+v", got)
	}
}

// TestIntegration_PartCreate_SettingsAddedCategorySucceeds: a category added in
// Settings can be used on a part (the old fixed CHECK rejected it, #194).
func TestIntegration_PartCreate_SettingsAddedCategorySucceeds(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	withRestoredPartCategories(t, h)
	ctx := context.Background()

	rec := httptest.NewRecorder()
	h.SettingsCategoriesSave(rec, postCategoriesSave(withExtraCategory("ZZT", "Test Category", nil)))
	assert302(t, "SettingsCategoriesSave", rec)

	id, cl := seedPart(t, h, ctx, "ZZT")
	t.Cleanup(cl) // runs before the category restore (cleanups are LIFO)
	var got string
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT category FROM %s WHERE id=@p1`, h.cfg().PartsTable()), id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "ZZT" {
		t.Errorf("part category = %q, want ZZT", got)
	}
}

// TestIntegration_PartCreate_EmptyCategoryStoresNull: an empty category is
// stored as NULL (uncategorized) and the part page still renders.
func TestIntegration_PartCreate_EmptyCategoryStoresNull(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	id, cl := seedPart(t, h, ctx, "")
	t.Cleanup(cl)
	var isNull bool
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT category IS NULL FROM %s WHERE id=@p1`, h.cfg().PartsTable()), id).Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	if !isNull {
		t.Error("empty category not stored as NULL")
	}
	rec := httptest.NewRecorder()
	h.PartDetail(rec, withID(httptest.NewRequest(http.MethodGet, fmt.Sprintf("/part/%d", id), nil), id))
	assertStatus(t, "PartDetail", rec, http.StatusOK)
}

// TestIntegration_LoadBuildComponents_NullCategory: a BOM component with a NULL
// category loads (permissive tabs, so it is consumed from stock).
func TestIntegration_LoadBuildComponents_NullCategory(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	parent, clParent := seedPart(t, h, ctx, "ASM")
	t.Cleanup(clParent)
	comp, clComp := seedPart(t, h, ctx, "BUY")
	t.Cleanup(clComp)
	if _, err := h.execContext(ctx, fmt.Sprintf(`UPDATE %s SET category=NULL WHERE id=@p1`, h.cfg().PartsTable()), comp); err != nil {
		t.Fatal(err)
	}
	if _, err := h.execContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (parent_part_id, component_part_id, line_number, qty) VALUES (@p1, @p2, 1, 1)`,
		h.cfg().BOMTable()), parent, comp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE parent_part_id=@p1`, h.cfg().BOMTable()), parent)
	})

	comps, err := h.loadBuildComponents(ctx, parent)
	if err != nil {
		t.Fatalf("loadBuildComponents: %v", err)
	}
	if len(comps) != 1 || comps[0].PartID != comp {
		t.Errorf("components = %+v, want the NULL-category part %d", comps, comp)
	}
}

// TestIntegration_SettingsAttachmentCategoriesSave_PersistsOrder: the saved list
// is trimmed, de-duplicated (first wins) and kept in posted order.
func TestIntegration_SettingsAttachmentCategoriesSave_PersistsOrder(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	withRestoredAttachmentCategories(t, h)

	rec := httptest.NewRecorder()
	h.SettingsAttachmentCategoriesSave(rec, postForm("/settings/attachment-categories",
		url.Values{"attachment_categories": {"Zeta, Alpha,Zeta, "}}))
	if got, want := h.loadAttachmentCategories(context.Background()), []string{"Zeta", "Alpha"}; !reflect.DeepEqual(got, want) {
		t.Errorf("attachment categories = %v, want %v", got, want)
	}
}

// TestIntegration_PartCategories_SeedMatchesDefaults guards seed/model drift:
// the categories loaded from the seeded DB equal models.DefaultCategories().
func TestIntegration_PartCategories_SeedMatchesDefaults(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	h.loadPartCategories(context.Background())
	if got, want := h.st().partCategories, models.DefaultCategories(); !reflect.DeepEqual(got, want) {
		t.Errorf("loaded part categories = %+v, want DefaultCategories %+v", got, want)
	}
}

// seededAttachmentCategories is the seeded attachment-category list, in order.
var seededAttachmentCategories = []string{
	"Vendor Link", "Drawing", "CAD", "Datasheet", "Vendor Document", "Fabrication", "Schematic",
	"Quote", "BOM", "SOP", "Certificate", "Photo", "PDF Preview", "Thumbnail",
}

// TestIntegration_PartAttachments_CategoryOptions pins that the attachments
// page offers every seeded attachment category, in seed order.
func TestIntegration_PartAttachments_CategoryOptions(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)

	rec := httptest.NewRecorder()
	h.PartAttachments(rec, withID(httptest.NewRequest(http.MethodGet, "/part/3002/attachments", nil), 3002))
	assertStatus(t, "PartAttachments", rec, http.StatusOK)
	body := rec.Body.String()
	last := -1
	for _, c := range seededAttachmentCategories {
		i := strings.Index(body, `<option value="`+c+`">`)
		if i < 0 {
			t.Errorf("attachments page missing option %q", c)
			continue
		}
		if i < last {
			t.Errorf("option %q out of seed order", c)
		}
		last = i
	}
}
