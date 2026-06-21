package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"arx/arx_go/models"
)

// partCategoriesKey is the app_config key holding the part-category list as JSON.
const partCategoriesKey = "part_categories"

// loadCategories returns the configured part categories, falling back to the
// built-in defaults when nothing is saved or the saved JSON is unreadable.
func (h *Handler) loadCategories(ctx context.Context) []models.Category {
	raw := h.appConfigGetOr(ctx, partCategoriesKey, "")
	if raw == "" {
		return models.DefaultCategories()
	}
	var cats []models.Category
	if err := json.Unmarshal([]byte(raw), &cats); err != nil || len(cats) == 0 {
		return models.DefaultCategories()
	}
	return cats
}

// applyCategoryTabs resolves p.Category against the configured categories and
// sets p.Tabs, which the part_tabs partial uses to show/hide subtabs.
func (h *Handler) applyCategoryTabs(ctx context.Context, p *models.Part) {
	p.Tabs = models.TabsForCategory(h.loadCategories(ctx), p.Category)
}

// SettingsCategoriesSave persists the part-category editor table to app_config.
// Rows are submitted indexed (code_i, label_i, bom_i, ...) with cat_count rows;
// rows with an empty code are dropped.
func (h *Handler) SettingsCategoriesSave(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}
	n, _ := strconv.Atoi(r.FormValue("cat_count"))
	var cats []models.Category
	for i := 0; i < n; i++ {
		code := strings.ToUpper(strings.TrimSpace(r.FormValue("code_" + strconv.Itoa(i))))
		if code == "" {
			continue
		}
		cats = append(cats, models.Category{
			Code:  code,
			Label: strings.TrimSpace(r.FormValue("label_" + strconv.Itoa(i))),
			CategoryTabs: models.CategoryTabs{
				BOM:       r.FormValue("bom_"+strconv.Itoa(i)) == "1",
				Orders:    r.FormValue("orders_"+strconv.Itoa(i)) == "1",
				Pricing:   r.FormValue("pricing_"+strconv.Itoa(i)) == "1",
				MfgParts:  r.FormValue("mfgparts_"+strconv.Itoa(i)) == "1",
				Suppliers: r.FormValue("suppliers_"+strconv.Itoa(i)) == "1",
				Inventory: r.FormValue("inventory_"+strconv.Itoa(i)) == "1",
			},
		})
	}
	data, _ := json.Marshal(cats)
	if err := h.appConfigSet(r.Context(), partCategoriesKey, string(data)); err != nil {
		h.renderError(w, r, "Could not save categories: "+err.Error())
		return
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
}
