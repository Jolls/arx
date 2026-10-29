package main

import (
	"io/fs"
	"strings"
	"testing"
	"time"

	"arx/internal/inventory"
	"arx/internal/records"

	"github.com/shopspring/decimal"
)

func execPopout(t *testing.T, tmpl string, data map[string]any) string {
	t.Helper()
	m, err := parseTemplates(templatesFS)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := m["popouts"].ExecuteTemplate(&sb, tmpl, data); err != nil {
		t.Fatalf("%s: %v", tmpl, err)
	}
	return sb.String()
}

func requireAll(t *testing.T, name, out string, want, notWant []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("%s: missing %q\n%s", name, w, out)
		}
	}
	for _, w := range notWant {
		if strings.Contains(out, w) {
			t.Errorf("%s: unexpected %q\n%s", name, w, out)
		}
	}
}

func TestPopoutUnitFragment(t *testing.T) {
	lot := 7
	d := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	full := execPopout(t, "popout_unit", map[string]any{
		"ID": 1, "Serial": "SN-1", "PartID": 2, "PartNumber": "ASM-1", "IsActive": true,
		"LotID": &lot, "LotNumber": "L-7", "BuildLabel": "Build #5 - qty 1 - 2026-05-25",
		"RecTotal": 2, "RecPass": 1, "RecFail": 1, "RecMore": true,
		"Records": []records.UnitRecordSummary{
			{ID: 7014, Date: &d, Type: "New Release", Passed: 1, Failed: 1},
			{ID: 7011, Passed: 2},
		},
		"Consumers": []inventory.TraceNode{{NodeType: "lot", ID: 9, Number: "L-9", PartID: 3, PartNumber: "ASM-2", Qty: decimal.NewFromInt(1)}},
		"ConsMore":  true,
	})
	requireAll(t, "unit full", full, []string{
		`href="/part/2/units/1"`, `href="/part/2/lots/7"`, "Build #5", "2 records: 1 pass, 1 fail",
		`href="/records/7014"`, "Fail", "Pass", "Consumed into", `href="/part/3/lots/9"`, "and more",
	}, nil)
	empty := execPopout(t, "popout_unit", map[string]any{
		"ID": 3, "Serial": "SN-3", "PartID": 2, "PartNumber": "ASM-1", "IsActive": false,
		"LotID": (*int)(nil), "LotNumber": "", "BuildLabel": "",
		"RecTotal": 0, "RecPass": 0, "RecFail": 0, "RecMore": false,
		"Records": []records.UnitRecordSummary(nil), "Consumers": []inventory.TraceNode(nil), "ConsMore": false,
	})
	requireAll(t, "unit empty", empty, []string{"No test records.", "Not consumed into anything yet.", "&mdash;"}, []string{"and more", "/lots/"})
}

func TestPopoutBuildFragment(t *testing.T) {
	lot := 8301
	d := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	full := execPopout(t, "popout_build", map[string]any{
		"ID": 8203, "PartID": 9, "PartNumber": "ASM-9", "Qty": decimal.NewFromInt(2), "Date": d, "Username": "jo",
		"OutputLotID": &lot, "OutputLot": "L-1",
		"Consumed": []inventory.BuildConsumed{
			{NodeType: "lot", ID: 8302, Number: "L-2", PartID: 3012, PartNumber: "RAW-2", Qty: decimal.NewFromInt(4)},
			{NodeType: "unit", ID: 8503, Number: "SN-5", PartID: 3005, PartNumber: "ASM-5", Qty: decimal.NewFromInt(1)},
		},
		"ConsMore": false,
		"Units":    []inventory.BuildUnit{{ID: 8501, SerialNumber: "SN-1", PartID: 9, IsActive: true}}, "UnitsMore": true,
	})
	requireAll(t, "build full", full, []string{
		"Build #8203", `href="/part/9"`, `href="/part/9/lots/8301"`, `href="/part/3012/lots/8302"`,
		`href="/part/3005/units/8503"`, "Produced units", `href="/part/9/units/8501"`, "and more",
	}, nil)
	empty := execPopout(t, "popout_build", map[string]any{
		"ID": 1, "PartID": 9, "PartNumber": "ASM-9", "Qty": decimal.NewFromInt(1), "Date": d, "Username": "jo",
		"OutputLotID": (*int)(nil), "OutputLot": "",
		"Consumed": []inventory.BuildConsumed(nil), "ConsMore": false,
		"Units": []inventory.BuildUnit(nil), "UnitsMore": false,
	})
	requireAll(t, "build empty", empty, []string{"No components recorded.", "No units produced.", "No output lot"}, nil)
}

func TestPopoutPartThumbFragment(t *testing.T) {
	requireAll(t, "thumb", execPopout(t, "popout_part_thumb", map[string]any{"Thumb": "/local/t.png"}),
		[]string{`<img class="popout-thumb" src="/local/t.png"`}, nil)
	requireAll(t, "no thumb", execPopout(t, "popout_part_thumb", map[string]any{"Thumb": ""}), nil, []string{"<img"})
}

func TestPopoutJS_ThumbMechanismRemoved(t *testing.T) {
	src := readAppJS(t)
	for _, bad := range []string{"pn-thumb"} {
		if strings.Contains(src, bad) {
			t.Errorf("app.js still contains %q", bad)
		}
	}
	css, err := fs.ReadFile(staticFS, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(css), "pn-thumb") {
		t.Error("app.css still contains pn-thumb")
	}
}

func TestPopoutJS_LotSubRowUnitTrigger(t *testing.T) {
	src := readAppJS(t)
	if !strings.Contains(src, "'/popout/unit/' + node.ID") {
		t.Error("app.js buildLotSubRow: unit nodes should use /popout/unit/<id>")
	}
	if strings.Contains(src, "'/units/' : '/lots/') + node.ID + '/consumers'") {
		t.Error("app.js buildLotSubRow: still builds the removed unit consumers URL")
	}
}

func TestBreadcrumbThumbUsesPopout(t *testing.T) {
	for _, f := range []string{"templates/shared/partials.html", "templates/parts/part_edit.html"} {
		b, err := fs.ReadFile(templatesFS, f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if strings.Contains(s, "pn-thumb") || strings.Contains(s, "data-thumb=") {
			t.Errorf("%s: still uses the old .pn-thumb hover", f)
		}
		if !strings.Contains(s, `data-popout-url="/popout/part/{{.Part.ID}}/thumb"`) {
			t.Errorf("%s: breadcrumb thumbnail should use /popout/part/<id>/thumb", f)
		}
	}
}

func TestUnitAndBuildCellTriggers(t *testing.T) {
	lot := 9
	units := renderPartsList(t, "all_units.html", map[string]any{"Units": []UnitRow{
		{ID: 1, PartID: 2, SerialNumber: "SN-362", PartNumber: "ASM-1", IsActive: true},
	}})
	requireAll(t, "all_units", units, []string{`data-popout-url="/popout/unit/1"`}, []string{"/consumers"})
	builds := renderPartsList(t, "all_builds.html", map[string]any{"Builds": []inventory.BuildListRow{
		{ID: 5, PartID: 2, PartNumber: "ASM-1", Qty: decimal.NewFromInt(3), Date: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC), OutputLotID: &lot, LotNumber: "L-9"},
		{ID: 6, PartID: 2, PartNumber: "ASM-1", Qty: decimal.NewFromInt(1), Date: time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)},
	}})
	requireAll(t, "all_builds", builds, []string{
		`data-popout-url="/popout/build/5" href="/part/2/lots/9">#5<`,
		`data-popout-url="/popout/build/6" href="/part/2/build">#6<`,
	}, nil)
}
