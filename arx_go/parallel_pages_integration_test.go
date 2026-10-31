//go:build integration

package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Pages #426 fans out, pinned around the change: which failures abort, which are tolerated,
// and that fan-out can neither cancel its siblings nor starve itself.

var errInjected = errors.New("injected fault")

// faultConn fails any sqlc query named failOn. Every sqlc query starts "-- name: X :kind", and
// the trailing space stops GetPart from matching GetPartBasic.
type faultConn struct {
	*stdlib.Conn
	failOn string
}

func (c faultConn) fails(query string) bool {
	return strings.Contains(query, "-- name: "+c.failOn+" ")
}

func (c faultConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.fails(query) {
		return nil, errInjected
	}
	return c.Conn.QueryContext(ctx, query, args)
}

func (c faultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.fails(query) {
		return nil, errInjected
	}
	return c.Conn.ExecContext(ctx, query, args)
}

type faultConnector struct {
	inner  driver.Connector
	failOn string
}

func (c faultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return faultConn{Conn: conn.(*stdlib.Conn), failOn: c.failOn}, nil
}

func (c faultConnector) Driver() driver.Driver { return c.inner.Driver() }

// withFault points h at a pool whose connections fail the sqlc query named failOn; the returned
// func restores the real pool.
func withFault(t *testing.T, h *Handler, failOn string) func() {
	t.Helper()
	cfg, err := pgx.ParseConfig(os.Getenv("ARX_TEST_DSN"))
	if err != nil {
		t.Fatal("parse test dsn") // never print err: the DSN carries the password
	}
	faulty := sql.OpenDB(faultConnector{inner: stdlib.GetConnector(*cfg), failOn: failOn})
	orig := h.st().conn
	h.update(func(s *runtimeState) { s.conn = &dbConn{db: faulty} })
	return func() {
		h.update(func(s *runtimeState) { s.conn = orig })
		faulty.Close()
	}
}

// parallelPage is one fanned-out page: its handler, request and a marker of a correct render.
type parallelPage struct {
	name   string
	fn     func(*Handler) http.HandlerFunc
	target string
	values map[string]string
	marker string
}

var (
	pageDashboard = parallelPage{"reports dashboard", func(h *Handler) http.HandlerFunc { return h.ReportsDashboard }, "/reports", nil, "Recent Activity"}
	pagePart3005  = parallelPage{"part 3005 summary", func(h *Handler) http.HandlerFunc { return h.PartDetail }, "/part/3005/summary", map[string]string{"id": "3005"}, "ASM-1001"}
	pagePart3007  = parallelPage{"part 3007 summary", func(h *Handler) http.HandlerFunc { return h.PartDetail }, "/part/3007/summary", map[string]string{"id": "3007"}, "Total lots"}
	pageRecord    = parallelPage{"record detail", func(h *Handler) http.HandlerFunc { return h.RecordDetail }, "/records/7001", map[string]string{"id": "7001"}, "FORM-1001"}
	pageLotTrace  = parallelPage{"lot trace", func(h *Handler) http.HandlerFunc { return h.PartLotTrace }, "/part/3012/lots/8302", map[string]string{"id": "3012", "lotID": "8302"}, "8302"}
	pageUnitTrace = parallelPage{"unit trace", func(h *Handler) http.HandlerFunc { return h.PartUnitTrace }, "/part/3005/units/8503", map[string]string{"id": "3005", "unitID": "8503"}, "ASM-1001"}
	pagePO        = parallelPage{"PO detail", func(h *Handler) http.HandlerFunc { return h.PODetail }, "/po/5002", map[string]string{"id": "5002"}, "PO #5002"}
)

func (p parallelPage) get(ctx context.Context, h *Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, p.target, nil).WithContext(ctx)
	for k, v := range p.values {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	p.fn(h)(rec, req)
	return rec
}

func TestIntegration_ParallelPages_ErrorBehaviour(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	type row struct {
		page   parallelPage
		failOn string
		status int
		want   []string
		absent []string
	}
	var rows []row
	fatal := func(p parallelPage, failOn, msg string) {
		rows = append(rows, row{p, failOn, 200, []string{msg}, nil})
	}
	tolerated := func(p parallelPage, failOn string, want ...string) {
		rows = append(rows, row{p, failOn, 200, append([]string{p.marker}, want...), []string{"injected fault"}})
	}

	// ReportsDashboard itself only still runs the two inline counts (#435) — a fault in
	// either one fails the shell. The other nine queries moved to their own lazy-loaded
	// card endpoints, each rendering the shared error fragment (HTTP 500) on failure
	// instead of aborting the shell.
	for _, q := range []string{"CountOpenPOs", "CountPOsReceivedThisMonth"} {
		rows = append(rows, row{pageDashboard, q, 200, []string{"Error loading dashboard: injected fault"}, []string{"Recent Activity"}})
	}
	for _, c := range []struct {
		name string
		fn   func(*Handler) http.HandlerFunc
		q    string
	}{
		{"recent-activity", func(h *Handler) http.HandlerFunc { return h.ReportsDashboardRecentActivity }, "ListRecentModifiedParts"},
		{"recent-activity", func(h *Handler) http.HandlerFunc { return h.ReportsDashboardRecentActivity }, "ListRecentPOEvents"},
		{"top-failures", func(h *Handler) http.HandlerFunc { return h.ReportsDashboardTopFailures }, "ListTopFailureModes"},
		{"lowest-yield", func(h *Handler) http.HandlerFunc { return h.ReportsDashboardLowestYield }, "ListRecordFailFlags"},
		{"stale-wip", func(h *Handler) http.HandlerFunc { return h.ReportsDashboardStaleWIP }, "ListStaleWIPRecords"},
		{"pending-approval", func(h *Handler) http.HandlerFunc { return h.ReportsDashboardPendingApproval }, "ListPendingApprovalPOs"},
		{"below-reorder", func(h *Handler) http.HandlerFunc { return h.ReportsDashboardBelowReorder }, "ListBelowReorderParts"},
		{"recent-lots", func(h *Handler) http.HandlerFunc { return h.ReportsDashboardRecentLots }, "ListLots"},
		{"recent-builds", func(h *Handler) http.HandlerFunc { return h.ReportsDashboardRecentBuilds }, "ListRecentBuilds"},
	} {
		p := parallelPage{"reports dashboard card " + c.name, c.fn, "/api/reports/dashboard/" + c.name, nil, "Failed to load"}
		rows = append(rows, row{p, c.q, 500, []string{"Failed to load"}, nil})
	}

	fatal(pagePart3005, "GetPart", "Error retrieving part: injected fault")
	for _, q := range []string{"ListPartAttachments", "PreferredSupplierMinPrice", "ListRecentPartTxns", "ListRecentPartUnits",
		"CountPartBuilds", "GetPreferredSupplier", "ListPartPricePoints"} {
		tolerated(pagePart3005, q)
	}
	// A failed branch must not cancel its siblings: the units card still renders.
	tolerated(pagePart3005, "ListPartOrders", "SN-3005-001")
	tolerated(pagePart3007, "ListRecentPartLots")

	fatal(pageLotTrace, "GetPartBasic", "Error retrieving part:")
	fatal(pageLotTrace, "GetLot", "Error retrieving lot:")
	fatal(pageLotTrace, "ListTraceAncestors", "Error tracing lot ancestry:")
	fatal(pageLotTrace, "ListTraceDescendants", "Error tracing lot descendants:")
	fatal(pageLotTrace, "ListRecordTypes", "Error retrieving record types:")
	fatal(pageLotTrace, "ListLotUnits", "Error retrieving lot units:")

	fatal(pageUnitTrace, "GetUnit", "Error retrieving unit:")
	fatal(pageUnitTrace, "GetBuild", "Error retrieving build:")
	fatal(pageUnitTrace, "ListTraceAncestors", "Error tracing unit ancestry:")
	fatal(pageUnitTrace, "ListTraceDescendants", "Error tracing unit descendants:")
	fatal(pageUnitTrace, "ListRecordTypes", "Error retrieving record types:")

	fatal(pagePO, "GetPO", "Error retrieving purchase order:")
	fatal(pagePO, "ListPOLines", "Error loading PO items:")
	for _, q := range []string{"GetSupplierBulkOrder", "ListPOHistory", "ListPOReceipts"} {
		tolerated(pagePO, q)
	}

	for _, q := range []string{"GetRecord", "GetFormHeader", "ListRecordResults", "ListFormSteps", "GetPartTracking"} {
		rows = append(rows, row{pageRecord, q, 500, []string{"query error"}, nil})
	}
	for _, q := range []string{"GetRecordNeighbors", "ListRecordEvents", "ListRecordEventResults"} {
		rows = append(rows, row{pageRecord, q, 200, []string{pageRecord.marker}, []string{"query error"}})
	}

	for _, c := range rows {
		t.Run(c.page.name+"/"+c.failOn, func(t *testing.T) {
			restore := withFault(t, h, c.failOn)
			defer restore()
			rec := c.page.get(context.Background(), h)
			body := rec.Body.String()
			if rec.Code != c.status {
				t.Errorf("status %d, want %d", rec.Code, c.status)
			}
			for _, s := range c.want {
				if !strings.Contains(body, s) {
					t.Errorf("body missing %q", s)
				}
			}
			for _, s := range c.absent {
				if strings.Contains(body, s) {
					t.Errorf("body unexpectedly contains %q", s)
				}
			}
		})
	}
}

func TestIntegration_ParallelPages_NotFound(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	const missing = "999999999"
	ctx := context.Background()

	part := parallelPage{fn: pagePart3005.fn, target: "/part/" + missing + "/summary", values: map[string]string{"id": missing}}
	if body := part.get(ctx, h).Body.String(); !strings.Contains(body, "Part not found") {
		t.Errorf("part: body missing not-found message")
	}
	po := parallelPage{fn: pagePO.fn, target: "/po/" + missing, values: map[string]string{"id": missing}}
	if body := po.get(ctx, h).Body.String(); !strings.Contains(body, "Purchase order not found") {
		t.Errorf("PO: body missing not-found message")
	}
	record := parallelPage{fn: pageRecord.fn, target: "/records/" + missing, values: map[string]string{"id": missing}}
	if code := record.get(ctx, h).Code; code != http.StatusNotFound {
		t.Errorf("record: status %d, want 404", code)
	}
}

func TestIntegration_ParallelPages_CanceledRequest(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := pageDashboard.get(ctx, h)
	if rec.Code != http.StatusOK {
		t.Errorf("status %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Error loading dashboard: context canceled") {
		t.Errorf("body missing cancellation message")
	}
}

// One connection is the worst case for a capped pool: a fan-out that held a connection while
// waiting on another would hit the 30 s deadline here.
func TestIntegration_ParallelPages_OneConnection(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	h.database().SetMaxOpenConns(1)
	for _, p := range []parallelPage{pageDashboard, pagePart3005, pageRecord, pageLotTrace, pageUnitTrace, pagePO} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rec := p.get(ctx, h)
		cancel()
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), p.marker) {
			t.Errorf("%s: status %d, marker %q present=%v", p.name, rec.Code, p.marker, strings.Contains(rec.Body.String(), p.marker))
		}
	}
}

// Critical-path round trips and total queries per page; see the budgets in
// TestIntegration_RouteRoundTrips.
func TestIntegration_ParallelPages_Stats(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	for _, c := range []struct {
		page           parallelPage
		count, queries int
	}{
		{pageDashboard, 1, 2}, // #435: the other eight queries moved to lazy-loaded card endpoints
		{pagePart3005, 2, 9},
		{pageRecord, 2, 8},
		{pageLotTrace, 4, 8},
		{pageUnitTrace, 4, 7},
		{pagePO, 2, 5},
	} {
		ctx, st := withStats(context.Background())
		if rec := c.page.get(ctx, h); rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", c.page.name, rec.Code)
		}
		if st.count != c.count || st.queries != c.queries {
			t.Errorf("%s: count=%d queries=%d, want %d, %d", c.page.name, st.count, st.queries, c.count, c.queries)
		}
	}
}

// Warm connections are the point of the pool: a fan-out's connections must stay idle afterwards
// rather than being closed down to database/sql's default of 2.
func TestIntegration_PoolKeepsTwelveIdle(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	db := h.database()
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := db.ExecContext(context.Background(), `SELECT pg_sleep(0.2)`); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for db.Stats().Idle != 12 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if st := db.Stats(); st.Idle != 12 || st.MaxOpenConnections != 12 {
		t.Errorf("Idle=%d MaxOpenConnections=%d, want 12, 12", st.Idle, st.MaxOpenConnections)
	}
}

func TestIntegration_PartSummary_RenderedData(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	ctx := context.Background()

	full, _, _, cleanup := seedPartDetail(t, h)
	defer cleanup()
	fullID := fmt.Sprint(full)

	for _, c := range []struct {
		id   string
		want []string
	}{
		{"3005", []string{"<strong>Total units:</strong><span>2</span>", "SN-3005-001"}},
		{"3007", []string{"<strong>Total lots:</strong><span>2</span>"}},
		{"3002", []string{"window.PRICE_HISTORY_DASH"}},
		{fullID, []string{`data-popout-url="/popout/part/` + fullID + `/thumb"`}},
	} {
		p := parallelPage{fn: pagePart3005.fn, target: "/part/" + c.id + "/summary", values: map[string]string{"id": c.id}}
		body := p.get(ctx, h).Body.String()
		for _, s := range c.want {
			if !strings.Contains(body, s) {
				t.Errorf("part %s: body missing %q", c.id, s)
			}
		}
	}
}
