//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"arx/arx_go/models"
)

// bomFixture is an ASM parent P with four BOM lines (inserted out of line
// order): L1 a priced BUY leaf, L2 an OPS labor leaf, S a sub-assembly with a
// stored rollup and its own line S→L1, and L3 a leaf with every nullable column NULL.
type bomFixture struct {
	P, S, L1, L2, L3     int
	PN                   map[int]string
	LineL1, LineL2       int // bom ids of P's lines
	LineS, LineL3, SLine int // SLine is S's own line (S→L1)
}

func seedBOM(t *testing.T, h *Handler) (f bomFixture, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	base := smokeUniq("ITEST-BOM")
	f.PN = map[int]string{}
	var ids []int
	cleanup = func() {
		for _, id := range ids {
			smokeExec(ctx, h, `DELETE FROM bom WHERE parent_part_id=$1 OR component_part_id=$1`, id)
			smokeExec(ctx, h, `DELETE FROM price WHERE part_id=$1`, id)
		}
		for _, id := range ids {
			smokeExec(ctx, h, `DELETE FROM part WHERE id=$1`, id)
		}
	}
	scan := func(q string, args ...any) int {
		t.Helper()
		var id int
		if err := h.queryRowContext(ctx, q, args...).Scan(&id); err != nil {
			cleanup()
			t.Fatalf("seed: %v", err)
		}
		return id
	}
	part := func(suffix, cols, vals string, args ...any) int {
		t.Helper()
		number := base + "-" + suffix
		id := scan(fmt.Sprintf(`INSERT INTO part (part_number%s) VALUES ($1%s) RETURNING id`, cols, vals),
			append([]any{number}, args...)...)
		ids = append(ids, id)
		f.PN[id] = number
		return id
	}
	line := func(parent, comp, n int, qty float64) int {
		t.Helper()
		return scan(`INSERT INTO bom (parent_part_id, component_part_id, line_number, qty) VALUES ($1,$2,$3,$4) RETURNING id`,
			parent, comp, n, qty)
	}
	f.P = part("P", ", description, revision, category", ",'parent desc','A','ASM'")
	f.S = part("S", ", description, revision, category, last_rollup_cost, last_rollup_at",
		",'sub desc','A','ASM',3.5,'2026-02-03T04:05:06Z'")
	f.L1 = part("L1", ", description, revision, category, current_cost, default_supplier_id, attachment_count, po_line_count",
		",'l1 desc','A','BUY',0.4,1001,2,5")
	f.L2 = part("L2", ", description, revision, category, current_cost", ",'labor desc','','OPS',35")
	f.L3 = part("L3", ", description, revision, category, current_cost, attachment_count, po_line_count",
		",NULL,NULL,NULL,NULL,NULL,NULL")
	// L1's preferred price is the lowest active price from its default supplier.
	for _, p := range []struct {
		supplier      int
		priceEA, pack float64
		active        bool
	}{{1001, 0.30, 1, true}, {1001, 0.25, 10, true}, {1001, 0.10, 1, false}, {1002, 0.05, 1, true}} {
		scan(`INSERT INTO price (part_id, supplier_id, price_ea, pack_size, is_active) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			f.L1, p.supplier, p.priceEA, p.pack, p.active)
	}
	f.LineS = line(f.P, f.S, 3, 1)
	f.LineL1 = line(f.P, f.L1, 1, 2)
	f.LineL3 = line(f.P, f.L3, 4, 3)
	f.LineL2 = line(f.P, f.L2, 2, 0.5)
	f.SLine = line(f.S, f.L1, 1, 4)
	return f, cleanup
}

// bomLines reads parent's lines as "component:line:qty", by line then component.
func bomLines(t *testing.T, h *Handler, parent int) []string {
	t.Helper()
	rows, err := h.queryContext(context.Background(),
		`SELECT component_part_id, line_number, qty FROM bom WHERE parent_part_id=$1 ORDER BY line_number, component_part_id`,
		parent)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var comp, n int
		var qty float64
		if err := rows.Scan(&comp, &n, &qty); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d:%d:%g", comp, n, qty))
	}
	return out
}

// TestIntegration_FetchBOMItems_Fields pins the BOM view / children-API rows:
// line order, NULL columns reading blank/0, and each cost source.
func TestIntegration_FetchBOMItems_Fields(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedBOM(t, h)
	defer cleanup()

	items, total, err := h.fetchBOMItems(context.Background(), strconv.Itoa(f.P))
	if err != nil {
		t.Fatal(err)
	}
	want := []models.BOMItem{
		{ID: f.LineL1, LineNumber: 1, Qty: 2, ComponentPartID: f.L1, PartNumber: f.PN[f.L1], Description: "l1 desc", Revision: "A",
			Category: "BUY", CurrentCost: 0.4, AttachCount: 2, POLineCount: 5, LineUnitCost: 0.25, LineExtCost: 0.5, CostSource: "price"},
		{ID: f.LineL2, LineNumber: 2, Qty: 0.5, ComponentPartID: f.L2, PartNumber: f.PN[f.L2], Description: "labor desc", Category: "OPS",
			CurrentCost: 35, LineUnitCost: 35, LineExtCost: 17.5, CostSource: "labor"},
		{ID: f.LineS, LineNumber: 3, Qty: 1, ComponentPartID: f.S, PartNumber: f.PN[f.S], Description: "sub desc", Revision: "A",
			Category: "ASM", LastRollupCost: 3.5, ChildHasBOM: true, LineUnitCost: 3.5, LineExtCost: 3.5, CostSource: "rollup"},
		{ID: f.LineL3, LineNumber: 4, Qty: 3, ComponentPartID: f.L3, PartNumber: f.PN[f.L3], CostSource: "missing"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("fetchBOMItems:\n got %+v\nwant %+v", items, want)
	}
	if total != 21.5 {
		t.Errorf("fetchBOMItems total = %v, want 21.5", total)
	}
	if items, _, err := h.fetchBOMItems(context.Background(), strconv.Itoa(f.L3)); err != nil || items != nil {
		t.Errorf("fetchBOMItems(leaf) = %v, %v; want nil, nil", items, err)
	}
}

// TestIntegration_BOMExportCSV pins the export's filename and rows.
func TestIntegration_BOMExportCSV(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedBOM(t, h)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.BOMExportCSV(rec, withID(httptest.NewRequest(http.MethodGet, "/part/x/bom/export.csv", nil), f.P))
	if got, want := rec.Header().Get("Content-Disposition"), `attachment; filename="`+f.PN[f.P]+`-bom.csv"`; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
	want := "Line #,Qty,Part Number,Description,Revision,Category,Unit Cost,Ext Cost,Cost Source\n" +
		"1,2," + f.PN[f.L1] + ",l1 desc,A,BUY,0.25,0.50,price\n" +
		"2,0.5," + f.PN[f.L2] + ",labor desc,,OPS,35.00,17.50,labor\n" +
		"3,1," + f.PN[f.S] + ",sub desc,A,ASM,3.50,3.50,rollup\n" +
		"4,3," + f.PN[f.L3] + ",,,,0.00,0.00,missing\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("BOMExportCSV body:\n got %q\nwant %q", got, want)
	}
}

// TestIntegration_PartWhereUsed_Renders pins the parents list, by parent part number.
func TestIntegration_PartWhereUsed_Renders(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedBOM(t, h)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.PartWhereUsed(rec, withID(httptest.NewRequest(http.MethodGet, "/part/x/where-used", nil), f.L1))
	body := rec.Body.String()
	iP := strings.Index(body, fmt.Sprintf(`<a href="/part/%d" class="part-number-link">%s</a></td>
                    <td>parent desc</td>
                    <td>A</td>
                    <td>ASM</td>
                    <td style="text-align:right;">2</td>`, f.P, f.PN[f.P]))
	iS := strings.Index(body, fmt.Sprintf(`<a href="/part/%d" class="part-number-link">%s</a></td>
                    <td>sub desc</td>
                    <td>A</td>
                    <td>ASM</td>
                    <td style="text-align:right;">4</td>`, f.S, f.PN[f.S]))
	if iP < 0 || iS < 0 || iP > iS {
		t.Errorf("PartWhereUsed: parent rows missing or out of order (P=%d S=%d)", iP, iS)
	}
	if !strings.Contains(body, "Used in 2 assemblies") {
		t.Error(`PartWhereUsed: body missing "Used in 2 assemblies"`)
	}
}

// TestIntegration_PartBOMEdit_Renders pins the edit rows (bom id, line, part)
// in line order, and the stored rollup cost.
func TestIntegration_PartBOMEdit_Renders(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedBOM(t, h)
	defer cleanup()

	get := func(id int) string {
		rec := httptest.NewRecorder()
		h.PartBOMEdit(rec, withID(httptest.NewRequest(http.MethodGet, "/part/x/bom/edit", nil), id))
		return rec.Body.String()
	}
	body := get(f.P)
	last := -1
	for _, l := range []struct{ id, n, comp int }{{f.LineL1, 1, f.L1}, {f.LineL2, 2, f.L2}, {f.LineS, 3, f.S}, {f.LineL3, 4, f.L3}} {
		for _, s := range []string{
			fmt.Sprintf(`name="pl[%d][PLItem]" value="%d"`, l.id, l.n),
			fmt.Sprintf(`name="pl[%d][PLPartNumber]" value="%s"`, l.id, f.PN[l.comp]),
			fmt.Sprintf(`name="pl[%d][PLPNID]" value="%d"`, l.id, l.comp),
		} {
			if !strings.Contains(body, s) {
				t.Errorf("PartBOMEdit: body missing %q", s)
			}
		}
		i := strings.Index(body, fmt.Sprintf(`name="pl[%d][PLItem]"`, l.id))
		if i < last {
			t.Errorf("PartBOMEdit: line %d out of order", l.n)
		}
		last = i
	}
	if !strings.Contains(body, `<span class="muted">Never run</span>`) {
		t.Error("PartBOMEdit(P): want Last Rollup \"Never run\"")
	}
	if !strings.Contains(get(f.S), "<strong>Last Rollup:</strong> <span>$3.5000 &mdash; ") {
		t.Error("PartBOMEdit(S): want Last Rollup $3.5000")
	}
}

// TestIntegration_PartBOMSave pins deletes, updates (by id or part number) and
// inserts, the parent guard on another part's line, and skipped unresolvable rows.
func TestIntegration_PartBOMSave(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedBOM(t, h)
	defer cleanup()

	vals := url.Values{"delete_pl[]": {strconv.Itoa(f.LineL3), strconv.Itoa(f.SLine)}}
	set := func(prefix string, key, item, qty, pnid, number string) {
		vals.Set(fmt.Sprintf("%s[%s][PLItem]", prefix, key), item)
		vals.Set(fmt.Sprintf("%s[%s][PLQty]", prefix, key), qty)
		vals.Set(fmt.Sprintf("%s[%s][PLPNID]", prefix, key), pnid)
		vals.Set(fmt.Sprintf("%s[%s][PLPartNumber]", prefix, key), number)
	}
	set("pl", strconv.Itoa(f.LineL1), "5", "7", strconv.Itoa(f.L1), f.PN[f.L1]) // new line/qty
	set("pl", strconv.Itoa(f.LineL2), "2", "0.5", "", f.PN[f.L3])               // component by part number
	set("pl", strconv.Itoa(f.LineS), "8", "9", "", "NOPE-NO-SUCH-PART")         // unresolvable → unchanged
	set("pl", strconv.Itoa(f.LineL3), "6", "6", strconv.Itoa(f.L3), "")         // deleted → skipped
	set("new_pl", "a", "9", "1.5", "", "BUY-1001")                              // by part number
	set("new_pl", "b", "10", "2", strconv.Itoa(f.L2), "")                       // by id
	set("new_pl", "c", "11", "1", "", "NOPE-NO-SUCH-PART")                      // skipped
	set("new_pl", "d", "12", "1", "", "")                                       // blank → skipped

	rec := httptest.NewRecorder()
	h.PartBOMSave(rec, withID(postForm("/part/x/bom", vals), f.P))
	assert302(t, "PartBOMSave", rec)
	if got, want := rec.Header().Get("Location"), fmt.Sprintf("/part/%d/bom", f.P); got != want {
		t.Errorf("PartBOMSave redirect = %q, want %q", got, want)
	}
	want := []string{
		fmt.Sprintf("%d:2:0.5", f.L3), fmt.Sprintf("%d:3:1", f.S), fmt.Sprintf("%d:5:7", f.L1),
		"3002:9:1.5", fmt.Sprintf("%d:10:2", f.L2),
	}
	if got := bomLines(t, h, f.P); !reflect.DeepEqual(got, want) {
		t.Errorf("P's BOM after save:\n got %v\nwant %v", got, want)
	}
	if got, want := bomLines(t, h, f.S), []string{fmt.Sprintf("%d:1:4", f.L1)}; !reflect.DeepEqual(got, want) {
		t.Errorf("S's BOM after save (another part's line): got %v, want %v", got, want)
	}
}

var pastePreviewRow = regexp.MustCompile(`data-status="(\w+)"\s*(?:data-plid="(\d+)")?\s*(?:data-pnid="(\d+)")?\s*` +
	`data-part-number="([^"]*)"\s*data-description="([^"]*)"\s*data-qty="([^"]*)"`)

// TestIntegration_PartBOMPastePreview pins the paste diff against the current
// BOM: noop/update/new by component, and error rows for unknown parts or bad qty.
func TestIntegration_PartBOMPastePreview(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedBOM(t, h)
	defer cleanup()

	paste := "PN\tQty\n" + f.PN[f.L1] + "\t2\n" + f.PN[f.L2] + "\t1\nBUY-1001\t4\nNOPE-NO-SUCH-PART\t1\n" + f.PN[f.L3] + "\tabc\n"
	rec := httptest.NewRecorder()
	h.PartBOMPastePreview(rec, withID(postForm("/part/x/bom/preview", url.Values{"paste_text": {paste}}), f.P))
	var got [][]string
	for _, m := range pastePreviewRow.FindAllStringSubmatch(rec.Body.String(), -1) {
		got = append(got, m[1:])
	}
	want := [][]string{
		{"noop", strconv.Itoa(f.LineL1), strconv.Itoa(f.L1), f.PN[f.L1], "l1 desc", "2"},
		{"update", strconv.Itoa(f.LineL2), strconv.Itoa(f.L2), f.PN[f.L2], "labor desc", "1"},
		{"new", "", "3002", "BUY-1001", "M3x8 SHCS", "4"},
		{"error", "", "", "NOPE-NO-SUCH-PART", "", "0"},
		{"error", "", "", f.PN[f.L3], "", "0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("paste preview rows:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(rec.Body.String(), `id="bom-paste-confirm-btn" disabled`) {
		t.Error("paste preview: want Confirm Import disabled on error rows")
	}
}

// TestIntegration_PartDuplicate_CopiesBOM pins the duplicate form's BOM-copy
// promise and PartsCreate copying the source's lines.
func TestIntegration_PartDuplicate_CopiesBOM(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedBOM(t, h)
	defer cleanup()
	ctx := context.Background()

	for id, want := range map[int]bool{f.P: true, f.L3: false} {
		rec := httptest.NewRecorder()
		h.PartDuplicate(rec, withID(httptest.NewRequest(http.MethodGet, "/part/x/duplicate", nil), id))
		if got := strings.Contains(rec.Body.String(), `name="duplicate_has_bom" value="1"`); got != want {
			t.Errorf("PartDuplicate(%d): duplicate_has_bom = %v, want %v", id, got, want)
		}
	}

	rec := httptest.NewRecorder()
	h.PartsCreate(rec, postForm("/parts", url.Values{
		"part_number": {smokeUniq("ITEST-BOMDUP")}, "category": {"ASM"}, "release_status": {"U"},
		"duplicate_bom_from": {strconv.Itoa(f.P)},
	}))
	id := locID(t, rec, "/part/")
	defer smokeExec(ctx, h, "DELETE FROM part WHERE id=$1", id)
	defer smokeExec(ctx, h, "DELETE FROM bom WHERE parent_part_id=$1", id)
	if got, want := bomLines(t, h, id), bomLines(t, h, f.P); !reflect.DeepEqual(got, want) || len(got) != 4 {
		t.Errorf("duplicated BOM = %v, want %v", got, want)
	}
}
