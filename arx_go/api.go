package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"arx/arxlib/folderpick"
)

func (h *Handler) APISupplierSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		writeJSON(w, []any{})
		return
	}
	supplierFilter := ""
	if r.URL.Query().Get("supplier_only") == "1" {
		supplierFilter = " AND su.is_supplier = 1"
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT su.id, su.name, cn.city
		FROM %s su
		LEFT JOIN %s cn ON su.default_contact = cn.id
		WHERE su.name LIKE @p1 AND su.is_active = 1`+supplierFilter+`
		ORDER BY su.name
		OFFSET 0 ROWS FETCH NEXT 20 ROWS ONLY
	`, h.cfg.CompanyTable(), h.cfg.ContactTable()), "%"+q+"%")
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	defer rows.Close()
	type result struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		City string `json:"city"`
	}
	var out []result
	for rows.Next() {
		var r result
		var name, city sql.NullString
		if rows.Scan(&r.ID, &name, &city) == nil {
			r.Name = name.String
			r.City = city.String
			out = append(out, r)
		}
	}
	writeJSON(w, out)
}

func (h *Handler) APISupplierContacts(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, display_name, address, city, state, zipcode,
		       country, phone_1, fax, email
		FROM %s
		WHERE company_id = @p1 AND is_active = 1
		ORDER BY display_name
	`, h.cfg.ContactTable()), id)
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	defer rows.Close()
	type result struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Address string `json:"address"`
		City    string `json:"city"`
		State   string `json:"state"`
		Zipcode string `json:"zipcode"`
		Country string `json:"country"`
		Phone   string `json:"phone"`
		Fax     string `json:"fax"`
		Email   string `json:"email"`
	}
	var out []result
	for rows.Next() {
		var c result
		var name, addr, city, state, zip, country, phone, fax, email sql.NullString
		if rows.Scan(&c.ID, &name, &addr, &city, &state, &zip, &country, &phone, &fax, &email) == nil {
			c.Name = name.String
			c.Address = addr.String
			c.City = city.String
			c.State = state.String
			c.Zipcode = zip.String
			c.Country = country.String
			c.Phone = phone.String
			c.Fax = fax.String
			c.Email = email.String
			out = append(out, c)
		}
	}
	writeJSON(w, out)
}

func (h *Handler) APIPartSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		writeJSON(w, []any{})
		return
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, part_number, revision, title, detail FROM %s
		WHERE part_number LIKE @p1
		ORDER BY part_number
		OFFSET 0 ROWS FETCH NEXT 25 ROWS ONLY
	`, h.cfg.PartsTable()), "%"+q+"%")
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	defer rows.Close()
	type result struct {
		PNID       int    `json:"pnid"`
		PartNumber string `json:"part_number"`
		Revision   string `json:"revision"`
		Title      string `json:"title"`
		Detail     string `json:"detail"`
	}
	var out []result
	for rows.Next() {
		var p result
		var partNumber, revision, title, detail sql.NullString
		if rows.Scan(&p.PNID, &partNumber, &revision, &title, &detail) == nil {
			p.PartNumber = partNumber.String
			p.Revision = revision.String
			p.Title = title.String
			p.Detail = detail.String
			out = append(out, p)
		}
	}
	writeJSON(w, out)
}

// APISupplierPN returns the supplier_pn for a (part, supplier) pair.
// GET /api/supplier-part?part_id=X&supplier_id=Y
func (h *Handler) APISupplierPN(w http.ResponseWriter, r *http.Request) {
	partID := r.URL.Query().Get("part_id")
	supplierID := r.URL.Query().Get("supplier_id")
	if partID == "" || supplierID == "" {
		writeJSON(w, map[string]string{"supplier_pn": ""})
		return
	}
	var pn sql.NullString
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT TOP 1 supplier_pn
		FROM %s
		WHERE part_id = @p1 AND supplier_id = @p2
		ORDER BY preference ASC
	`, h.cfg.SupplierPartTable()), partID, supplierID).Scan(&pn)
	if err != nil {
		writeJSON(w, map[string]string{"supplier_pn": ""})
		return
	}
	writeJSON(w, map[string]string{"supplier_pn": pn.String})
}

// APIBrowseFolder opens a native Windows folder-picker dialog and returns
// the selected path as JSON. Used by the Settings page browse buttons.
func (h *Handler) APIBrowseFolder(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"path": folderpick.BrowseFolderContext(r.Context())})
}

// APIBrowseFile opens a native Windows file-picker dialog and returns the
// selected absolute path as JSON. Used by the attachment Browse button.
func (h *Handler) APIBrowseFile(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"path": folderpick.BrowseFileContext(r.Context())})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if v == nil {
		w.Write([]byte("[]"))
		return
	}
	json.NewEncoder(w).Encode(v)
}
