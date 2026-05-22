package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/parts_master_go/models"
)

func (h *Handler) ContactsList(w http.ResponseWriter, r *http.Request) {
	cn, su := h.cfg.ContactTable(), h.cfg.SupplierTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT cn.CNID, cn.CNSUID, cn.CNName, cn.CNEmail, cn.CNPhone1,
		       cn.CNCity, cn.CNState, cn.CNCountry, cn.CNWeb,
		       cn.CNActive, cn.CNNotes, cn.CNDateModified, su.name
		FROM %s cn
		LEFT JOIN %s su ON cn.CNSUID = su.id
		ORDER BY su.name, cn.CNName ASC
	`, cn, su))
	if err != nil {
		h.renderError(w, "Error connecting to database: "+err.Error())
		return
	}
	defer rows.Close()
	var contacts []models.Contact
	for rows.Next() {
		var c models.Contact
		var cnsuid sql.NullInt64
		var name, email, phone, city, state, country, web, notes, suName sql.NullString
		var active sql.NullBool
		var dateModified sql.NullTime
		if err := rows.Scan(
			&c.CNID, &cnsuid, &name, &email, &phone,
			&city, &state, &country, &web,
			&active, &notes, &dateModified, &suName,
		); err != nil {
			h.renderError(w, "Error reading contacts: "+err.Error())
			return
		}
		if cnsuid.Valid {
			v := int(cnsuid.Int64)
			c.CNSUID = &v
		}
		c.CNName = name.String
		c.CNEmail = email.String
		c.CNPhone1 = phone.String
		c.CNCity = city.String
		c.CNState = state.String
		c.CNCountry = country.String
		c.CNWeb = web.String
		c.CNNotes = notes.String
		c.CNActive = active.Bool
		c.SupplierName = suName.String
		if dateModified.Valid {
			c.CNDateModified = &dateModified.Time
		}
		contacts = append(contacts, c)
	}
	h.render(w, "contacts.html", map[string]any{
		"Contacts": contacts, "ActiveTab": "contacts", "TestMode": h.cfg.TestMode,
	})
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
	h.render(w, "contact_detail.html", map[string]any{
		"Contact": c, "ActiveTab": "contacts",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) ContactsNew(w http.ResponseWriter, r *http.Request) {
	suppliers := h.fetchSupplierList(r)
	h.render(w, "contact_edit.html", map[string]any{
		"Contact": models.Contact{}, "IsNew": true, "Suppliers": suppliers,
		"ActiveTab": "contacts",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) ContactsCreate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	name := fs(r, "CNName")
	if name == "" {
		suppliers := h.fetchSupplierList(r)
		h.render(w, "contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": true, "Suppliers": suppliers,
			"Error": "Contact name is required", "ActiveTab": "contacts",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	var newID int
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (CNName, CNSUID, CNEmail, CNPhone1, CNPhone2, CNFAX,
		                CNAddress, CNCity, CNState, CNZipcode, CNCountry,
		                CNWeb, CNUserAccountLink, CNActive, CNNotes, CNDateModified)
		OUTPUT INSERTED.CNID
		VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16)
	`, h.cfg.ContactTable()),
		name, nullableInt(fs(r, "CNSUID")),
		fs(r, "CNEmail"), fs(r, "CNPhone1"), fs(r, "CNPhone2"), fs(r, "CNFAX"),
		fs(r, "CNAddress"), fs(r, "CNCity"), fs(r, "CNState"), fs(r, "CNZipcode"), fs(r, "CNCountry"),
		fs(r, "CNWeb"), fs(r, "CNUserAccountLink"),
		r.FormValue("CNActive") == "1",
		fs(r, "CNNotes"), time.Now(),
	).Scan(&newID)
	if err != nil {
		suppliers := h.fetchSupplierList(r)
		h.render(w, "contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": true, "Suppliers": suppliers,
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
	suppliers := h.fetchSupplierList(r)
	h.setNavContext(w, r, fmt.Sprintf("/contact/%d", c.CNID), c.CNName)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, "contact_edit.html", map[string]any{
		"Contact": c, "IsNew": false, "Suppliers": suppliers,
		"ActiveTab": "contacts",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) ContactUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	name := fs(r, "CNName")
	if name == "" {
		suppliers := h.fetchSupplierList(r)
		h.render(w, "contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": false, "Suppliers": suppliers,
			"Error": "Contact name is required", "ActiveTab": "contacts",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET
		  CNName=@p1, CNSUID=@p2, CNEmail=@p3, CNPhone1=@p4, CNPhone2=@p5, CNFAX=@p6,
		  CNAddress=@p7, CNCity=@p8, CNState=@p9, CNZipcode=@p10, CNCountry=@p11,
		  CNWeb=@p12, CNUserAccountLink=@p13, CNActive=@p14, CNNotes=@p15, CNDateModified=@p16
		WHERE CNID=@p17
	`, h.cfg.ContactTable()),
		name, nullableInt(fs(r, "CNSUID")),
		fs(r, "CNEmail"), fs(r, "CNPhone1"), fs(r, "CNPhone2"), fs(r, "CNFAX"),
		fs(r, "CNAddress"), fs(r, "CNCity"), fs(r, "CNState"), fs(r, "CNZipcode"), fs(r, "CNCountry"),
		fs(r, "CNWeb"), fs(r, "CNUserAccountLink"),
		r.FormValue("CNActive") == "1",
		fs(r, "CNNotes"), time.Now(), id,
	)
	if err != nil {
		suppliers := h.fetchSupplierList(r)
		h.render(w, "contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": false, "Suppliers": suppliers,
			"Error": "Error saving contact: " + err.Error(), "ActiveTab": "contacts",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/contact/%s", id), http.StatusFound)
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (h *Handler) fetchContact(w http.ResponseWriter, r *http.Request, id string) (models.Contact, bool) {
	cn, su := h.cfg.ContactTable(), h.cfg.SupplierTable()
	var c models.Contact
	var cnsuid sql.NullInt64
	var name, email, phone1, phone2, fax, address, city, state, zip, country sql.NullString
	var web, userLink, notes, suName sql.NullString
	var active sql.NullBool
	var dateModified sql.NullTime
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT cn.CNID, cn.CNSUID, cn.CNName, cn.CNEmail,
		       cn.CNPhone1, cn.CNPhone2, cn.CNFAX,
		       cn.CNAddress, cn.CNCity, cn.CNState, cn.CNZipcode, cn.CNCountry,
		       cn.CNWeb, cn.CNUserAccountLink, cn.CNNotes, cn.CNActive, cn.CNDateModified,
		       su.name
		FROM %s cn
		LEFT JOIN %s su ON cn.CNSUID = su.id
		WHERE cn.CNID = @p1
	`, cn, su), id).Scan(
		&c.CNID, &cnsuid, &name, &email,
		&phone1, &phone2, &fax,
		&address, &city, &state, &zip, &country,
		&web, &userLink, &notes, &active, &dateModified, &suName,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, "Contact not found")
		return c, false
	}
	if err != nil {
		h.renderError(w, "Error retrieving contact: "+err.Error())
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

func (h *Handler) fetchSupplierList(r *http.Request) []models.Supplier {
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(
		`SELECT id, name FROM %s ORDER BY name`, h.cfg.SupplierTable(),
	))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []models.Supplier
	for rows.Next() {
		var s models.Supplier
		var name sql.NullString
		if err := rows.Scan(&s.ID, &name); err == nil {
			s.Name = name.String
			out = append(out, s)
		}
	}
	return out
}

func contactFromForm(r *http.Request) models.Contact {
	c := models.Contact{
		CNName: fs(r, "CNName"), CNEmail: fs(r, "CNEmail"),
		CNPhone1: fs(r, "CNPhone1"), CNPhone2: fs(r, "CNPhone2"), CNFAX: fs(r, "CNFAX"),
		CNAddress: fs(r, "CNAddress"), CNCity: fs(r, "CNCity"), CNState: fs(r, "CNState"),
		CNZipcode: fs(r, "CNZipcode"), CNCountry: fs(r, "CNCountry"),
		CNWeb: fs(r, "CNWeb"), CNUserAccountLink: fs(r, "CNUserAccountLink"),
		CNNotes: fs(r, "CNNotes"), CNActive: r.FormValue("CNActive") == "1",
	}
	if v := fs(r, "CNSUID"); v != "" {
		// store as string; template will re-select the right option
		_ = v
	}
	return c
}
