//go:build integration

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	arxdb "arx/internal/db"
)

const scaleBaseRows, scaleExtraRows = 2, 10 // baseline is non-empty so empty-list shortcuts are not mistaken for growth

// scaleFixture is a page with a growable set of child rows.
type scaleFixture struct {
	target   string
	children func() int        // COUNT(*) of the page's child rows
	grow     func(n int)        // insert n more child rows, each marker-unique
	marker   func(k int) string // text the page shows for child k (1-based); nil when the page repeats a value
}

type scaleCase struct {
	name, pattern string
	handler       func(*Handler) http.HandlerFunc
	tables        []string // tables whose COUNT(*) must be identical before setup and after cleanup
	// setup builds the parent rows and registers its cleanup with t.Cleanup before its first insert.
	setup func(t *testing.T, h *Handler) scaleFixture
}

// scaleExec / scaleScan run fixture SQL, failing the test on error.
func scaleExec(t *testing.T, h *Handler, q string, args ...any) {
	t.Helper()
	if _, err := h.execContext(context.Background(), q, args...); err != nil {
		t.Fatalf("scaling fixture: %v\n%s", err, q)
	}
}

func scaleScan(t *testing.T, h *Handler, q string, args ...any) int {
	t.Helper()
	var id int
	if err := h.queryRowContext(context.Background(), q, args...).Scan(&id); err != nil {
		t.Fatalf("scaling fixture: %v\n%s", err, q)
	}
	return id
}

func scaleCount(t *testing.T, h *Handler, q string, args ...any) func() int {
	return func() int { return scaleScan(t, h, q, args...) }
}

func scaleCases() []scaleCase {
	return []scaleCase{
		{
			name: "part BOM", pattern: "/part/{id}/bom", handler: func(h *Handler) http.HandlerFunc { return h.PartBOM },
			tables: []string{"part", "bom", "price"},
			setup: func(t *testing.T, h *Handler) scaleFixture {
				g := newBOMGraph(t, h)
				p := g.asm("P")
				k := 0
				return scaleFixture{
					target:   fmt.Sprintf("/part/%d/bom", p),
					children: scaleCount(t, h, `SELECT COUNT(*)::int FROM bom WHERE parent_part_id=$1`, p),
					grow: func(n int) {
						for range n {
							k++
							g.line(p, g.leaf("L"+strconv.Itoa(k)), k, 1)
						}
					},
					marker: func(k int) string { return g.base + "-L" + strconv.Itoa(k) },
				}
			},
		},
		{
			name: "part orders", pattern: "/part/{id}/orders", handler: func(h *Handler) http.HandlerFunc { return h.PartOrders },
			tables: []string{"part", "purchase_order", "po_line"},
			setup: func(t *testing.T, h *Handler) scaleFixture {
				p, po, _ := scalePOFixture(t, h)
				k := 0
				return scaleFixture{
					target:   fmt.Sprintf("/part/%d/orders", p),
					children: scaleCount(t, h, `SELECT COUNT(*)::int FROM po_line WHERE po_id=$1`, po),
					grow:     func(n int) { scaleAddPOLines(t, h, po, p, &k, n) },
				}
			},
		},
		{
			name: "part lots", pattern: "/part/{id}/lots", handler: func(h *Handler) http.HandlerFunc { return h.PartLots },
			tables: []string{"part", "lot"},
			setup: func(t *testing.T, h *Handler) scaleFixture {
				base := smokeUniq("ITEST-SCALE")
				t.Cleanup(func() {
					smokeExec(context.Background(), h, `DELETE FROM lot WHERE part_id IN (SELECT id FROM part WHERE part_number=$1)`, base)
					smokeExec(context.Background(), h, `DELETE FROM part WHERE part_number=$1`, base)
				})
				p := scaleScan(t, h, `INSERT INTO part (part_number, category, tracking_mode) VALUES ($1,'RAW','lot') RETURNING id`, base)
				k := 0
				return scaleFixture{
					target:   fmt.Sprintf("/part/%d/lots", p),
					children: scaleCount(t, h, `SELECT COUNT(*)::int FROM lot WHERE part_id=$1`, p),
					grow: func(n int) {
						for range n {
							k++
							scaleExec(t, h, `INSERT INTO lot (part_id, lot_number, lot_description, source) VALUES ($1,$2,'itest','adjust')`,
								p, base+"-"+strconv.Itoa(k))
						}
					},
					marker: func(k int) string { return base + "-" + strconv.Itoa(k) },
				}
			},
		},
		{
			name: "part units", pattern: "/part/{id}/units", handler: func(h *Handler) http.HandlerFunc { return h.PartUnits },
			tables: []string{"part", "unit"},
			setup:  scaleUnitsFixture,
		},
		{
			name: "PO detail", pattern: "/po/{id}", handler: func(h *Handler) http.HandlerFunc { return h.PODetail },
			tables: []string{"part", "purchase_order", "po_line"},
			setup: func(t *testing.T, h *Handler) scaleFixture {
				p, po, number := scalePOFixture(t, h)
				k := 0
				return scaleFixture{
					target:   "/po/" + number,
					children: scaleCount(t, h, `SELECT COUNT(*)::int FROM po_line WHERE po_id=$1`, po),
					grow:     func(n int) { scaleAddPOLines(t, h, po, p, &k, n) },
					marker:   func(k int) string { return "itest line " + strconv.Itoa(k) },
				}
			},
		},
		{
			name: "supplier parts", pattern: "/supplier/{id}/parts", handler: func(h *Handler) http.HandlerFunc { return h.SupplierParts },
			tables: []string{"part", "company", "supplier_part"},
			setup: func(t *testing.T, h *Handler) scaleFixture {
				base := smokeUniq("ITEST-SCALE")
				t.Cleanup(func() {
					ctx := context.Background()
					smokeExec(ctx, h, `DELETE FROM supplier_part WHERE supplier_id IN (SELECT id FROM company WHERE name=$1)`, base)
					smokeExec(ctx, h, `DELETE FROM company WHERE name=$1`, base)
					smokeExec(ctx, h, `DELETE FROM part WHERE part_number=$1`, base)
				})
				sup := scaleScan(t, h, `INSERT INTO company (name, is_supplier, is_active) VALUES ($1,TRUE,TRUE) RETURNING id`, base)
				p := scaleScan(t, h, `INSERT INTO part (part_number, category) VALUES ($1,'BUY') RETURNING id`, base)
				k := 0
				return scaleFixture{
					target:   fmt.Sprintf("/supplier/%d/parts", sup),
					children: scaleCount(t, h, `SELECT COUNT(*)::int FROM supplier_part WHERE supplier_id=$1`, sup),
					grow: func(n int) {
						for range n {
							k++
							scaleExec(t, h, `INSERT INTO supplier_part (supplier_id, part_id, supplier_pn, preference) VALUES ($1,$2,$3,1)`,
								sup, p, base+"-"+strconv.Itoa(k))
						}
					},
					marker: func(k int) string { return base + "-" + strconv.Itoa(k) },
				}
			},
		},
		{
			name: "record detail", pattern: "/records/{id}", handler: func(h *Handler) http.HandlerFunc { return h.RecordDetail },
			tables: []string{"part", "form", "form_row", "form_row_history", "form_record", "result"},
			setup: func(t *testing.T, h *Handler) scaleFixture {
				ctx := context.Background()
				partID, formID, testID := seedThrowawayForm(t, h, ctx, "ITEST-SCALE")
				t.Cleanup(func() {
					smokeExec(ctx, h, `DELETE FROM result WHERE form_record_id IN (SELECT id FROM form_record WHERE form_id=$1)`, formID)
					smokeExec(ctx, h, `DELETE FROM form_record WHERE form_id=$1`, formID)
					smokeExec(ctx, h, `DELETE FROM form_row_history WHERE form_row_id IN (SELECT id FROM form_row WHERE form_id=$1)`, formID)
					smokeExec(ctx, h, `DELETE FROM form_row WHERE form_id=$1`, formID)
					smokeExec(ctx, h, `DELETE FROM form WHERE id=$1`, formID)
					smokeExec(ctx, h, `DELETE FROM part WHERE id=$1`, partID)
				})
				ids := []string{strconv.Itoa(testID)}
				rec := scaleScan(t, h,
					`INSERT INTO form_record (form_id, record_date, serial_number, is_active, is_locked, test_order, form_revision) VALUES ($1,'2026-07-01',$2,TRUE,FALSE,$3,1) RETURNING id`,
					formID, smokeUniq("ITEST-SCALE"), ids[0])
				scaleExec(t, h, `INSERT INTO result (form_record_id, form_row_id, result, pass_fail, parameter, type) VALUES ($1,$2,'1',TRUE,'Step',0)`, rec, testID)
				k := 0
				return scaleFixture{
					target:   fmt.Sprintf("/records/%d", rec),
					children: scaleCount(t, h, `SELECT COUNT(*)::int FROM result WHERE form_record_id=$1`, rec),
					grow: func(n int) {
						for range n {
							k++
							param := fmt.Sprintf("Step ITEST-%d", k)
							row := scaleScan(t, h, `INSERT INTO form_row (form_id, type, parameter, archived) VALUES ($1,0,$2,FALSE) RETURNING id`, formID, param)
							scaleExec(t, h, `INSERT INTO result (form_record_id, form_row_id, result, pass_fail, parameter, type) VALUES ($1,$2,'1',TRUE,$3,0)`, rec, row, param)
							ids = append(ids, strconv.Itoa(row))
						}
						order := strings.Join(ids, ",")
						scaleExec(t, h, `UPDATE form_record SET test_order=$1 WHERE id=$2`, order, rec)
						scaleExec(t, h, `UPDATE form SET test_order=$1 WHERE id=$2`, order, formID)
					},
					marker: func(k int) string { return fmt.Sprintf("Step ITEST-%d", k) },
				}
			},
		},
	}
}

// scalePOFixture creates a BUY part and a throwaway PO (made open, so the part's Orders tab
// lists its lines), registering cleanup first.
func scalePOFixture(t *testing.T, h *Handler) (partID, poID int, number string) {
	t.Helper()
	ctx := context.Background()
	base := smokeUniq("ITEST-SCALE")
	t.Cleanup(func() { smokeExec(ctx, h, `DELETE FROM part WHERE part_number=$1`, base) })
	poID, number, poCleanup := seedThrowawayPO(t, h, ctx)
	t.Cleanup(poCleanup)
	partID = scaleScan(t, h, `INSERT INTO part (part_number, category) VALUES ($1,'BUY') RETURNING id`, base)
	scaleExec(t, h, `UPDATE purchase_order SET status='open' WHERE id=$1`, poID)
	return partID, poID, number
}

func scaleAddPOLines(t *testing.T, h *Handler, po, part int, k *int, n int) {
	t.Helper()
	for range n {
		*k++
		scaleExec(t, h,
			`INSERT INTO po_line (po_id, line_number, part_id, description, qty, unit_cost) VALUES ($1,$2,$3,$4,1,1)`,
			po, *k, part, "itest line "+strconv.Itoa(*k))
	}
}

func scaleUnitsFixture(t *testing.T, h *Handler) scaleFixture {
	base := smokeUniq("ITEST-SCALE")
	t.Cleanup(func() {
		smokeExec(context.Background(), h, `DELETE FROM unit WHERE part_id IN (SELECT id FROM part WHERE part_number=$1)`, base)
		smokeExec(context.Background(), h, `DELETE FROM part WHERE part_number=$1`, base)
	})
	p := scaleScan(t, h, `INSERT INTO part (part_number, category, tracking_mode) VALUES ($1,'ASM','serial') RETURNING id`, base)
	k := 0
	return scaleFixture{
		target:   fmt.Sprintf("/part/%d/units", p),
		children: scaleCount(t, h, `SELECT COUNT(*)::int FROM unit WHERE part_id=$1`, p),
		grow: func(n int) {
			for range n {
				k++
				scaleExec(t, h, `INSERT INTO unit (part_id, serial_number) VALUES ($1,$2)`, p, base+"-"+strconv.Itoa(k))
			}
		},
		marker: func(k int) string { return base + "-" + strconv.Itoa(k) },
	}
}

// scaleTableCounts returns COUNT(*) for each table.
func scaleTableCounts(t *testing.T, h *Handler, tables []string) map[string]int {
	m := make(map[string]int, len(tables))
	for _, tb := range tables {
		m[tb] = countRows(t, h, context.Background(), tb)
	}
	return m
}

// scaleGet requests the fixture's page as the seeded admin and returns the body and stats.
func scaleGet(t *testing.T, h *Handler, pattern string, fn http.HandlerFunc, target string) (string, *sqlStats) {
	t.Helper()
	admin, err := h.userByID(context.Background(), seedAdminUserID)
	if err != nil {
		t.Fatalf("load seeded admin: %v", err)
	}
	rec, st := getThroughMux(pattern, fn, target, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d, want 200. body: %s", target, rec.Code, rec.Body.String())
	}
	return rec.Body.String(), st
}

// measureScaling builds the fixture, grows it to scaleBaseRows, makes one unmeasured warm-up
// request, measures, grows by scaleExtraRows, measures again, and returns the two
// critical-path counts. It fails t when the child count did not rise by exactly scaleExtraRows,
// the page body did not get longer, or a marker is missing.
func measureScaling(t *testing.T, h *Handler, c scaleCase) (before, after int) {
	t.Helper()
	fn := c.handler(h)
	fx := c.setup(t, h)
	fx.grow(scaleBaseRows)
	scaleGet(t, h, c.pattern, fn, fx.target) // warm-up: statements prepared, connections open
	baseBody, st := scaleGet(t, h, c.pattern, fn, fx.target)
	before = st.count
	baseChildren := fx.children()

	fx.grow(scaleExtraRows)
	if got := fx.children(); got != baseChildren+scaleExtraRows {
		t.Fatalf("%s: %d child rows after growing by %d from %d", c.name, got, scaleExtraRows, baseChildren)
	}
	body, st := scaleGet(t, h, c.pattern, fn, fx.target)
	after = st.count
	if len(body) <= len(baseBody) {
		t.Errorf("%s: page body did not grow after adding %d child rows (%d -> %d bytes): the page may not show them",
			c.name, scaleExtraRows, len(baseBody), len(body))
	}
	if fx.marker != nil {
		if m := fx.marker(scaleBaseRows + scaleExtraRows); !strings.Contains(body, m) {
			t.Errorf("%s: page does not show the newest child %q", c.name, m)
		}
	}
	return before, after
}

func TestIntegration_RoundTripsIndependentOfRowCount(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	for _, c := range scaleCases() {
		t.Run(c.name, func(t *testing.T) {
			tablesBefore := scaleTableCounts(t, h, c.tables)
			var before, after int
			t.Run("measure", func(t *testing.T) { before, after = measureScaling(t, h, c) })
			if before <= 0 {
				t.Fatalf("%s: %d round trips before adding child rows, want > 0", c.name, before)
			}
			if err := checkRoundTripGrowth(c.name, before, after); err != nil {
				t.Error(err)
			}
			// Subtest cleanups have run by now: nothing may be left behind.
			for tb, n := range scaleTableCounts(t, h, c.tables) {
				if n != tablesBefore[tb] {
					t.Errorf("%s: table %s has %d rows, had %d before the fixture: cleanup left rows behind", c.name, tb, n, tablesBefore[tb])
				}
			}
		})
	}
}

// TestIntegration_Scaling_DetectsNPlusOne proves the grow-measure-compare loop sees real
// growth: a handler that issues one query per child row must make the count rise.
func TestIntegration_Scaling_DetectsNPlusOne(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	c := scaleCase{
		name: "N+1 probe", pattern: "/part/{id}/units",
		tables: []string{"part", "unit"},
		setup:  scaleUnitsFixture,
		handler: func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				var ids []int
				rows, err := h.queryContext(r.Context(), `SELECT id FROM unit WHERE part_id=$1`, r.PathValue("id"))
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				for rows.Next() {
					var id int
					_ = rows.Scan(&id)
					ids = append(ids, id)
				}
				rows.Close()
				for _, id := range ids { // the N+1 shape
					var serial string
					_ = h.queryRowContext(r.Context(), `SELECT serial_number FROM unit WHERE id=$1`, id).Scan(&serial)
					fmt.Fprintln(w, serial)
				}
			}
		},
	}
	before, after := measureScaling(t, h, c)
	if after <= before {
		t.Errorf("N+1 probe: %d round trips before, %d after; want growth", before, after)
	}
	if checkRoundTripGrowth(c.name, before, after) == nil {
		t.Error("checkRoundTripGrowth accepted a count that grew with row count")
	}
}

// TestIntegration_ScalingFixtures_PagesRenderChildRows (characterization): each fixture's page
// shows its child rows, so the scaling test is not vacuous (rows inserted but never rendered).
func TestIntegration_ScalingFixtures_PagesRenderChildRows(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	for _, c := range scaleCases() {
		t.Run(c.name, func(t *testing.T) {
			fx := c.setup(t, h)
			fx.grow(scaleBaseRows)
			if got := fx.children(); got != scaleBaseRows && c.name != "record detail" {
				t.Fatalf("children = %d after growing to %d", got, scaleBaseRows)
			}
			body, _ := scaleGet(t, h, c.pattern, c.handler(h), fx.target)
			if fx.marker != nil {
				if m := fx.marker(scaleBaseRows); !strings.Contains(body, m) {
					t.Errorf("page does not show child marker %q", m)
				}
			}
			fx.grow(scaleExtraRows)
			longer, _ := scaleGet(t, h, c.pattern, c.handler(h), fx.target)
			if len(longer) <= len(body) {
				t.Errorf("page body did not grow after adding %d child rows (%d -> %d bytes)", scaleExtraRows, len(body), len(longer))
			}
		})
	}
}

// TestIntegration_HiddenEvents_FreshPool checks the real pgx behavior behind sqlStats' hidden
// counts on a pool built by arxdb.Connect: the first use of a connection pings and prepares, a
// repeat does neither, and a statement on a second connection opens one.
func TestIntegration_HiddenEvents_FreshPool(t *testing.T) {
	_, cleanup := liveHandler(t) // vets ARX_TEST_DSN (ArxDev only, sentinel row)
	defer cleanup()
	dsn := os.Getenv("ARX_TEST_DSN")
	if dsn == "" {
		t.Skip("needs an explicit ARX_TEST_DSN")
	}
	db, err := arxdb.Connect(dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	run := func(ctx context.Context, q string) {
		t.Helper()
		var one int
		err := timeQuery(ctx, q, func() *sql.Row { return db.QueryRowContext(ctx, q) }).Scan(&one)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	statsCtx := func() (context.Context, *sqlStats) {
		st := &sqlStats{}
		return context.WithValue(context.Background(), ctxSQLStatsKey, st), st
	}
	type hidden struct{ opens, prepares, pings, count int }
	got := func(st *sqlStats) hidden { return hidden{st.opens, st.prepares, st.pings, st.count} }

	q1 := fmt.Sprintf("SELECT 1 /* %s */", smokeUniq("hidden"))
	ctx, st := statsCtx()
	run(ctx, q1)
	if want := (hidden{0, 1, 1, 1}); got(st) != want {
		t.Errorf("first query on a fresh pool: %+v, want %+v (opens, prepares, pings, count)", got(st), want)
	}
	ctx, st = statsCtx()
	run(ctx, q1)
	if want := (hidden{0, 0, 0, 1}); got(st) != want {
		t.Errorf("same query again: %+v, want %+v", got(st), want)
	}

	held, err := db.Conn(context.Background()) // takes the only idle connection
	if err != nil {
		t.Fatalf("hold connection: %v", err)
	}
	defer held.Close()
	ctx, st = statsCtx()
	run(ctx, fmt.Sprintf("SELECT 1 /* %s */", smokeUniq("hidden")))
	if want := (hidden{1, 1, 0, 1}); got(st) != want {
		t.Errorf("query needing a second connection: %+v, want %+v", got(st), want)
	}
}
