package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"arx/arx_go/models"
	"arx/internal/parts"
)

// loadPartCategories caches the part_category rows on the Handler so the hot
// render path stays DB-free, falling back to the built-in defaults when no DB
// is connected or the read fails. Safe to call when db is nil.
func (h *Handler) loadPartCategories(ctx context.Context) {
	cats := models.DefaultCategories()
	if h.database() != nil {
		if rows, err := h.fetchPartCategories(ctx); err != nil {
			log.Printf("warning: could not load part categories: %v", err)
		} else {
			cats = rows
		}
	}
	h.update(func(s *runtimeState) { s.partCategories = cats })
}

func (h *Handler) parts() *parts.Service { return parts.New(handlerDB{h}) }

// modelCategory and serviceCategory convert between the parts service's flat
// Category and models.Category (which embeds CategoryTabs).
func modelCategory(c parts.Category) models.Category {
	return models.Category{Code: c.Code, Label: c.Label, Purchased: c.Purchased, CategoryTabs: models.CategoryTabs{
		BOM: c.BOM, Orders: c.Orders, Pricing: c.Pricing, MfgParts: c.MfgParts, Suppliers: c.Suppliers, Inventory: c.Inventory,
	}}
}

func serviceCategory(c models.Category) parts.Category {
	return parts.Category{Code: c.Code, Label: c.Label, Purchased: c.Purchased,
		BOM: c.BOM, Orders: c.Orders, Pricing: c.Pricing, MfgParts: c.MfgParts, Suppliers: c.Suppliers, Inventory: c.Inventory}
}

// fetchPartCategories reads part_category in editor order (#194).
func (h *Handler) fetchPartCategories(ctx context.Context) ([]models.Category, error) {
	cs, err := h.parts().ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	cats := make([]models.Category, 0, len(cs))
	for _, c := range cs {
		cats = append(cats, modelCategory(c))
	}
	return cats, nil
}

// categoriesInUse lists, sorted, the used codes that keep would remove.
func categoriesInUse(usage map[string]int, keep map[string]bool) []string {
	var blocked []string
	for code, n := range usage {
		if !keep[code] {
			blocked = append(blocked, fmt.Sprintf("%s (%d parts)", code, n))
		}
	}
	sort.Strings(blocked)
	return blocked
}

// savePartCategories upserts cats (in order) and deletes codes not in cats, in
// one transaction. FK_part_category rejects deleting a code a part still uses.
func (h *Handler) savePartCategories(ctx context.Context, cats []models.Category) error {
	tx, err := h.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	svcCats := make([]parts.Category, 0, len(cats))
	for _, c := range cats {
		svcCats = append(svcCats, serviceCategory(c))
	}
	if err := parts.New(tx).SaveCategories(ctx, svcCats); err != nil {
		return err
	}
	return tx.Commit()
}

// applyCategoryTabs resolves p.Category against the configured categories and
// sets p.Tabs, which the part_tabs partial uses to show/hide subtabs.
func (h *Handler) applyCategoryTabs(ctx context.Context, p *models.Part) {
	p.Tabs = models.TabsForCategory(h.st().partCategories, p.Category)
}

// SettingsCategoriesSave persists the part-category editor table to the
// part_category table (#194). Rows are submitted indexed (code_i, label_i,
// bom_i, ...) with cat_count rows; rows with an empty or repeated code are
// dropped. Removing a code that parts still use is refused.
func (h *Handler) SettingsCategoriesSave(w http.ResponseWriter, r *http.Request) {
	if h.database() == nil {
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}
	settingsError := func(msg string) {
		h.render(w, r, "settings/settings.html", h.settingsData(w, r, map[string]any{"Error": msg}))
	}
	n, _ := strconv.Atoi(r.FormValue("cat_count"))
	var cats []models.Category
	seen := map[string]bool{}
	for i := range n {
		code := strings.ToUpper(strings.TrimSpace(r.FormValue("code_" + strconv.Itoa(i))))
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		cats = append(cats, models.Category{
			Code:      code,
			Label:     strings.TrimSpace(r.FormValue("label_" + strconv.Itoa(i))),
			Purchased: r.FormValue("purchased_"+strconv.Itoa(i)) == "1",
			BOM:       r.FormValue("bom_"+strconv.Itoa(i)) == "1",
			Orders:    r.FormValue("orders_"+strconv.Itoa(i)) == "1",
			Pricing:   r.FormValue("pricing_"+strconv.Itoa(i)) == "1",
			MfgParts:  r.FormValue("mfgparts_"+strconv.Itoa(i)) == "1",
			Suppliers: r.FormValue("suppliers_"+strconv.Itoa(i)) == "1",
			Inventory: r.FormValue("inventory_"+strconv.Itoa(i)) == "1",
		})
	}
	if len(cats) == 0 {
		settingsError("At least one part category is required.")
		return
	}
	usage, err := h.parts().CategoryUsage(r.Context())
	if err != nil {
		settingsError("Could not save categories: " + err.Error())
		return
	}
	if blocked := categoriesInUse(usage, seen); len(blocked) > 0 {
		settingsError("Cannot remove part categories still used by parts: " + strings.Join(blocked, ", ") + ". Reassign those parts first.")
		return
	}
	if err := h.savePartCategories(r.Context(), cats); err != nil {
		settingsError("Could not save categories: " + err.Error())
		return
	}
	h.loadPartCategories(r.Context())
	http.Redirect(w, r, "/settings", http.StatusFound)
}
