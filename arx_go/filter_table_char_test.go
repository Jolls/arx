package main

import (
	"arx/arx_go/models"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Characterization of the 11 filtered list tables (#411): the rendered <table>
// (header row, filter row, colgroup, Loading row, data-rows-url) is pinned against
// testdata/filter_table/*.html so moving them onto the shared filter_table partial
// cannot silently change a column key, filter control or default. Regenerate with
// UPDATE_GOLDEN=1 only for an intentional markup change.

var (
	tableElemRE = regexp.MustCompile(`(?s)<table[^>]*data-rows-url[^>]*>.*?</table>`)
	wsRE        = regexp.MustCompile(`\s+`)
	gapRE       = regexp.MustCompile(`>\s+<`)
)

func normalizeHTML(s string) string {
	s = gapRE.ReplaceAllString(s, "><")
	return strings.TrimSpace(wsRE.ReplaceAllString(s, " "))
}

type filterTablePage struct {
	golden string
	key    string
	data   map[string]any
}

func filterTablePages() []filterTablePage {
	return []filterTablePage{
		{"suppliers", "layout:suppliers/suppliers.html", map[string]any{"ActiveTab": "suppliers"}},
		{"rfqs", "layout:pos/rfqs.html", map[string]any{"ActiveTab": "pos"}},
		{"pos", "layout:pos/pos.html", map[string]any{"ActiveTab": "pos"}},
		{"contacts", "layout:contacts/contacts.html", map[string]any{"ActiveTab": "suppliers"}},
		{"parts_index", "layout:parts/index.html", map[string]any{"ActiveTab": "parts", "Categories": []models.Category{{Code: "ASM", Label: "Assembly"}}}},
		{"part_unit_trace", "layout:parts/part_unit_trace.html", map[string]any{"ActiveTab": "parts", "Unit": UnitRow{}, "UserLoc": time.UTC, "TypeOptions": []string{"Acceptance"}}},
		{"part_lot_trace", "layout:parts/part_lot_trace.html", map[string]any{"ActiveTab": "parts", "Lot": LotRow{}, "UserLoc": time.UTC, "TypeOptions": []string{"Acceptance"}}},
		{"part_records", "layout:parts/part_records.html", map[string]any{"ActiveTab": "parts", "TypeOptions": []string{"Acceptance"}}},
		{"records_index_old", "records:index.html", map[string]any{"ActiveTab": "records"}},
		{"records_index", "records:records_index.html", map[string]any{"ActiveTab": "records", "LockedCount": 0, "TypeOptions": []string{"Acceptance"}}},
		{"test_report", "records:test_report.html", map[string]any{"ActiveTab": "records"}},
	}
}

func TestFilterTable_ExistingTablesStructure(t *testing.T) {
	tmpls, err := parseTemplates(templatesFS)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range filterTablePages() {
		t.Run(p.golden, func(t *testing.T) {
			tm := tmpls[p.key]
			if tm == nil {
				t.Fatalf("template %s not parsed", p.key)
			}
			out := renderToString(t, tm, "layout", coreLayoutFakeData(p.data))
			table := tableElemRE.FindString(out)
			if table == "" {
				t.Fatalf("%s: no table[data-rows-url] in output", p.key)
			}
			got := normalizeHTML(table)
			path := filepath.Join("testdata", "filter_table", p.golden+".html")
			if os.Getenv("UPDATE_GOLDEN") != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with UPDATE_GOLDEN=1 to create): %v", err)
			}
			if strings.TrimSpace(string(want)) != got {
				t.Errorf("%s table markup changed\n got: %s\nwant: %s", p.golden, got, strings.TrimSpace(string(want)))
			}
			if !strings.Contains(out, `aria-label="Pagination"`) || !strings.Contains(out, `class="record-count"`) {
				t.Errorf("%s: pagination nav or record-count missing", p.golden)
			}
		})
	}
}
