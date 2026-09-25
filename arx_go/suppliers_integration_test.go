//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestIntegration_SuppliersRows verifies SuppliersRows' JSON output, including
// the default_contact join, against pinned seed data (#817).
func TestIntegration_SuppliersRows(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.SuppliersRows(rec, httptest.NewRequest(http.MethodGet, "/suppliers/rows", nil))
	assertStatus(t, "SuppliersRows", rec, http.StatusOK)

	var rows []struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Contact string `json:"contact"`
		Country string `json:"country"`
		Active  bool   `json:"active"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.ID == 1001 {
			found = true
			if r.Name != "Acme Fasteners" || r.Contact != "John Doe" || r.Country != "USA" || !r.Active {
				t.Errorf("supplier 1001 = %+v, want Name=Acme Fasteners Contact=John Doe Country=USA Active=true", r)
			}
		}
	}
	if !found {
		t.Fatalf("supplier 1001 not present in rows")
	}
}

// TestIntegration_SupplierDetail verifies SupplierDetail's assembled page:
// OtherContacts excludes both the default contact and inactive contacts, and
// TopParts is wired in (#817).
func TestIntegration_SupplierDetail(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.SupplierDetail(rec, withID(httptest.NewRequest(http.MethodGet, "/supplier/1001", nil), 1001))
	assertStatus(t, "SupplierDetail", rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{"Acme Fasteners", "Jane Smith", "RAW-1001"} {
		if !strings.Contains(body, want) {
			t.Errorf("SupplierDetail(1001): body missing %q", want)
		}
	}
	// "John Doe" (2001) is the default contact, so it legitimately appears once
	// in the Default Contact card — OtherContacts excluding it means it must not
	// appear a second time in the Other Contacts list.
	if n := strings.Count(body, "John Doe"); n != 1 {
		t.Errorf("SupplierDetail(1001): body contains %q %d times, want exactly 1 (Default Contact card only, not repeated in OtherContacts)", "John Doe", n)
	}
	if strings.Contains(body, "Sam Retired") {
		t.Errorf("SupplierDetail(1001): OtherContacts unexpectedly includes inactive contact %q", "Sam Retired")
	}
}

// TestIntegration_SupplierDetail_NotFound verifies fetchSupplier's not-found
// guard (#817).
func TestIntegration_SupplierDetail_NotFound(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.SupplierDetail(rec, withID(httptest.NewRequest(http.MethodGet, "/supplier/999999999", nil), 999999999))
	if !strings.Contains(rec.Body.String(), "Supplier not found") {
		t.Errorf("SupplierDetail(999999999): body missing %q", "Supplier not found")
	}
}

// TestIntegration_SupplierEdit verifies the edit form renders supplier data
// plus its contactsForSupplier dropdown (#817).
func TestIntegration_SupplierEdit(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.SupplierEdit(rec, withID(httptest.NewRequest(http.MethodGet, "/supplier/1002/edit", nil), 1002))
	assertStatus(t, "SupplierEdit", rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{"Precision Machining Co", "Bob Lee"} {
		if !strings.Contains(body, want) {
			t.Errorf("SupplierEdit(1002): body missing %q", want)
		}
	}
}

// TestIntegration_SupplierEdit_NotFound verifies SupplierEdit's not-found
// guard (#817).
func TestIntegration_SupplierEdit_NotFound(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.SupplierEdit(rec, withID(httptest.NewRequest(http.MethodGet, "/supplier/999999999/edit", nil), 999999999))
	if !strings.Contains(rec.Body.String(), "Supplier not found") {
		t.Errorf("SupplierEdit(999999999): body missing %q", "Supplier not found")
	}
}

// TestIntegration_SupplierUpdate verifies SupplierUpdate persists every
// changed field, using a throwaway company (never pinned 1001-1004, which
// other integration tests assert on by value) (#817).
func TestIntegration_SupplierUpdate(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	supplierID, cl := seedSupplier(t, h, ctx)
	defer cl()

	newName := smokeUniq("SMOKE-SUP-UPDATED")
	rec := httptest.NewRecorder()
	h.SupplierUpdate(rec, withID(postForm(fmt.Sprintf("/supplier/%d", supplierID), url.Values{
		"name":            {newName},
		"SUSupplierCode":  {"UPD1"},
		"is_active":       {"0"},
		"is_supplier":     {"1"},
		"is_manufacturer": {"0"},
		"default_contact": {""},
	}), supplierID))
	assert302(t, "SupplierUpdate", rec)

	var name, code string
	var active, isSupplier, isManufacturer bool
	var defaultContact *int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT name, SUSupplierCode, is_active, is_supplier, is_manufacturer, default_contact FROM %s WHERE id=@p1`,
		h.cfg().CompanyTable()), supplierID,
	).Scan(&name, &code, &active, &isSupplier, &isManufacturer, &defaultContact); err != nil {
		t.Fatalf("select updated supplier: %v", err)
	}
	if name != newName || code != "UPD1" || active || !isSupplier || isManufacturer || defaultContact != nil {
		t.Errorf("supplier %d = name=%q code=%q active=%v supplier=%v mfg=%v default_contact=%v, want name=%q code=UPD1 active=false supplier=true mfg=false default_contact=nil",
			supplierID, name, code, active, isSupplier, isManufacturer, defaultContact, newName)
	}
}

// TestIntegration_SupplierUpdate_MissingName verifies the required-name guard
// re-renders without writing (#817).
func TestIntegration_SupplierUpdate_MissingName(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	supplierID, cl := seedSupplier(t, h, ctx)
	defer cl()

	var before string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT name FROM %s WHERE id=@p1`, h.cfg().CompanyTable()), supplierID,
	).Scan(&before); err != nil {
		t.Fatalf("select seed supplier: %v", err)
	}

	rec := httptest.NewRecorder()
	h.SupplierUpdate(rec, withID(postForm(fmt.Sprintf("/supplier/%d", supplierID), url.Values{
		"name": {""},
	}), supplierID))
	assertStatus(t, "SupplierUpdate missing name", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Supplier name is required") {
		t.Errorf("SupplierUpdate missing name: body missing %q", "Supplier name is required")
	}

	var after string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT name FROM %s WHERE id=@p1`, h.cfg().CompanyTable()), supplierID,
	).Scan(&after); err != nil {
		t.Fatalf("select supplier after rejected update: %v", err)
	}
	if after != before {
		t.Errorf("name changed on rejected update: %q -> %q, want unchanged", before, after)
	}
}

// TestIntegration_SupplierUpdate_InvalidFolderStub verifies validateFolderStub
// short-circuits the UPDATE before it runs (#817).
func TestIntegration_SupplierUpdate_InvalidFolderStub(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	supplierID, cl := seedSupplier(t, h, ctx)
	defer cl()

	var before string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT SUSupplierCode FROM %s WHERE id=@p1`, h.cfg().CompanyTable()), supplierID,
	).Scan(&before); err != nil {
		t.Fatalf("select seed supplier: %v", err)
	}

	rec := httptest.NewRecorder()
	h.SupplierUpdate(rec, withID(postForm(fmt.Sprintf("/supplier/%d", supplierID), url.Values{
		"name":           {"Still Valid Name"},
		"SUSupplierCode": {"a/b"},
	}), supplierID))
	assertStatus(t, "SupplierUpdate invalid folder stub", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `folder stub cannot contain`) {
		t.Errorf("SupplierUpdate invalid folder stub: body missing folder-stub error, got: %s", rec.Body.String())
	}

	var after string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT SUSupplierCode FROM %s WHERE id=@p1`, h.cfg().CompanyTable()), supplierID,
	).Scan(&after); err != nil {
		t.Fatalf("select supplier after rejected update: %v", err)
	}
	if after != before {
		t.Errorf("SUSupplierCode changed on rejected update: %q -> %q, want unchanged", before, after)
	}
}

// TestIntegration_SuppliersNew closes the "entirely untested" coverage gap for
// the empty-model new-supplier form with a non-500 check (#817).
func TestIntegration_SuppliersNew(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.SuppliersNew(rec, httptest.NewRequest(http.MethodGet, "/suppliers/new", nil))
	assertStatus(t, "SuppliersNew", rec, http.StatusOK)
}
