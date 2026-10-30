package main

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"arx/internal/inventory"

	"github.com/shopspring/decimal"
)

// Red tests for #411: Lots/Builds/Units list pages get column filters and every
// filter row comes from one shared partial.

func TestFilterRow_NoDuplicatedMarkup(t *testing.T) {
	var files []string
	err := fs.WalkDir(templatesFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		if strings.Contains(readTemplate(t, p), `class="filter-row"`) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "templates/shared/layout.html" {
		t.Errorf(`class="filter-row" must live only in templates/shared/layout.html, found in: %v`, files)
	}
}

func TestFilterRow_UnknownTableErrors(t *testing.T) {
	if _, err := filterRow("no-such-table"); err == nil {
		t.Error("filterRow with an unknown name should return an error, not an empty row")
	}
}

func TestFilterTable_NewPagesHaveFilterRow(t *testing.T) {
	for page, url := range map[string]string{
		"all_lots.html":   "/api/lots/rows",
		"all_units.html":  "/api/units/rows",
		"all_builds.html": "/api/builds/rows",
	} {
		out := renderPartsList(t, page, map[string]any{})
		for _, want := range []string{`class="filter-row"`, `data-rows-url="` + url + `"`} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %s", page, want)
			}
		}
	}
}

func TestRowBuildersCoverListEndpoints(t *testing.T) {
	js, err := os.ReadFile("static/shared/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"/api/lots/rows", "/api/units/rows", "/api/builds/rows"} {
		if got := strings.Count(string(js), "'"+url+"'"); got < 2 {
			t.Errorf("app.js: %s needs a ROW_BUILDERS and a CELL_TEXT entry, found %d", url, got)
		}
	}
}

func TestLotListRows_UserZone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	lots := []LotRow{{ID: 1, PartID: 2, LotNumber: "L-1", VendorLot: "V-1", LotDescription: "ld", PartNumber: "P-1",
		PartDescription: "pd", Notes: "n", CreatedAt: time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC), IsActive: true}}
	want := lotListRow{ID: 1, PartID: 2, Lot: "L-1", Vendor: "V-1", LotDesc: "ld", PN: "P-1", PartDesc: "pd", Notes: "n", Created: "2026-01-02", Active: true}
	if got := lotListRows(lots, time.UTC); len(got) != 1 || got[0] != want {
		t.Errorf("UTC: got %+v, want [%+v]", got, want)
	}
	// Audit timestamps display in the viewing user's zone (#192): 03:00 UTC Jan 2 is still Jan 1 in LA.
	want.Created = "2026-01-01"
	if got := lotListRows(lots, la); len(got) != 1 || got[0] != want {
		t.Errorf("LA: got %+v, want [%+v]", got, want)
	}
}

func TestUnitListRows(t *testing.T) {
	lot := 7
	units := []UnitRow{
		{ID: 1, PartID: 2, SerialNumber: "SN-1", PartNumber: "ASM-1", PartDescription: "d", LotID: &lot, LotNumber: "L-7",
			CreatedAt: time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC), IsActive: true},
		{ID: 3, PartID: 2, SerialNumber: "SN-NOLOT", PartNumber: "ASM-1", CreatedAt: time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)},
	}
	got := unitListRows(units, time.UTC)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if want := (unitListRow{ID: 1, PartID: 2, SN: "SN-1", PN: "ASM-1", PartDesc: "d", LotID: 7, Lot: "L-7", Created: "2026-01-02", Active: true}); got[0] != want {
		t.Errorf("row 0: got %+v, want %+v", got[0], want)
	}
	if got[1].LotID != 0 || got[1].Active {
		t.Errorf("row 1: want LotID 0 and inactive, got %+v", got[1])
	}
}

func TestBuildListRows(t *testing.T) {
	lot := 9
	builds := []inventory.BuildListRow{
		{ID: 5, PartID: 2, PartNumber: "ASM-1", PartDescription: "d", Qty: decimal.NewFromInt(3), Date: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC), OutputLotID: &lot, LotNumber: "L-9", Note: "n"},
		{ID: 6, PartID: 2, PartNumber: "ASM-1", Qty: decimal.NewFromInt(1), Date: time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)},
	}
	got := buildListRows(builds)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if want := (buildListRow{ID: 5, PartID: 2, PN: "ASM-1", PartDesc: "d", Qty: "3", Date: "2026-05-25", OutLotID: 9, Lot: "L-9", Note: "n"}); got[0] != want {
		t.Errorf("row 0: got %+v, want %+v", got[0], want)
	}
	if got[1].OutLotID != 0 || got[1].Date != "2026-05-26" {
		t.Errorf("row 1: got %+v", got[1])
	}
}
