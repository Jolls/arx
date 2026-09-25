//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// seedContact creates a contact under companyID via ContactsCreate and returns
// its id plus a cleanup that hard-deletes it. Mirrors seedSupplier's shape.
func seedContact(t *testing.T, h *Handler, ctx context.Context, companyID int) (int, func()) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ContactsCreate(rec, postForm("/contacts", url.Values{
		"CNName":   {smokeUniq("SMOKE-CON")},
		"CNSUID":   {strconv.Itoa(companyID)},
		"CNActive": {"1"},
	}))
	id := locID(t, rec, "/contact/")
	return id, func() {
		smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=@p1", h.cfg().ContactTable()), id)
	}
}

// TestIntegration_ContactsRows verifies ContactsRows' JSON output against
// pinned seed contacts, including that inactive contacts (2004) are still
// listed — unlike siblingContacts, which filters them out (#817).
func TestIntegration_ContactsRows(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.ContactsRows(rec, httptest.NewRequest(http.MethodGet, "/contacts/rows", nil))
	assertStatus(t, "ContactsRows", rec, http.StatusOK)

	var rows []struct {
		ID       int    `json:"id"`
		Supplier string `json:"supplier"`
		Active   bool   `json:"active"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	byID := map[int]struct {
		Supplier string
		Active   bool
	}{}
	for _, r := range rows {
		byID[r.ID] = struct {
			Supplier string
			Active   bool
		}{r.Supplier, r.Active}
	}
	if got, ok := byID[2001]; !ok || got.Supplier != "Acme Fasteners" || !got.Active {
		t.Errorf("contact 2001 = %+v (present=%v), want Supplier=Acme Fasteners Active=true", got, ok)
	}
	if got, ok := byID[2004]; !ok || got.Active {
		t.Errorf("contact 2004 = %+v (present=%v), want present with Active=false", got, ok)
	}
}

// TestIntegration_ContactDetail verifies ContactDetail's assembled page:
// fetchContact's full column scan, siblingContacts' is_active filter, and
// contactPOs wired into the rendered page (#817).
func TestIntegration_ContactDetail(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.ContactDetail(rec, withID(httptest.NewRequest(http.MethodGet, "/contact/2001", nil), 2001))
	assertStatus(t, "ContactDetail", rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{"John Doe", "100 Fastener Blvd", "Dayton", "Jane Smith", "5003"} {
		if !strings.Contains(body, want) {
			t.Errorf("ContactDetail(2001): body missing %q", want)
		}
	}
	if strings.Contains(body, "Sam Retired") {
		t.Errorf("ContactDetail(2001): body unexpectedly contains inactive sibling %q", "Sam Retired")
	}
}

// TestIntegration_ContactDetail_NotFound verifies the not-found guard renders
// (200, error text) rather than an HTTP error status (#817).
func TestIntegration_ContactDetail_NotFound(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.ContactDetail(rec, withID(httptest.NewRequest(http.MethodGet, "/contact/999999999", nil), 999999999))
	assertStatus(t, "ContactDetail not found", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Contact not found") {
		t.Errorf("ContactDetail(999999999): body missing %q, got: %s", "Contact not found", rec.Body.String())
	}
}

// TestIntegration_ContactEdit verifies the edit form renders the pinned
// contact's data (#817).
func TestIntegration_ContactEdit(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.ContactEdit(rec, withID(httptest.NewRequest(http.MethodGet, "/contact/2003/edit", nil), 2003))
	assertStatus(t, "ContactEdit", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Bob Lee") {
		t.Errorf("ContactEdit(2003): body missing %q", "Bob Lee")
	}
}

// TestIntegration_ContactEdit_NotFound verifies ContactEdit's not-found guard (#817).
func TestIntegration_ContactEdit_NotFound(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.ContactEdit(rec, withID(httptest.NewRequest(http.MethodGet, "/contact/999999999/edit", nil), 999999999))
	if !strings.Contains(rec.Body.String(), "Contact not found") {
		t.Errorf("ContactEdit(999999999): body missing %q", "Contact not found")
	}
}

// TestIntegration_ContactUpdate verifies ContactUpdate persists every changed
// field, using a throwaway contact (never a pinned 2001-2005 row, which
// TestIntegration_UpdatedAtSentinel asserts stay untouched) (#817).
func TestIntegration_ContactUpdate(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	contactID, cl := seedContact(t, h, ctx, 1001)
	defer cl()

	newName := smokeUniq("SMOKE-CON-UPDATED")
	rec := httptest.NewRecorder()
	h.ContactUpdate(rec, withID(postForm(fmt.Sprintf("/contact/%d", contactID), url.Values{
		"CNName":   {newName},
		"CNEmail":  {"updated@example.com"},
		"CNSUID":   {"1002"},
		"CNActive": {"0"},
	}), contactID))
	assert302(t, "ContactUpdate", rec)

	var name, email string
	var companyID int
	var active bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT display_name, email, company_id, is_active FROM %s WHERE id=@p1`, h.cfg().ContactTable()), contactID,
	).Scan(&name, &email, &companyID, &active); err != nil {
		t.Fatalf("select updated contact: %v", err)
	}
	if name != newName || email != "updated@example.com" || companyID != 1002 || active {
		t.Errorf("contact %d = name=%q email=%q company=%d active=%v, want name=%q email=updated@example.com company=1002 active=false",
			contactID, name, email, companyID, active, newName)
	}
}

// TestIntegration_ContactUpdate_MissingName verifies the required-name guard
// re-renders (no redirect) and makes no partial write (#817).
func TestIntegration_ContactUpdate_MissingName(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	contactID, cl := seedContact(t, h, ctx, 1001)
	defer cl()

	var before string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT display_name FROM %s WHERE id=@p1`, h.cfg().ContactTable()), contactID,
	).Scan(&before); err != nil {
		t.Fatalf("select seed contact: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ContactUpdate(rec, withID(postForm(fmt.Sprintf("/contact/%d", contactID), url.Values{
		"CNName": {""},
	}), contactID))
	assertStatus(t, "ContactUpdate missing name", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Contact name is required") {
		t.Errorf("ContactUpdate missing name: body missing %q", "Contact name is required")
	}

	var after string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT display_name FROM %s WHERE id=@p1`, h.cfg().ContactTable()), contactID,
	).Scan(&after); err != nil {
		t.Fatalf("select contact after rejected update: %v", err)
	}
	if after != before {
		t.Errorf("display_name changed on rejected update: %q -> %q, want unchanged", before, after)
	}
}

// TestIntegration_ContactsNew closes the "entirely untested" coverage gap for
// the empty-model new-contact form with a non-500 check (#817).
func TestIntegration_ContactsNew(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.ContactsNew(rec, httptest.NewRequest(http.MethodGet, "/contacts/new", nil))
	assertStatus(t, "ContactsNew", rec, http.StatusOK)
}
