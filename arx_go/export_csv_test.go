package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
	"testing"
)

// exportCSVDriverJS stubs the browser globals exportCSV() touches, runs it, and prints the
// captured download as JSON.
const exportCSVDriverJS = `
const input = JSON.parse(require('fs').readFileSync(0, 'utf8'));
let allRows = input.rows;
const filters = (input.filters || []).map(f => makeTextFilter(f));
const getFilterColumns = () => filters;
let download = null, content = null;
const document = {
    querySelector: () => ({ dataset: { rowsUrl: input.url } }),
    getElementById: () => null,
    createElement: () => ({ click() { download = this.download; } }),
};
class Blob { constructor(p) { this.p = p; } }
URL.createObjectURL = b => { content = b.p[0]; return 'blob:x'; };
URL.revokeObjectURL = () => {};
exportCSV();
process.stdout.write(JSON.stringify({ filename: download, csv: content }));
`

type exportRow struct {
	Fields map[string]any
	Text   []string
}

// runExportCSV runs app.js exportCSV() for rowsURL over rows; filters holds one raw filter
// string per column slot ("" = no filter). Returns the downloaded filename ("" if none) and CSV.
func runExportCSV(t *testing.T, rowsURL string, rows []exportRow, filters []string) (string, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	b, err := fs.ReadFile(staticFS, "static/shared/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	js := ""
	for _, fn := range []string{"compileGlob", "termMatcher", "parseFilterTerms", "makeTextFilter", "matchesRow", "exportCSV"} {
		js += jsFunc(t, src, fn)
	}
	js += exportCSVDriverJS

	jsRows := make([]map[string]any, len(rows))
	for i, r := range rows {
		text := make([]string, len(r.Text))
		for j, s := range r.Text {
			text[j] = strings.ToLower(s) // the page lowercases cell text when building _text
		}
		m := map[string]any{"_text": text}
		for k, v := range r.Fields {
			m[k] = v
		}
		jsRows[i] = m
	}
	in, _ := json.Marshal(map[string]any{"url": rowsURL, "rows": jsRows, "filters": filters})
	cmd := exec.Command(node, "-e", js)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var out struct {
		Filename *string `json:"filename"`
		CSV      *string `json:"csv"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("bad node output %q: %v", stdout.String(), err)
	}
	if out.Filename == nil || out.CSV == nil {
		return "", ""
	}
	return *out.Filename, *out.CSV
}

func csvLines(csv string) []string { return strings.Split(csv, "\r\n") }

func lotRow(lot, pn, notes string, active bool) exportRow {
	return exportRow{
		Fields: map[string]any{"id": 1, "partId": 9, "lot": lot, "vendorLot": "V-" + lot, "lotDesc": "d", "pn": pn,
			"partDesc": "pd", "notes": notes, "created": "2026-05-01", "active": active},
		Text: []string{lot, "V-" + lot, "d", pn, "pd", notes, "2026-05-01", ""},
	}
}

func TestExportCSV_PartsCharacterization(t *testing.T) {
	row := func(pn, desc string) exportRow {
		return exportRow{
			Fields: map[string]any{"pn": pn, "rev": "A", "description": desc, "detail": "", "reqBy": "x", "date": "2026-01-01",
				"cat": "c", "modified": "", "attach": 0, "poLines": 0, "active": true},
			Text: []string{pn},
		}
	}
	name, csv := runExportCSV(t, "/api/parts/rows", []exportRow{row("A-1", `say "hi", ok`), row("B-2", "x")}, []string{"A-"})
	if name != "parts.csv" {
		t.Fatalf("filename %q, want parts.csv", name)
	}
	lines := csvLines(csv)
	if lines[0] != "Part Number,Revision,Description,Detail,Requested By,Date,Category,Modified,Attachments,PO Lines,Active" {
		t.Errorf("header %q", lines[0])
	}
	if len(lines) != 2 || !strings.HasPrefix(lines[1], `A-1,A,"say ""hi"", ok",`) {
		t.Errorf("rows %q", lines[1:])
	}
}

func TestExportCSV_UnknownURLNoop(t *testing.T) {
	if name, _ := runExportCSV(t, "/api/nope/rows", []exportRow{lotRow("L1", "P", "", true)}, nil); name != "" {
		t.Errorf("unknown URL downloaded %q", name)
	}
}

func TestExportCSV_Lots(t *testing.T) {
	rows := []exportRow{lotRow("L1", "P-1", "plain", true), lotRow("L2", "P-2", "a, b", false), lotRow("L3", "P-1", "", true)}
	name, csv := runExportCSV(t, "/api/lots/rows", rows, nil)
	if name != "lots.csv" {
		t.Fatalf("filename %q, want lots.csv", name)
	}
	lines := csvLines(csv)
	if lines[0] != "Lot Number,Vendor Lot,Lot Description,Part Number,Part Description,Notes,Created,Status" {
		t.Errorf("header %q", lines[0])
	}
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4: %q", len(lines), lines)
	}
	if lines[1] != "L1,V-L1,d,P-1,pd,plain,2026-05-01,Active" {
		t.Errorf("row 1 %q", lines[1])
	}
	if lines[2] != `L2,V-L2,d,P-2,pd,"a, b",2026-05-01,Inactive` {
		t.Errorf("row 2 %q", lines[2])
	}
}

func TestExportCSV_LotsFiltered(t *testing.T) {
	rows := []exportRow{lotRow("L1", "P-1", "", true), lotRow("L2", "P-2", "", true), lotRow("L3", "P-1", "", true)}
	_, csv := runExportCSV(t, "/api/lots/rows", rows, []string{"", "", "", "P-1"})
	lines := csvLines(csv)
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "L1,") || !strings.HasPrefix(lines[2], "L3,") {
		t.Errorf("filtered rows %q", lines)
	}
}

func TestExportCSV_LotsAllPages(t *testing.T) {
	var rows []exportRow
	for i := 0; i < 250; i++ {
		rows = append(rows, lotRow(fmt.Sprintf("L%d", i), "P", "", true))
	}
	_, csv := runExportCSV(t, "/api/lots/rows", rows, nil)
	if n := len(csvLines(csv)); n != 251 {
		t.Errorf("got %d lines, want 251 (header + every row, not one page)", n)
	}
}

func TestExportCSV_Units(t *testing.T) {
	unit := func(sn, lot string, active bool) exportRow {
		return exportRow{
			Fields: map[string]any{"id": 1, "partId": 9, "lotId": 0, "sn": sn, "pn": "P", "partDesc": "pd", "lot": lot, "created": "2026-05-02", "active": active},
			Text:   []string{sn, "P", "pd", lot, "2026-05-02", ""},
		}
	}
	name, csv := runExportCSV(t, "/api/units/rows", []exportRow{unit("S1", "", true), unit("S2", "L1", false)}, nil)
	if name != "units.csv" {
		t.Fatalf("filename %q, want units.csv", name)
	}
	lines := csvLines(csv)
	if lines[0] != "Serial #,Part Number,Description,Lot,Created,Status" ||
		lines[1] != "S1,P,pd,,2026-05-02,Active" || lines[2] != "S2,P,pd,L1,2026-05-02,Scrapped" {
		t.Errorf("units csv %q", lines)
	}
}

func TestExportCSV_Builds(t *testing.T) {
	build := func(id int, lot string) exportRow {
		return exportRow{
			Fields: map[string]any{"id": id, "partId": 9, "pn": "P", "partDesc": "pd", "qty": "2", "date": "2026-05-03", "outLotId": 0, "lot": lot, "note": "n"},
			Text:   []string{fmt.Sprint(id), "P", "pd", "2", "2026-05-03", lot, "n"},
		}
	}
	name, csv := runExportCSV(t, "/api/builds/rows", []exportRow{build(8201, ""), build(8202, "L1")}, nil)
	if name != "builds.csv" {
		t.Fatalf("filename %q, want builds.csv", name)
	}
	lines := csvLines(csv)
	if lines[0] != "Build,Part Number,Description,Qty,Build Date,Output Lot,Note" ||
		lines[1] != "8201,P,pd,2,2026-05-03,,n" || lines[2] != "8202,P,pd,2,2026-05-03,L1,n" {
		t.Errorf("builds csv %q", lines)
	}
}

func TestListPagesHaveExportButton(t *testing.T) {
	for page, link := range map[string]string{
		"all_lots.html":   `>All Units</a>`,
		"all_units.html":  `>All Lots</a>`,
		"all_builds.html": `>All Lots</a>`,
	} {
		out := renderPartsList(t, page, map[string]any{})
		btn := strings.Index(out, `onclick="exportCSV()"`)
		if btn < 0 || !strings.Contains(out, "Export CSV") {
			t.Errorf("%s: no Export CSV button", page)
			continue
		}
		if l := strings.Index(out, link); l < btn {
			t.Errorf("%s: Export CSV button should come before the %s link", page, link)
		}
	}
}
