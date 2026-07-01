package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
)

func (h *Handler) ContactsList(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "contacts.html", map[string]any{
		"ActiveTab": "contacts", "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) ContactsRows(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	type row struct {
		ID       int    `json:"id"`
		SUID     *int   `json:"suid"`
		Supplier string `json:"supplier"`
		Name     string `json:"name"`
		Email    string `json:"email"`
		Country  string `json:"country"`
		State    string `json:"state"`
		City     string `json:"city"`
		Phone    string `json:"phone"`
		Web      string `json:"web"`
		Modified string `json:"modified"`
		Notes    string `json:"notes"`
		Active   bool   `json:"active"`
	}
	cn, su := h.cfg.ContactTable(), h.cfg.CompanyTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT cn.id, cn.company_id, cn.display_name, cn.email, cn.phone_1,
		       cn.city, cn.state, cn.country, cn.website,
		       cn.is_active, cn.notes, cn.updated_at, su.name
		FROM %s cn
		LEFT JOIN %s su ON cn.company_id = su.id
		ORDER BY su.name, cn.display_name ASC
	`, cn, su))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := make([]row, 0)
	for rows.Next() {
		var c row
		var cnsuid sql.NullInt64
		var name, email, phone, city, state, country, web, notes, suName sql.NullString
		var active sql.NullBool
		var modified sql.NullTime
		if err := rows.Scan(
			&c.ID, &cnsuid, &name, &email, &phone,
			&city, &state, &country, &web,
			&active, &notes, &modified, &suName,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if cnsuid.Valid {
			v := int(cnsuid.Int64)
			c.SUID = &v
		}
		c.Name = name.String
		c.Email = email.String
		c.Phone = phone.String
		c.City = city.String
		c.State = state.String
		c.Country = country.String
		c.Web = web.String
		c.Notes = notes.String
		c.Active = active.Bool
		c.Supplier = suName.String
		if modified.Valid {
			c.Modified = modified.Time.Format("2006-01-02")
		}
		out = append(out, c)
	}
	log.Printf("[rows] contacts: %d rows in %v", len(out), time.Since(start))
	writeJSON(w, out)
}

func (h *Handler) ContactDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, ok := h.fetchContact(w, r, id)
	if !ok {
		return
	}
	h.setNavContext(w, r, fmt.Sprintf("/contact/%d", c.CNID), c.CNName)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "contact_detail.html", map[string]any{
		"Contact": c, "ActiveTab": "contacts",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) ContactsNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "contact_edit.html", map[string]any{
		"Contact": models.Contact{}, "IsNew": true,
		"ActiveTab": "contacts",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) ContactsCreate(w http.ResponseWriter, r *http.Request) {
	name := fv(r, "CNName")
	if name == "" {
		h.render(w, r, "contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": true,
			"Error": "Contact name is required", "ActiveTab": "contacts",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	var newID int
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (display_name, company_id, email, phone_1, phone_2, fax,
		                address, city, state, zipcode, country,
		                website, is_active, notes, updated_at)
		OUTPUT INSERTED.id
		VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15)
	`, h.cfg.ContactTable()),
		name, nullableInt(fv(r, "CNSUID")),
		fv(r, "CNEmail"), fv(r, "CNPhone1"), fv(r, "CNPhone2"), fv(r, "CNFAX"),
		fv(r, "CNAddress"), fv(r, "CNCity"), fv(r, "CNState"), fv(r, "CNZipcode"), fv(r, "CNCountry"),
		fv(r, "CNWeb"),
		r.FormValue("CNActive") == "1",
		fv(r, "CNNotes"), time.Now(),
	).Scan(&newID)
	if err != nil {
		h.render(w, r, "contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": true,
			"Error": "Error creating contact: " + err.Error(), "ActiveTab": "contacts",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/contact/%d", newID), http.StatusFound)
}

func (h *Handler) ContactEdit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, ok := h.fetchContact(w, r, id)
	if !ok {
		return
	}
	h.setNavContext(w, r, fmt.Sprintf("/contact/%d", c.CNID), c.CNName)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "contact_edit.html", map[string]any{
		"Contact": c, "IsNew": false,
		"ActiveTab": "contacts",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) ContactUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := fv(r, "CNName")
	if name == "" {
		h.render(w, r, "contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": false,
			"Error": "Contact name is required", "ActiveTab": "contacts",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET
		  display_name=@p1, company_id=@p2, email=@p3, phone_1=@p4, phone_2=@p5, fax=@p6,
		  address=@p7, city=@p8, state=@p9, zipcode=@p10, country=@p11,
		  website=@p12, is_active=@p13, notes=@p14, updated_at=@p15
		WHERE id=@p16
	`, h.cfg.ContactTable()),
		name, nullableInt(fv(r, "CNSUID")),
		fv(r, "CNEmail"), fv(r, "CNPhone1"), fv(r, "CNPhone2"), fv(r, "CNFAX"),
		fv(r, "CNAddress"), fv(r, "CNCity"), fv(r, "CNState"), fv(r, "CNZipcode"), fv(r, "CNCountry"),
		fv(r, "CNWeb"),
		r.FormValue("CNActive") == "1",
		fv(r, "CNNotes"), time.Now(), id,
	)
	if err != nil {
		h.render(w, r, "contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": false,
			"Error": "Error saving contact: " + err.Error(), "ActiveTab": "contacts",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/contact/%s", id), http.StatusFound)
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (h *Handler) fetchContact(w http.ResponseWriter, r *http.Request, id string) (models.Contact, bool) {
	cn, su := h.cfg.ContactTable(), h.cfg.CompanyTable()
	var c models.Contact
	var cnsuid sql.NullInt64
	var name, email, phone1, phone2, fax, address, city, state, zip, country sql.NullString
	var web, userLink, notes, suName sql.NullString
	var active sql.NullBool
	var dateModified sql.NullTime
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT cn.id, cn.company_id, cn.display_name, cn.email,
		       cn.phone_1, cn.phone_2, cn.fax,
		       cn.address, cn.city, cn.state, cn.zipcode, cn.country,
		       cn.website, cn.user_account_link, cn.notes, cn.is_active, cn.updated_at,
		       su.name
		FROM %s cn
		LEFT JOIN %s su ON cn.company_id = su.id
		WHERE cn.id = @p1
	`, cn, su), id).Scan(
		&c.CNID, &cnsuid, &name, &email,
		&phone1, &phone2, &fax,
		&address, &city, &state, &zip, &country,
		&web, &userLink, &notes, &active, &dateModified, &suName,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Contact not found")
		return c, false
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving contact: "+err.Error())
		return c, false
	}
	if cnsuid.Valid {
		v := int(cnsuid.Int64)
		c.CNSUID = &v
	}
	c.CNName = name.String
	c.CNEmail = email.String
	c.CNPhone1 = phone1.String
	c.CNPhone2 = phone2.String
	c.CNFAX = fax.String
	c.CNAddress = address.String
	c.CNCity = city.String
	c.CNState = state.String
	c.CNZipcode = zip.String
	c.CNCountry = country.String
	c.CNWeb = web.String
	c.CNUserAccountLink = userLink.String
	c.CNNotes = notes.String
	c.CNActive = active.Bool
	c.SupplierName = suName.String
	if dateModified.Valid {
		c.CNDateModified = &dateModified.Time
	}
	return c, true
}

func contactFromForm(r *http.Request) models.Contact {
	c := models.Contact{
		CNName: fv(r, "CNName"), CNEmail: fv(r, "CNEmail"),
		CNPhone1: fv(r, "CNPhone1"), CNPhone2: fv(r, "CNPhone2"), CNFAX: fv(r, "CNFAX"),
		CNAddress: fv(r, "CNAddress"), CNCity: fv(r, "CNCity"), CNState: fv(r, "CNState"),
		CNZipcode: fv(r, "CNZipcode"), CNCountry: fv(r, "CNCountry"),
		CNWeb: fv(r, "CNWeb"),
		CNNotes: fv(r, "CNNotes"), CNActive: r.FormValue("CNActive") == "1",
	}
	if v := fv(r, "CNSUID"); v != "" {
		// store as string; template will re-select the right option
		_ = v
	}
	return c
}
