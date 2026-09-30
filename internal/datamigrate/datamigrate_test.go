package datamigrate

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sourceFKs reads the real foreign keys exported from the Azure SQL ArxProd database.
func sourceFKs(t *testing.T) []FK {
	t.Helper()
	f, err := os.Open("../../docs/216-data-migration/source-export/foreign_keys.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	recs, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var fks []FK
	for _, rec := range recs[1:] {
		fks = append(fks, FK{Child: rec[1], ChildCol: rec[2], Parent: rec[3], ParentCol: rec[4]})
	}
	return fks
}

func TestLoadOrderBreaksTheRealFKCycles(t *testing.T) {
	fks := sourceFKs(t)
	var tables []string
	seen := map[string]bool{}
	for _, fk := range fks {
		for _, n := range []string{fk.Child, fk.Parent} {
			if !seen[n] {
				seen[n] = true
				tables = append(tables, n)
			}
		}
	}
	order, err := LoadOrder(tables, fks)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	deferred := map[tableColumn]bool{}
	for _, d := range deferredColumns {
		deferred[d] = true
	}
	for _, fk := range fks {
		if deferred[tableColumn{fk.Child, fk.ChildCol}] {
			continue
		}
		if pos[fk.Parent] > pos[fk.Child] {
			t.Errorf("%s loads before its parent %s", fk.Child, fk.Parent)
		}
	}
}

func TestLoadOrderReportsUndeferredCycle(t *testing.T) {
	_, err := LoadOrder([]string{"a", "b"}, []FK{{"a", "b_id", "b", "id"}, {"b", "a_id", "a", "id"}})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestConvertTimestamps(t *testing.T) {
	zones, err := testZones()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ table, col, in, want string }{
		{"form_events", "event_date", "2026-01-15 10:00:00.000", "2026-01-15 10:00:00+00:00"},       // server clock: UTC
		{"purchase_order", "date_modified", "2026-01-15 10:00:00.000", "2026-01-15 10:00:00-08:00"}, // desktop clock: Pacific
		{"purchase_order", "date_modified", "2026-07-15 10:00:00.250", "2026-07-15 10:00:00.25-07:00"},
	}
	for _, c := range cases {
		got, err := convert(c.table, c.col, "timestamp with time zone", c.in, zones)
		if err != nil || got != c.want {
			t.Errorf("%s.%s %s = %q, %v; want %q", c.table, c.col, c.in, got, err, c.want)
		}
	}
	if _, err := convert("part", "unclassified_at", "timestamp with time zone", "2026-01-01 00:00:00", zones); err == nil {
		t.Error("unclassified timestamptz column must be an error")
	}
	if got, _ := convert("form_record", "record_date", "timestamp without time zone", "2026-01-01 00:00:00.000", zones); got != "2026-01-01 00:00:00.000" {
		t.Errorf("zoneless TIMESTAMP must pass through, got %q", got)
	}
	if got, _ := convert("part", "x", "timestamp with time zone", NullMarker, zones); got != NullMarker {
		t.Errorf("NULL must stay NULL, got %q", got)
	}
}

func TestEveryClassifiedColumnIsClassifiedOnce(t *testing.T) {
	for k, clock := range timestampClocks {
		if clock != serverClock && clock != desktopClock {
			t.Errorf("%v has unknown clock %q", k, clock)
		}
	}
}

func TestFindOrphansReportsAllInOnePass(t *testing.T) {
	parent := &Table{Name: "company", Cols: []string{"id"}, Rows: [][]string{{"1"}, {"2"}}}
	child := &Table{Name: "contact", Cols: []string{"id", "company_id"},
		Rows: [][]string{{"10", "1"}, {"11", "99"}, {"12", NullMarker}, {"13", "98"}}}
	got := FindOrphans([]*Table{parent, child}, []FK{{"contact", "company_id", "company", "id"}})
	if len(got) != 2 || got[0].ID != "id 11" || got[1].ID != "id 13" {
		t.Fatalf("orphans = %v", got)
	}
}

func TestAppConfigFilter(t *testing.T) {
	tb := &Table{Name: "app_config", Cols: []string{"setting_key", "setting_value"}, Rows: [][]string{
		{"company_name", "Acme"}, {"secret_digikey_client_id", "x"}, {"schema_version", "10"},
		{"db_password", "x"}, {"part_categories", "[]"}, {"digikey_client_secret", "x"},
		{"some_api_key", "x"}, {"oauth_token", "x"},
	}}
	skipped, carried, version := filterAppConfig(tb)
	if len(tb.Rows) != 2 || len(skipped) != 6 || len(carried) != 2 || version != "10" {
		t.Fatalf("rows=%v skipped=%v carried=%v version=%q", tb.Rows, skipped, carried, version)
	}
}

func TestPrepareRefusesOtherSourceSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app_config.csv"),
		[]byte("\"setting_key\",\"setting_value\"\r\n\"schema_version\",\"9\"\r\n"), 0o644)
	s := &Schema{Columns: map[string][]Column{"app_config": {{"setting_key", "character varying"}, {"setting_value", "text"}}}}
	_, err := Prepare(dir, s, Options{ServerZone: "UTC", DesktopZone: "America/Los_Angeles"})
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("want schema_version refusal, got %v", err)
	}
}

func TestReadCSVKeepsCRLFInsideQuotes(t *testing.T) {
	rows, err := readCSV([]byte("\xef\xbb\xbf\"a\",\"b\"\r\n\"line1\r\nline2\",\"q\"\"x\"\r\n\r\n\\N,\"\"\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0][0] != "a" || rows[1][0] != "line1\r\nline2" || rows[1][1] != `q"x` ||
		rows[2][0] != NullMarker || rows[2][1] != "" {
		t.Fatalf("rows = %q", rows)
	}
	if _, err := readCSV([]byte("\"open")); err == nil {
		t.Error("unterminated quote must be an error")
	}
}

func TestSumsAreExact(t *testing.T) {
	tb := &Table{Cols: []string{"a", "n"}, Types: []string{"integer", "numeric"},
		Rows: [][]string{{"1", "0.1"}, {"2", "0.2"}, {"3", NullMarker}}}
	sums := tb.Sums()
	if _, ok := sums["a"]; ok {
		t.Error("integer columns are not summed")
	}
	if got := sums["n"].FloatString(8); got != "0.30000000" {
		t.Errorf("sum = %s", got)
	}
}

func TestCheckManifest(t *testing.T) {
	tb := &Table{Name: "po_line", Cols: []string{"qty"}, Types: []string{"numeric"}, Rows: [][]string{{"2.00"}}, SourceRows: 1}
	good := map[string]map[string]string{"po_line": {"rows": "1", "sum:qty": "2.00000000"}}
	if err := CheckManifest([]*Table{tb}, good); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]map[string]map[string]string{
		"rows":    {"po_line": {"rows": "2", "sum:qty": "2"}},
		"sum":     {"po_line": {"rows": "1", "sum:qty": "3"}},
		"missing": {},
		"unknown table": {"po_line": {"rows": "1", "sum:qty": "2"}, "brand_new_source_table": {"rows": "5"}},
	} {
		if err := CheckManifest([]*Table{tb}, m); err == nil {
			t.Errorf("%s mismatch not detected", name)
		}
	}
}

func TestRunRefusesWithoutMatchingConfirmation(t *testing.T) {
	env := func(string) string { return "" }
	cases := map[string][]string{
		"no confirm":       {"--csv-dir", "x", "--target-db", "ArxDev"},
		"mismatched":       {"--csv-dir", "x", "--target-db", "ArxDev", "--confirm-truncate", "Other"},
		"prod w/o allow":   {"--csv-dir", "x", "--target-db", "ArxProd", "--confirm-truncate", "ArxProd"},
		"no dsn":           {"--csv-dir", "x", "--target-db", "ArxDev", "--confirm-truncate", "ArxDev"},
		"missing csv-dir":  {"--target-db", "ArxDev", "--confirm-truncate", "ArxDev"},
		"unexpected extra": {"--csv-dir", "x", "--target-db", "ArxDev", "--confirm-truncate", "ArxDev", "more"},
	}
	for name, args := range cases {
		if err := Run(context.Background(), args, env, os.Stderr); err == nil {
			t.Errorf("%s: want refusal", name)
		}
	}
}

func TestPrepareRejectsUnmappedSourceColumn(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "uom.csv"), []byte("\"uom_id\",\"surprise\"\r\n\"1\",\"x\"\r\n"), 0o644)
	s := &Schema{Columns: map[string][]Column{"uom": {{"uom_id", "integer"}}}}
	_, err := Prepare(dir, s, Options{ServerZone: "UTC", DesktopZone: "America/Los_Angeles"})
	if err == nil || !strings.Contains(err.Error(), `"surprise"`) {
		t.Fatalf("want unmapped-column error, got %v", err)
	}
}

func TestPrepareRenamesDropsAndConverts(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "company.csv"), []byte(
		"\"id\",\"SUNotes\",\"date_modified\"\r\n\"1\",\"a, \"\"b\"\"\",\"2026-01-15 10:00:00.000\"\r\n\"2\",\\N,\\N\r\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "part.csv"), []byte("\"id\",\"is_lot_tracked\"\r\n\"1\",\"0\"\r\n"), 0o644)
	s := &Schema{Columns: map[string][]Column{
		"company": {{"id", "integer"}, {"notes", "character varying"}, {"date_modified", "timestamp with time zone"}},
		"part":    {{"id", "integer"}},
	}}
	p, err := Prepare(dir, s, Options{ServerZone: "UTC", DesktopZone: "America/Los_Angeles"})
	if err != nil {
		t.Fatal(err)
	}
	var company, part *Table
	for _, tb := range p.Tables {
		switch tb.Name {
		case "company":
			company = tb
		case "part":
			part = tb
		}
	}
	if got := strings.Join(company.Cols, ","); got != "id,notes,date_modified" {
		t.Errorf("company cols = %s", got)
	}
	if company.Rows[0][1] != `a, "b"` || company.Rows[0][2] != "2026-01-15 10:00:00-08:00" {
		t.Errorf("row 1 = %q", company.Rows[0])
	}
	if company.Rows[1][1] != NullMarker || company.Rows[1][2] != NullMarker {
		t.Errorf("row 2 = %q", company.Rows[1])
	}
	if len(part.Cols) != 1 || len(part.Rows[0]) != 1 {
		t.Errorf("dropped column still present: %v %v", part.Cols, part.Rows)
	}
}

// testZones resolves the default clock zones; shared by tests.
func testZones() (map[string]*time.Location, error) {
	utc, err := time.LoadLocation("UTC")
	if err != nil {
		return nil, err
	}
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return nil, err
	}
	return map[string]*time.Location{serverClock: utc, desktopClock: la}, nil
}
