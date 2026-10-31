//go:build integration

package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Part detail tabs that run the part lookup concurrently with their own queries (#431).

type partTab struct {
	name     string
	fn       func(*Handler) http.HandlerFunc
	tab      string // detailTab passed to partPageBase / partTabPage
	id       int    // seeded part the tab renders for (same parts as the RouteRoundTrips rows)
	fault    string // sqlc query whose failure surfaces as errPrefix
	errPrefix string
	gated    bool   // tabVisible can hide this tab by category
	count    int    // critical-path round trips after #431
	queries  int    // total queries (unchanged by #431)
}

var partTabs = []partTab{
	{"bom", func(h *Handler) http.HandlerFunc { return h.PartBOM }, "bom", 3005, "ListBOMLines", "Error retrieving BOM", true, 1, 2},
	{"orders", func(h *Handler) http.HandlerFunc { return h.PartOrders }, "orders", 3005, "ListPartOrders", "Error retrieving orders", true, 1, 2},
	{"where-used", func(h *Handler) http.HandlerFunc { return h.PartWhereUsed }, "where-used", 3002, "ListWhereUsed", "Error retrieving where-used", false, 1, 2},
	{"records", func(h *Handler) http.HandlerFunc { return h.PartRecords }, "records", 3005, "ListRecordTypes", "Error retrieving record types", false, 1, 2},
	{"transactions", func(h *Handler) http.HandlerFunc { return h.PartTransactions }, "transactions", 3005, "ListPartLedger", "Error retrieving transactions", true, 1, 2},
	{"units", func(h *Handler) http.HandlerFunc { return h.PartUnits }, "units", 3005, "ListPartUnits", "Error retrieving units", true, 1, 2},
	{"lots", func(h *Handler) http.HandlerFunc { return h.PartLots }, "lots", 3012, "ListLots", "Error retrieving lots", true, 2, 3},
	{"pricing", func(h *Handler) http.HandlerFunc { return h.PartPricing }, "pricing", 3002, "ListPartPrices", "Error retrieving pricing", true, 2, 3},
	{"mfg-parts", func(h *Handler) http.HandlerFunc { return h.PartMfgParts }, "mfg-parts", 3002, "ListMfgParts", "Error retrieving manufacturer parts", true, 3, 4},
	{"suppliers", func(h *Handler) http.HandlerFunc { return h.PartSourcing }, "suppliers", 3002, "ListSupplierParts", "Error retrieving supplier links", true, 1, 5},
	{"attachments", func(h *Handler) http.HandlerFunc { return h.PartAttachments }, "attachments", 3005, "ListPartAttachments", "Error retrieving attachments", false, 4, 5},
	{"build", func(h *Handler) http.HandlerFunc { return h.PartBuild }, "build", 3005, "ListBuildHistory", "Error retrieving builds", true, 3, 4},
}

func (p partTab) get(ctx context.Context, h *Handler, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/part/"+id+"/"+p.tab, nil).WithContext(ctx)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	p.fn(h)(rec, req)
	return rec
}

// gateFailingPart returns a seeded part whose category hides tab, or 0 when none does.
func gateFailingPart(t *testing.T, h *Handler, tab string) (id int, category string) {
	t.Helper()
	ctx := context.Background()
	for id := 3001; id <= 3013; id++ {
		p, err := h.fetchPartBasic(ctx, strconv.Itoa(id))
		if err != nil {
			continue
		}
		h.applyCategoryTabs(ctx, &p)
		if !tabVisible(p, tab) {
			return id, p.Category
		}
	}
	return 0, ""
}

// ── Characterization: pass before and after the change ───────────────────────

func TestIntegration_PartTabs_NotFound(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	for _, tab := range partTabs {
		rec := tab.get(context.Background(), h, "999999999")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Part not found") {
			t.Errorf("%s: status %d, body missing %q", tab.name, rec.Code, "Part not found")
		}
	}
}

func TestIntegration_PartTabs_InvalidID(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	for _, tab := range partTabs {
		rec := tab.get(context.Background(), h, "abc")
		if !strings.Contains(rec.Body.String(), "Error retrieving part:") {
			t.Errorf("%s: body missing %q", tab.name, "Error retrieving part:")
		}
	}
}

func TestIntegration_PartTabs_NotApplicable(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	for _, tab := range partTabs {
		if !tab.gated {
			continue
		}
		id, category := gateFailingPart(t, h, tab.tab)
		if id == 0 {
			t.Logf("%s: no seeded part hides this tab; skipped", tab.name)
			continue
		}
		rec := tab.get(context.Background(), h, strconv.Itoa(id))
		want := "The " + tab.tab + " section does not apply to " + category + " parts."
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s (part %d): body missing %q", tab.name, id, want)
		}
	}
}

func TestIntegration_PartTabs_TabErrorSurfaces(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	for _, tab := range partTabs {
		restore := withFault(t, h, tab.fault)
		rec := tab.get(context.Background(), h, strconv.Itoa(tab.id))
		restore()
		body := rec.Body.String()
		if !strings.Contains(body, tab.errPrefix) || !strings.Contains(body, "injected fault") {
			t.Errorf("%s: body missing %q with the injected fault", tab.name, tab.errPrefix)
		}
	}
}

func TestIntegration_PartTabs_TabErrorIgnoredWhenGateFails(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	for _, tab := range partTabs {
		restore := withFault(t, h, tab.fault)
		notFound := tab.get(context.Background(), h, "999999999").Body.String()
		var gated string
		if id, _ := gateFailingPart(t, h, tab.tab); tab.gated && id != 0 {
			gated = tab.get(context.Background(), h, strconv.Itoa(id)).Body.String()
		}
		restore()
		if !strings.Contains(notFound, "Part not found") || strings.Contains(notFound, "injected fault") {
			t.Errorf("%s: not-found page must show %q and hide the tab fault", tab.name, "Part not found")
		}
		if tab.gated && gated != "" && (!strings.Contains(gated, "does not apply to") || strings.Contains(gated, "injected fault")) {
			t.Errorf("%s: gate-failing page must show the gate message and hide the tab fault", tab.name)
		}
	}
}

// A failed part lookup wins over a passing tab fetch, for every tab.
func TestIntegration_PartTabs_PartLoadErrorWins(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	restore := withFault(t, h, "GetPartBasic")
	defer restore()
	for _, tab := range partTabs {
		rec := tab.get(context.Background(), h, strconv.Itoa(tab.id))
		if !strings.Contains(rec.Body.String(), "Error retrieving part: ") {
			t.Errorf("%s: body missing %q", tab.name, "Error retrieving part: ")
		}
	}
}

func TestIntegration_PartDetail_BOMRedirect(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	for _, c := range []struct {
		path string
		code int
	}{{"/part/3005", http.StatusFound}, {"/part/3005/summary", http.StatusOK}} {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		req.SetPathValue("id", "3005")
		rec := httptest.NewRecorder()
		h.PartDetail(rec, req)
		if rec.Code != c.code {
			t.Errorf("%s: status %d, want %d", c.path, rec.Code, c.code)
		}
		if c.code == http.StatusFound && rec.Header().Get("Location") != "/part/3005/bom" {
			t.Errorf("%s: Location %q, want /part/3005/bom", c.path, rec.Header().Get("Location"))
		}
	}
}

// ── Red: fail before the change, pass after ──────────────────────────────────

func TestIntegration_PartTabs_RoundTrips(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	for _, tab := range partTabs {
		ctx, st := withStats(context.Background())
		rec := tab.get(ctx, h, strconv.Itoa(tab.id))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", tab.name, rec.Code)
		}
		if st.count != tab.count || st.queries != tab.queries {
			t.Errorf("%s: %d round trips / %d queries, want %d / %d", tab.name, st.count, st.queries, tab.count, tab.queries)
		}
	}
}

// The tab's query is issued even when the part is missing or the tab is hidden: the accepted
// cost of overlapping it with the part lookup.
func TestIntegration_PartTabs_GateFailStillRunsTabQuery(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	orders := partTabs[1]
	ctx, st := withStats(context.Background())
	orders.get(ctx, h, "999999999")
	if st.count != 1 || st.queries != 2 {
		t.Errorf("not-found orders tab: %d round trips / %d queries, want 1 / 2", st.count, st.queries)
	}
}

func TestIntegration_PartTabs_FetchRunsConcurrently(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	restore := withSlowQueries(t, h, 100*time.Millisecond)
	defer restore()
	start := time.Now()
	partTabs[1].get(context.Background(), h, "3005") // orders: part lookup + tab query
	if elapsed := time.Since(start); elapsed >= 190*time.Millisecond {
		t.Errorf("orders tab took %s with 100 ms queries, want the two queries to overlap (< 190 ms)", elapsed)
	}
}

// slowConn delays every query by d.
type slowConn struct {
	*stdlib.Conn
	d time.Duration
}

func (c slowConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	time.Sleep(c.d)
	return c.Conn.QueryContext(ctx, query, args)
}

type slowConnector struct {
	inner driver.Connector
	d     time.Duration
}

func (c slowConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return slowConn{Conn: conn.(*stdlib.Conn), d: c.d}, nil
}

func (c slowConnector) Driver() driver.Driver { return c.inner.Driver() }

// withSlowQueries points h at a pool whose queries each take an extra d; the returned func
// restores the real pool.
func withSlowQueries(t *testing.T, h *Handler, d time.Duration) func() {
	t.Helper()
	cfg, err := pgx.ParseConfig(os.Getenv("ARX_TEST_DSN"))
	if err != nil {
		t.Fatal("parse test dsn") // never print err: the DSN carries the password
	}
	slow := sql.OpenDB(slowConnector{inner: stdlib.GetConnector(*cfg), d: d})
	orig := h.st().conn
	h.update(func(s *runtimeState) { s.conn = &dbConn{db: slow} })
	return func() {
		h.update(func(s *runtimeState) { s.conn = orig })
		slow.Close()
	}
}
