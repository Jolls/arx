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

// fetchPartCategories reads part_category in editor order (#194).
func (h *Handler) fetchPartCategories(ctx context.Context) ([]models.Category, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT code, label, is_purchased, is_bom_visible, is_orders_visible, is_pricing_visible,
		        is_mfg_parts_visible, is_suppliers_visible, is_inventory_visible
		 FROM %s ORDER BY sort_order, code`, h.cfg().PartCategoryTable()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cats := []models.Category{}
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.Code, &c.Label, &c.Purchased, &c.BOM, &c.Orders, &c.Pricing,
			&c.MfgParts, &c.Suppliers, &c.Inventory); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, rows.Err()
}

// partCategoryUsage counts parts per category code.
func (h *Handler) partCategoryUsage(ctx context.Context) (map[string]int, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT category, COUNT(*) FROM %s WHERE category IS NOT NULL GROUP BY category`, h.cfg().PartsTable()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	usage := map[string]int{}
	for rows.Next() {
		var code string
		var n int
		if err := rows.Scan(&code, &n); err != nil {
			return nil, err
		}
		usage[code] = n
	}
	return usage, rows.Err()
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
	tbl := h.cfg().PartCategoryTable()
	tx, err := h.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT code FROM %s`, tbl))
	if err != nil {
		return err
	}
	var existing []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			rows.Close()
			return err
		}
		existing = append(existing, code)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	keep := map[string]bool{}
	for i, c := range cats {
		keep[c.Code] = true
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (code, label, is_purchased, is_bom_visible, is_orders_visible, is_pricing_visible,
			                is_mfg_parts_visible, is_suppliers_visible, is_inventory_visible, sort_order)
			VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10)
			ON CONFLICT (code) DO UPDATE SET label=EXCLUDED.label, is_purchased=EXCLUDED.is_purchased,
			  is_bom_visible=EXCLUDED.is_bom_visible, is_orders_visible=EXCLUDED.is_orders_visible,
			  is_pricing_visible=EXCLUDED.is_pricing_visible, is_mfg_parts_visible=EXCLUDED.is_mfg_parts_visible,
			  is_suppliers_visible=EXCLUDED.is_suppliers_visible, is_inventory_visible=EXCLUDED.is_inventory_visible,
			  sort_order=EXCLUDED.sort_order, updated_at=now()`, tbl),
			c.Code, c.Label, c.Purchased, c.BOM, c.Orders, c.Pricing, c.MfgParts, c.Suppliers, c.Inventory, i); err != nil {
			return err
		}
	}
	for _, code := range existing {
		if !keep[code] {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE code=@p1`, tbl), code); err != nil {
				return err
			}
		}
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
	usage, err := h.partCategoryUsage(r.Context())
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
