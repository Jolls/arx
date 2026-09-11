//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// smoke_post_test.go — #374.
//
// A table-driven smoke test over the high-value create routes: each case POSTs
// to a handler and asserts (a) a non-500 response and (b) that the target
// table's row count went up by exactly one. This is the cheap "did I break a
// handler" net the issue asks for — it catches silent insert failures, wrong
// table/column names, and busted form parsing without per-route assertions.
//
// Like the rest of integration_test.go it runs against ArxDev only (set
// ARX_TEST_DSN) and is excluded from the default `go test ./...` sweep and from
// build.bat. It reuses liveHandler/postForm/withID from integration_test.go.
//
// Scope is the clean create+delete routes. Routes with heavier preconditions —
// POCreate (PO sequence), CreateRecord (step materialization), and the various
// update/transition/toggle routes — are deliberate follow-ups; this first cut
// covers the create surface most likely to silently break.

var smokeSeq int64

// smokeUniq returns a collision-free token for unique part numbers / names.
func smokeUniq(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), atomic.AddInt64(&smokeSeq, 1))
}

func smokeExec(ctx context.Context, h *Handler, query string, args ...any) {
	_, _ = h.DB().ExecContext(ctx, query, args...)
}

func countRows(t *testing.T, h *Handler, ctx context.Context, table string) int {
	t.Helper()
	var n int
	if err := h.DB().QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// locID parses the numeric id out of a redirect Location like "/part/42".
func locID(t *testing.T, rec *httptest.ResponseRecorder, prefix string) int {
	t.Helper()
	loc := rec.Header().Get("Location")
	id, err := strconv.Atoi(strings.TrimPrefix(loc, prefix))
	if err != nil || id == 0 {
		t.Fatalf("could not parse id from Location %q (prefix %q): %v", loc, prefix, err)
	}
	return id
}

// seedPart creates a part of the given category via PartsCreate and returns its
// id plus a cleanup that hard-deletes it.
func seedPart(t *testing.T, h *Handler, ctx context.Context, category string) (int, func()) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.PartsCreate(rec, postForm("/parts", url.Values{
		"part_number":    {smokeUniq("SMOKE-PN")},
		"revision":       {"A"},
		"description":    {"smoke part"},
		"category":       {category},
		"release_status": {"U"},
		"active":         {"1"},
	}))
	id := locID(t, rec, "/part/")
	return id, func() {
		smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=@p1", h.cfg.PartsTable()), id)
	}
}

// seedSupplier creates a company via SuppliersCreate and returns its id plus a
// cleanup. The same row doubles as a manufacturer for MfgPartCreate.
func seedSupplier(t *testing.T, h *Handler, ctx context.Context) (int, func()) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.SuppliersCreate(rec, postForm("/suppliers", url.Values{
		"name":            {smokeUniq("SMOKE-SUP")},
		"is_active":       {"1"},
		"is_supplier":     {"1"},
		"is_manufacturer": {"1"},
	}))
	id := locID(t, rec, "/supplier/")
	return id, func() {
		smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=@p1", h.cfg.CompanyTable()), id)
	}
}

type smokePost struct {
	name   string
	table  func() string // table whose COUNT(*) must rise by one
	invoke func(t *testing.T) (*httptest.ResponseRecorder, func())
}

func TestIntegration_PostRoutesSmoke(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	cases := []smokePost{
		{
			name:  "PartsCreate",
			table: h.cfg.PartsTable,
			invoke: func(t *testing.T) (*httptest.ResponseRecorder, func()) {
				rec := httptest.NewRecorder()
				h.PartsCreate(rec, postForm("/parts", url.Values{
					"part_number":    {smokeUniq("SMOKE-PN")},
					"revision":       {"A"},
					"description":    {"smoke part"},
					"category":       {"BUY"},
					"release_status": {"U"},
					"active":         {"1"},
				}))
				id := locID(t, rec, "/part/")
				return rec, func() {
					smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=@p1", h.cfg.PartsTable()), id)
				}
			},
		},
		{
			name:  "SuppliersCreate",
			table: h.cfg.CompanyTable,
			invoke: func(t *testing.T) (*httptest.ResponseRecorder, func()) {
				rec := httptest.NewRecorder()
				h.SuppliersCreate(rec, postForm("/suppliers", url.Values{
					"name": {smokeUniq("SMOKE-SUP")}, "is_active": {"1"}, "is_supplier": {"1"},
				}))
				id := locID(t, rec, "/supplier/")
				return rec, func() {
					smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=@p1", h.cfg.CompanyTable()), id)
				}
			},
		},
		{
			name:  "ContactsCreate",
			table: h.cfg.ContactTable,
			invoke: func(t *testing.T) (*httptest.ResponseRecorder, func()) {
				rec := httptest.NewRecorder()
				h.ContactsCreate(rec, postForm("/contacts", url.Values{
					"CNName": {smokeUniq("SMOKE-CON")}, "CNActive": {"1"},
				}))
				id := locID(t, rec, "/contact/")
				return rec, func() {
					smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=@p1", h.cfg.ContactTable()), id)
				}
			},
		},
		{
			name:  "SupplierPartCreate",
			table: h.cfg.SupplierPartTable,
			invoke: func(t *testing.T) (*httptest.ResponseRecorder, func()) {
				partID, pc := seedPart(t, h, ctx, "BUY")
				supID, sc := seedSupplier(t, h, ctx)
				rec := httptest.NewRecorder()
				h.SupplierPartCreate(rec, withID(postForm(
					fmt.Sprintf("/part/%d/suppliers", partID), url.Values{
						"supplier_id":   {strconv.Itoa(supID)},
						"preference":    {"1"},
						"supplier_pn":   {smokeUniq("SMOKE-SPN")},
						"supplier_desc": {"smoke"},
						"lead_time":     {"5"},
					}), partID))
				return rec, func() {
					smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE part_id=@p1", h.cfg.SupplierPartTable()), partID)
					pc()
					sc()
				}
			},
		},
		{
			name:  "PriceCreate",
			table: h.cfg.PriceTable,
			invoke: func(t *testing.T) (*httptest.ResponseRecorder, func()) {
				partID, pc := seedPart(t, h, ctx, "BUY")
				supID, sc := seedSupplier(t, h, ctx)
				rec := httptest.NewRecorder()
				h.PriceCreate(rec, withID(postForm(
					fmt.Sprintf("/part/%d/pricing", partID), url.Values{
						"supplier_id":    {strconv.Itoa(supID)},
						"pack_size":      {"1"},
						"price_ea":       {"1.50"},
						"price_pack":     {"1.50"},
						"effective_date": {time.Now().Format("2006-01-02")},
					}), partID))
				return rec, func() {
					smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE part_id=@p1", h.cfg.PriceTable()), partID)
					pc()
					sc()
				}
			},
		},
		{
			name:  "MfgPartCreate",
			table: h.cfg.MfgPartTable,
			invoke: func(t *testing.T) (*httptest.ResponseRecorder, func()) {
				partID, pc := seedPart(t, h, ctx, "BUY")
				mfgID, mc := seedSupplier(t, h, ctx)
				rec := httptest.NewRecorder()
				h.MfgPartCreate(rec, withID(postForm(
					fmt.Sprintf("/part/%d/mfg-parts", partID), url.Values{
						"mfg_id":          {strconv.Itoa(mfgID)},
						"mfg_part_number": {smokeUniq("SMOKE-MPN")},
						"description":     {"smoke"},
					}), partID))
				return rec, func() {
					smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE part_id=@p1", h.cfg.MfgPartTable()), partID)
					pc()
					mc()
				}
			},
		},
		{
			name:  "CreateForm",
			table: h.cfg.FormsTable,
			invoke: func(t *testing.T) (*httptest.ResponseRecorder, func()) {
				partID, pc := seedPart(t, h, ctx, "FORM")
				rec := httptest.NewRecorder()
				h.CreateForm(rec, postForm("/forms/new", url.Values{
					"pnid": {strconv.Itoa(partID)},
				}))
				return rec, func() {
					smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE part_number_id=@p1", h.cfg.FormsTable()), partID)
					pc()
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := countRows(t, h, ctx, c.table())
			rec, cl := c.invoke(t)
			defer cl()

			if rec.Code >= 500 {
				t.Fatalf("%s: status %d (want non-500). body: %s", c.name, rec.Code, rec.Body.String())
			}
			if delta := countRows(t, h, ctx, c.table()) - before; delta != 1 {
				t.Errorf("%s: %s row count changed by %d, want 1 (status %d)",
					c.name, c.table(), delta, rec.Code)
			}
		})
	}
}
