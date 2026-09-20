package main

import (
	"encoding/json"
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
