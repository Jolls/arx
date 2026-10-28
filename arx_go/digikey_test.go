package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func loadDigiKeyFixture(t *testing.T) *digikeyProductResponse {
	t.Helper()
	data, err := os.ReadFile("testdata/digikey_productdetails.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var parsed digikeyProductResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshaling fixture: %v", err)
	}
	return &parsed
}

func TestMapDigiKeyProduct(t *testing.T) {
	parsed := loadDigiKeyFixture(t)
	got := mapDigiKeyProduct(parsed, "TEST-1096-1-ND")

	if got.SupplierDesc != "CAP CER 0.1UF 50V X7R 0603" {
		t.Errorf("SupplierDesc = %q", got.SupplierDesc)
	}
	if got.MfgName != "Contoso Components" {
		t.Errorf("MfgName = %q", got.MfgName)
	}
	if got.MfgPartNumber != "CC0603C104K5R" {
		t.Errorf("MfgPartNumber = %q", got.MfgPartNumber)
	}
	if got.LeadTime != "8 weeks" {
		t.Errorf("LeadTime = %q, want %q", got.LeadTime, "8 weeks")
	}
	if got.DatasheetURL == "" || got.PhotoURL == "" {
		t.Errorf("expected DatasheetURL and PhotoURL to be populated")
	}

	// Requested the Cut Tape variation's DigiKey PN explicitly — its MOQ (1)
	// and price breaks must be used, not the Digi-Reel sibling's.
	if got.MinIncrement == nil || *got.MinIncrement != 1 {
		t.Errorf("MinIncrement = %v, want 1", got.MinIncrement)
	}
	if len(got.Prices) != 3 {
		t.Fatalf("len(Prices) = %d, want 3", len(got.Prices))
	}
	if got.Prices[1].BreakQuantity != 10 || got.Prices[1].UnitPrice != 0.05 {
		t.Errorf("Prices[1] = %+v", got.Prices[1])
	}
}

// TestMapDigiKeyProductMatchesRequestedVariation guards against always using
// ProductVariations[0]: when the user looked up the Digi-Reel sibling's PN,
// its MOQ/pricing must be used even though it's second in the array.
func TestMapDigiKeyProductMatchesRequestedVariation(t *testing.T) {
	parsed := loadDigiKeyFixture(t)
	got := mapDigiKeyProduct(parsed, "TEST-1096-2-ND")

	if got.MinIncrement == nil || *got.MinIncrement != 4000 {
		t.Errorf("MinIncrement = %v, want 4000 (Digi-Reel variation)", got.MinIncrement)
	}
	if len(got.Prices) != 1 || got.Prices[0].UnitPrice != 0.008 {
		t.Errorf("Prices = %+v, want the Digi-Reel variation's single break at $0.008", got.Prices)
	}
}

func TestMapDigiKeyProductLeadTimeSingular(t *testing.T) {
	var parsed digikeyProductResponse
	parsed.Product.ManufacturerLeadWeeks = "1"
	got := mapDigiKeyProduct(&parsed, "")
	if got.LeadTime != "1 week" {
		t.Errorf("LeadTime = %q, want %q", got.LeadTime, "1 week")
	}
}

// TestMapDigiKeyProductNormalizesProtocolRelativeURLs guards against a
// regression to the schemeless "//mm.digikey.com/..." URLs the live API
// returns for DatasheetUrl/PhotoUrl — net/http rejects those outright, so the
// download silently breaks with "unsupported protocol scheme" if the
// normalization in mapDigiKeyProduct is ever dropped.
func TestMapDigiKeyProductNormalizesProtocolRelativeURLs(t *testing.T) {
	var parsed digikeyProductResponse
	parsed.Product.DatasheetURL = "//mm.digikey.com/Volume0/opasdata/d220001/medias/docus/6592/TMCM-1260_hardware_manual_V120.pdf"
	parsed.Product.PhotoURL = "//media.digikey.com/photos/Trinamic%20Photos/TMCM-1260.jpg"
	got := mapDigiKeyProduct(&parsed, "")

	wantDatasheet := "https://mm.digikey.com/Volume0/opasdata/d220001/medias/docus/6592/TMCM-1260_hardware_manual_V120.pdf"
	if got.DatasheetURL != wantDatasheet {
		t.Errorf("DatasheetURL = %q, want %q", got.DatasheetURL, wantDatasheet)
	}
	wantPhoto := "https://media.digikey.com/photos/Trinamic%20Photos/TMCM-1260.jpg"
	if got.PhotoURL != wantPhoto {
		t.Errorf("PhotoURL = %q, want %q", got.PhotoURL, wantPhoto)
	}
}

func TestMapDigiKeyProductEmpty(t *testing.T) {
	var parsed digikeyProductResponse
	got := mapDigiKeyProduct(&parsed, "")
	if got.MinIncrement != nil {
		t.Errorf("MinIncrement = %v, want nil for a product with no variations", got.MinIncrement)
	}
	if got.LeadTime != "" {
		t.Errorf("LeadTime = %q, want empty when ManufacturerLeadWeeks is absent", got.LeadTime)
	}
	if len(got.Prices) != 0 {
		t.Errorf("Prices = %v, want none", got.Prices)
	}
}

// The datasheet/photo URL comes from the posted form, so the download client
// must not reach internal addresses.
func TestDialPublicOnly(t *testing.T) {
	for addr, wantOK := range map[string]bool{
		"127.0.0.1:80":         false,
		"[::1]:443":            false,
		"10.1.2.3:80":          false,
		"192.168.0.5:80":       false,
		"172.16.0.1:80":        false,
		"169.254.169.254:80":   false,
		"0.0.0.0:80":           false,
		"[::ffff:10.0.0.1]:80": false,
		"[fe80::1]:80":         false,
		"100.101.102.103:80":   false,
		"0.1.2.3:80":           false,
		"[64:ff9b::a00:1]:80":  false,
		"8.8.8.8:443":          true,
		"[2606:4700::1]:443":   true,
	} {
		if err := dialPublicOnly("tcp", addr, nil); (err == nil) != wantOK {
			t.Errorf("dialPublicOnly(%s) err = %v, want allowed=%v", addr, err, wantOK)
		}
	}
}

func TestFetchDigiKeyFileRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("internal"))
	}))
	defer srv.Close()
	if _, err := fetchDigiKeyFile(context.Background(), srv.URL); err == nil {
		t.Fatal("fetched from a loopback server, want error")
	}
}
