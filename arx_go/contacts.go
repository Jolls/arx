package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"arx/internal/contacts"
)

func (h *Handler) contacts() *contacts.Service { return contacts.New(handlerDB{h}) }

func (h *Handler) ContactsList(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "contacts/contacts.html", map[string]any{
		"ActiveTab": "contacts", "TestMode": h.cfg().TestMode,
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
	list, err := h.contacts().List(r.Context())
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	out := make([]row, 0, len(list))
	for _, c := range list {
		o := row{
			ID: c.ID, SUID: c.CompanyID, Supplier: c.SupplierName, Name: c.DisplayName,
			Email: c.Email, Country: c.Country, State: c.State, City: c.City,
			Phone: c.Phone1, Web: c.Website, Notes: c.Notes, Active: c.IsActive,
		}
		if c.UpdatedAt != nil {
			o.Modified = c.UpdatedAt.In(h.userLocation(r)).Format("2006-01-02")
		}
		out = append(out, o)
	}
	log.Printf("[rows] contacts: %d rows in %v", len(out), time.Since(start))
	writeJSON(w, out)
}

func (h *Handler) ContactDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, ok := h.fetchContact(w, r, id)
	if !ok {
		return
	}
	h.setNavContext(w, r, fmt.Sprintf("/contact/%d", c.ID), c.DisplayName)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	var siblings []contacts.Sibling
	if c.CompanyID != nil {
		siblings = h.siblingContacts(r.Context(), *c.CompanyID, c.ID)
	}
	h.render(w, r, "contacts/contact_detail.html", map[string]any{
		"Contact": c, "ActiveTab": "contacts",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
		"Siblings": siblings,
		"POs":      h.contactPOs(r.Context(), c.ID),
	})
}

// contactPOs returns the POs a contact is linked to via supplier_contact_id or
// receiver_contact_id, most recent first. Returns nil when none or on error.
func (h *Handler) contactPOs(ctx context.Context, contactID int) []contacts.PO {
	if contactID <= 0 {
		return nil
	}
	out, err := h.contacts().POs(ctx, contactID)
	if err != nil {
		return nil
	}
	return out
}

// siblingContacts returns other active contacts at the same supplier, excluding
// the current contact. Returns nil when there is no supplier or on error.
func (h *Handler) siblingContacts(ctx context.Context, supplierID, excludeContactID int) []contacts.Sibling {
	if supplierID <= 0 {
		return nil
	}
	out, err := h.contacts().Siblings(ctx, supplierID, excludeContactID)
	if err != nil {
		return nil
	}
	return out
}

func (h *Handler) ContactsNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "contacts/contact_edit.html", map[string]any{
		"Contact": contacts.Contact{}, "IsNew": true,
		"ActiveTab": "contacts",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

func (h *Handler) ContactsCreate(w http.ResponseWriter, r *http.Request) {
	name := fv(r, "CNName")
	if name == "" {
		h.render(w, r, "contacts/contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": true,
			"Error": "Contact name is required", "ActiveTab": "contacts",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		})
		return
	}
	newID, err := h.contacts().Create(r.Context(), contactInput(r))
	if err != nil {
		h.render(w, r, "contacts/contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": true,
			"Error": "Error creating contact: " + err.Error(), "ActiveTab": "contacts",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/contact/%d", newID), http.StatusFound)
}

func (h *Handler) ContactEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, ok := h.fetchContact(w, r, id)
	if !ok {
		return
	}
	h.setNavContext(w, r, fmt.Sprintf("/contact/%d", c.ID), c.DisplayName)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "contacts/contact_edit.html", map[string]any{
		"Contact": c, "IsNew": false,
		"ActiveTab":  "contacts",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

func (h *Handler) ContactUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := fv(r, "CNName")
	if name == "" {
		h.render(w, r, "contacts/contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": false,
			"Error": "Contact name is required", "ActiveTab": "contacts",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		})
		return
	}
	contactID, err := strconv.Atoi(id)
	if err != nil {
		h.renderError(w, r, "Contact not found")
		return
	}
	if err := h.contacts().Update(r.Context(), contactID, contactInput(r)); err != nil {
		h.render(w, r, "contacts/contact_edit.html", map[string]any{
			"Contact": contactFromForm(r), "IsNew": false,
			"Error": "Error saving contact: " + err.Error(), "ActiveTab": "contacts",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/contact/%s", id), http.StatusFound)
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (h *Handler) fetchContact(w http.ResponseWriter, r *http.Request, id string) (contacts.Contact, bool) {
	contactID, err := strconv.Atoi(id)
	if err != nil {
		h.renderError(w, r, "Contact not found")
		return contacts.Contact{}, false
	}
	c, err := h.contacts().Get(r.Context(), contactID)
	if errors.Is(err, sql.ErrNoRows) {
		h.renderError(w, r, "Contact not found")
		return c, false
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving contact: "+err.Error())
		return c, false
	}
	return c, true
}

// contactInput is contactFromForm plus the company id, for writes.
func contactInput(r *http.Request) contacts.Contact {
	c := contactFromForm(r)
	c.CompanyID = intPtrOrNil(fv(r, "CNSUID"))
	return c
}

func contactFromForm(r *http.Request) contacts.Contact {
	c := contacts.Contact{
		DisplayName: fv(r, "CNName"), Email: fv(r, "CNEmail"),
		Phone1: fv(r, "CNPhone1"), Phone2: fv(r, "CNPhone2"), Fax: fv(r, "CNFAX"),
		Address: fv(r, "CNAddress"), City: fv(r, "CNCity"), State: fv(r, "CNState"),
		Zipcode: fv(r, "CNZipcode"), Country: fv(r, "CNCountry"),
		Website: fv(r, "CNWeb"),
		Notes:   fv(r, "CNNotes"), IsActive: r.FormValue("CNActive") == "1",
	}
	if v := fv(r, "CNSUID"); v != "" {
		// store as string; template will re-select the right option
		_ = v
	}
	return c
}
