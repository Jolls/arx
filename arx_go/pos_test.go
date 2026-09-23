package main

import (
	"context"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
)

func TestPOApprovalNext(t *testing.T) {
	ok := []struct{ action, from, want string }{
		{"submit", "not_submitted", "pending"},
		{"submit", "rejected", "pending"},
		{"approve", "pending", "approved"},
		{"reject", "pending", "rejected"},
	}
	for _, c := range ok {
		got, valid := poApprovalNext(c.action, c.from)
		if !valid || got != c.want {
			t.Errorf("poApprovalNext(%q,%q) = (%q,%v), want (%q,true)", c.action, c.from, got, valid, c.want)
		}
	}

	bad := []struct{ action, from string }{
		{"submit", "pending"},  // already submitted
		{"submit", "approved"}, // already approved
		{"approve", "not_submitted"},
		{"approve", "approved"}, // not pending
		{"approve", "rejected"},
		{"reject", "not_submitted"},
		{"reject", "approved"},
		{"bogus", "pending"}, // unknown action
	}
	for _, c := range bad {
		if got, valid := poApprovalNext(c.action, c.from); valid {
			t.Errorf("poApprovalNext(%q,%q) = (%q,true), want invalid", c.action, c.from, got)
		}
	}
}

func TestPOApprovalAllowsSend(t *testing.T) {
	if !poApprovalAllowsSend("approved") {
		t.Error("poApprovalAllowsSend(approved) = false, want true")
	}
	for _, s := range []string{"not_submitted", "pending", "rejected", ""} {
		if poApprovalAllowsSend(s) {
			t.Errorf("poApprovalAllowsSend(%q) = true, want false", s)
		}
	}
}

func TestPOApprovalActions(t *testing.T) {
	// A non-approver can submit (when not_submitted/rejected) but never approve/reject.
	for _, status := range []string{"not_submitted", "rejected"} {
		acts := poApprovalActions(status, false)
		if len(acts) != 1 || acts[0].Action != "submit" {
			t.Errorf("poApprovalActions(%q,false) = %+v, want a single submit", status, acts)
		}
	}
	// Pending offers nothing to a non-approver, approve+reject to an approver.
	if acts := poApprovalActions("pending", false); len(acts) != 0 {
		t.Errorf("poApprovalActions(pending,false) = %+v, want none", acts)
	}
	if acts := poApprovalActions("pending", true); len(acts) != 2 {
		t.Errorf("poApprovalActions(pending,true) = %+v, want approve+reject", acts)
	}
	// Approved is terminal — no further actions even for an approver.
	if acts := poApprovalActions("approved", true); len(acts) != 0 {
		t.Errorf("poApprovalActions(approved,true) = %+v, want none", acts)
	}
}

func TestPOCanTransition(t *testing.T) {
	allowed := []struct{ from, to string }{
		{"rfq", "cancelled"}, // decline (#270)
		{"draft", "open"},
		{"draft", "cancelled"},
		{"open", "sent"},
		{"sent", "partially_received"},
		{"sent", "closed"},
		{"partially_received", "closed"},
		{"closed", "open"},     // reopen
		{"cancelled", "draft"}, // reopen
	}
	for _, c := range allowed {
		if !poCanTransition(c.from, c.to) {
			t.Errorf("poCanTransition(%q,%q) = false, want true", c.from, c.to)
		}
	}

	disallowed := []struct{ from, to string }{
		{"rfq", "draft"},               // awarding is a duplicate-to-PO, not a generic transition (#270)
		{"rfq", "sent"},                // RFQ can only be declined
		{"draft", "rfq"},               // nothing transitions into rfq
		{"draft", "sent"},              // must go through open
		{"draft", "closed"},            // skips the chain
		{"open", "partially_received"}, // must be sent first
		{"closed", "cancelled"},        // closed only reopens to open
		{"cancelled", "open"},          // cancelled only reopens to draft
		{"sent", "draft"},              // no backward jump
		{"draft", "draft"},             // no self-transition
		{"bogus", "open"},              // unknown source
	}
	for _, c := range disallowed {
		if poCanTransition(c.from, c.to) {
			t.Errorf("poCanTransition(%q,%q) = true, want false", c.from, c.to)
		}
	}
}

func TestRFQBaseNumber(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1050R1", "1050"},
		{"1050R2", "1050"},
		{"1050R10", "1050"}, // multi-digit suffix
		{"1050", "1050"},    // no suffix (already a bare PO number)
		{"1050R", "1050R"},  // 'R' without a number is not a suffix
		{"12R3R4", "12R3"},  // only the trailing R<n> is stripped
		{"", ""},
	}
	for _, c := range cases {
		if got := rfqBaseNumber(c.in); got != c.want {
			t.Errorf("rfqBaseNumber(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildRFQGrid(t *testing.T) {
	lines := []rfqScanLine{
		{Number: "1050R1", SupplierName: "Acme", Status: "rfq", Total: 30, HasLine: true, POLID: 1, PartNumber: "P-1", Qty: 2, Cost: 10},
		{Number: "1050R1", SupplierName: "Acme", Status: "rfq", Total: 30, HasLine: true, POLID: 2, PartNumber: "P-2", Qty: 1, Cost: 10},
		{Number: "1050R2", SupplierName: "Globex", Status: "rfq", Total: 50, HasLine: true, POLID: 3, PartNumber: "P-1", Qty: 2, Cost: 25},
		// Globex did not quote P-2 → tests the ragged-row padding path.
		{Number: "1050R3", SupplierName: "Initech", Status: "rfq", Total: 0, HasLine: false}, // a quote with no lines
	}
	suppliers, rows := buildRFQGrid(lines)

	// Three columns, in first-seen order.
	if len(suppliers) != 3 {
		t.Fatalf("got %d suppliers, want 3", len(suppliers))
	}
	for i, name := range []string{"Acme", "Globex", "Initech"} {
		if suppliers[i].SupplierName != name {
			t.Errorf("column %d = %q, want %q", i, suppliers[i].SupplierName, name)
		}
	}

	// Acme has the lowest non-zero total (30); Initech's 0 total is ignored.
	if !suppliers[0].Best {
		t.Error("Acme (lowest total) should be the best supplier")
	}
	if suppliers[1].Best || suppliers[2].Best {
		t.Error("only Acme should be marked the best supplier")
	}

	// Two part rows, each padded to the full column count.
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, row := range rows {
		if len(row.Cells) != 3 {
			t.Errorf("row %q has %d cells, want 3 (padded to column count)", row.PartNumber, len(row.Cells))
		}
	}

	// P-1: Acme (10) beats Globex (25); Initech has no cell (padded empty).
	p1 := rows[0]
	if p1.PartNumber != "P-1" {
		t.Fatalf("first row = %q, want P-1", p1.PartNumber)
	}
	if !p1.Cells[0].Best {
		t.Error("P-1: Acme cell should be marked best (10 < 25)")
	}
	if p1.Cells[1].Best {
		t.Error("P-1: Globex cell should not be best")
	}
	if p1.Cells[2].Quoted {
		t.Error("P-1: Initech has no line → cell should be unquoted")
	}

	// P-2: only Acme quoted; Globex/Initech are padded empties.
	p2 := rows[1]
	if p2.PartNumber != "P-2" {
		t.Fatalf("second row = %q, want P-2", p2.PartNumber)
	}
	if !p2.Cells[0].Quoted || !p2.Cells[0].Best {
		t.Error("P-2: Acme cell should be quoted and best")
	}
	if p2.Cells[1].Quoted || p2.Cells[2].Quoted {
		t.Error("P-2: Globex/Initech cells should be empty (padded)")
	}
}

func TestBuildRFQGridEmpty(t *testing.T) {
	suppliers, rows := buildRFQGrid(nil)
	if len(suppliers) != 0 || len(rows) != 0 {
		t.Errorf("buildRFQGrid(nil) = (%d suppliers, %d rows), want (0, 0)", len(suppliers), len(rows))
	}
}

func TestDerivePOReceiptStatus(t *testing.T) {
	line := func(ordered, received float64) models.PurchaseOrderLine {
		return models.PurchaseOrderLine{Qty: ordered, ReceivedQty: received}
	}
	cases := []struct {
		name  string
		items []models.PurchaseOrderLine
		want  string
	}{
		{"nothing received", []models.PurchaseOrderLine{line(10, 0), line(5, 0)}, ""},
		{"some received", []models.PurchaseOrderLine{line(10, 4), line(5, 0)}, "partially_received"},
		{"all fully received", []models.PurchaseOrderLine{line(10, 10), line(5, 5)}, "closed"},
		{"over-receipt counts as full", []models.PurchaseOrderLine{line(10, 12), line(5, 5)}, "closed"},
		{"mixed full and partial", []models.PurchaseOrderLine{line(10, 10), line(5, 2)}, "partially_received"},
		{"no lines", nil, ""},
	}
	for _, c := range cases {
		if got := derivePOReceiptStatus(c.items); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestParseReceiveDeltas(t *testing.T) {
	items := []models.PurchaseOrderLine{{ID: 1}, {ID: 2}, {ID: 3}}

	// Mixed submission: line 1 a valid qty, line 2 blank (skipped), line 3 zero (ignored).
	form := map[string]string{"recv[1]": "  4.5 ", "recv[2]": "", "recv[3]": "0"}
	got, err := parseReceiveDeltas(items, func(k string) string { return form[k] })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[1] != 4.5 {
		t.Errorf("got %v, want only {1:4.5}", got)
	}

	// Nothing entered → empty map, no error (caller rejects empties).
	if got, err := parseReceiveDeltas(items, func(string) string { return "" }); err != nil || len(got) != 0 {
		t.Errorf("empty submission = (%v, %v), want (empty, nil)", got, err)
	}

	// Negative quantities are ignored, not errors.
	if got, _ := parseReceiveDeltas(items, func(k string) string {
		if k == "recv[2]" {
			return "-3"
		}
		return ""
	}); len(got) != 0 {
		t.Errorf("negative qty produced %v, want empty", got)
	}

	// A non-numeric value is a hard error.
	if _, err := parseReceiveDeltas(items, func(k string) string {
		if k == "recv[1]" {
			return "abc"
		}
		return ""
	}); err == nil {
		t.Error("non-numeric quantity should error")
	}
}

func TestPOStatusActions(t *testing.T) {
	// Every action target must be a valid transition from the current status,
	// and labels/classes must be populated.
	for _, status := range []string{"rfq", "draft", "open", "sent", "partially_received", "closed", "cancelled"} {
		actions := poStatusActions(status)
		if len(actions) == 0 {
			t.Errorf("poStatusActions(%q) returned no actions", status)
		}
		for _, a := range actions {
			if !poCanTransition(status, a.Target) {
				t.Errorf("poStatusActions(%q) offered invalid target %q", status, a.Target)
			}
			if a.Label == "" || a.Class == "" {
				t.Errorf("poStatusActions(%q) target %q missing label/class", status, a.Target)
			}
		}
	}
}

// ── findPOBaseFolder ─────────────────────────────────────────────────────────

func TestFindPOBaseFolder(t *testing.T) {
	root := t.TempDir()
	if got := findPOBaseFolder(root, "PO-100"); got != "" {
		t.Errorf("findPOBaseFolder(empty root) = %q, want empty", got)
	}

	if err := os.Mkdir(filepath.Join(root, "PO-200 Vendor"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := findPOBaseFolder(root, "PO-100"); got != "" {
		t.Errorf("findPOBaseFolder(no matching prefix) = %q, want empty", got)
	}
	if got := findPOBaseFolder(root, "PO-200"); got != "PO-200 Vendor" {
		t.Errorf("findPOBaseFolder(one match) = %q, want %q", got, "PO-200 Vendor")
	}
}

// ── renderPOFolder ───────────────────────────────────────────────────────────
// Called directly with a manually-built PurchaseOrder, bypassing the
// DB-backed fetchPO (see filesTestHandler in files_test.go for the DB-free
// Handler used throughout this file).

func TestRenderPOFolder_RootUnconfigured(t *testing.T) {
	h := filesTestHandler()
	po := models.PurchaseOrder{ID: 1, Number: "PO-100"}
	req := httptest.NewRequest(http.MethodGet, "/po/PO-100/folder", nil)
	rec := httptest.NewRecorder()
	h.renderPOFolder(rec, req, po, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRenderPOFolder_NoMatchingBaseFolder(t *testing.T) {
	h := filesTestHandler()
	h.cfg.POFolderRoot = t.TempDir()
	po := models.PurchaseOrder{ID: 1, Number: "PO-100"}
	req := httptest.NewRequest(http.MethodGet, "/po/PO-100/folder", nil)
	rec := httptest.NewRecorder()
	h.renderPOFolder(rec, req, po, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRenderPOFolder_SubpathNotFound(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.POFolderRoot = root
	if err := os.Mkdir(filepath.Join(root, "PO-100 Vendor"), 0755); err != nil {
		t.Fatal(err)
	}
	po := models.PurchaseOrder{ID: 1, Number: "PO-100"}
	req := httptest.NewRequest(http.MethodGet, "/po/PO-100/folder/missing", nil)
	rec := httptest.NewRecorder()
	h.renderPOFolder(rec, req, po, []string{"missing"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// NOT COVERED: renderPOFolder, unlike renderSupplierFolder, does not re-derive
// a containment check after filepath.Join(base, subParts...) — it relies
// entirely on os.Stat failing for a path that doesn't exist. subParts reaching
// here already went through POFolderSub's per-segment filepath.Base
// sanitizing in the real route, but renderPOFolder itself has no guard against
// raw ".." if called some other way. That's a real gap, not test scope for
// #863 (which pins behavior, not fixes it) — left as a known drift, not
// asserted as either "rejected" or "escapes", since fabricating either
// assertion here would misrepresent what the code actually guarantees.

func TestRenderPOFolder_SortOrderAndListing(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.POFolderRoot = root
	base := filepath.Join(root, "PO-100 Vendor")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "zzz-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "Aaa-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, base, "Banana.txt", "b")
	writeTempFile(t, base, "apple.txt", "a")

	po := models.PurchaseOrder{ID: 1, Number: "PO-100"}
	req := httptest.NewRequest(http.MethodGet, "/po/PO-100/folder", nil)
	rec := httptest.NewRecorder()
	h.renderPOFolder(rec, req, po, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	posAaa := strings.Index(body, "Aaa-dir")
	posZzz := strings.Index(body, "zzz-dir")
	posApple := strings.Index(body, "apple.txt")
	posBanana := strings.Index(body, "Banana.txt")
	if posAaa < 0 || posZzz < 0 || posApple < 0 || posBanana < 0 {
		t.Fatalf("expected all four entries in body, got:\n%s", body)
	}
	if !(posAaa < posZzz && posZzz < posApple && posApple < posBanana) {
		t.Errorf("expected order Aaa-dir < zzz-dir < apple.txt < Banana.txt, got positions %d,%d,%d,%d",
			posAaa, posZzz, posApple, posBanana)
	}
}

func TestRenderPOFolder_ParentURLAtDepth(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.POFolderRoot = root
	base := filepath.Join(root, "PO-100 Vendor")
	if err := os.MkdirAll(filepath.Join(base, "a", "b"), 0755); err != nil {
		t.Fatal(err)
	}
	po := models.PurchaseOrder{ID: 1, Number: "PO-100"}

	req := httptest.NewRequest(http.MethodGet, "/po/PO-100/folder", nil)
	rec := httptest.NewRecorder()
	h.renderPOFolder(rec, req, po, nil)
	if strings.Contains(rec.Body.String(), "Up one level") {
		t.Error("depth-0 listing should not show an Up one level link")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/po/PO-100/folder/a", nil)
	rec2 := httptest.NewRecorder()
	h.renderPOFolder(rec2, req2, po, []string{"a"})
	if !strings.Contains(rec2.Body.String(), "Up one level") {
		t.Error("depth-1 listing should show an Up one level link")
	}

	req3 := httptest.NewRequest(http.MethodGet, "/po/PO-100/folder/a/b", nil)
	rec3 := httptest.NewRecorder()
	h.renderPOFolder(rec3, req3, po, []string{"a", "b"})
	if !strings.Contains(rec3.Body.String(), "Up one level") {
		t.Error("depth-2 listing should show an Up one level link")
	}
}

// ── POFile ───────────────────────────────────────────────────────────────────
// POFile has no DB dependency (unlike SupplierFile), so it's tested directly
// through the exported handler via chi's URLParam.

func poFileRequest(t *testing.T, num, subpath string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/po/"+num+"/file/"+subpath, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", num)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestPOFile_RootUnconfigured(t *testing.T) {
	h := filesTestHandler()
	req := poFileRequest(t, "PO-100", "doc.pdf")
	rec := httptest.NewRecorder()
	h.POFile(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestPOFile_NoMatchingBaseFolder(t *testing.T) {
	h := filesTestHandler()
	h.cfg.POFolderRoot = t.TempDir()
	req := poFileRequest(t, "PO-100", "doc.pdf")
	rec := httptest.NewRecorder()
	h.POFile(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// POFile sanitizes its own path segments (dropping ".." like safePath does),
// but http.ServeFile independently rejects any request whose r.URL.Path
// contains a ".." element at all — see
// TestServeLocalFile_DotDotInURLRejectedByServeFile in files_test.go.
func TestPOFile_DotDotInURLRejectedByServeFile(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.POFolderRoot = root
	base := filepath.Join(root, "PO-100 Vendor")
	etcDir := filepath.Join(base, "etc")
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, etcDir, "secret.txt", "under-root")
	req := poFileRequest(t, "PO-100", "../../etc/secret.txt")
	rec := httptest.NewRecorder()
	h.POFile(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (http.ServeFile rejects \"..\" in r.URL.Path), body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid URL path") {
		t.Errorf("body = %q, want to contain %q", rec.Body.String(), "invalid URL path")
	}
}

func TestPOFile_NotFound(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.POFolderRoot = root
	if err := os.Mkdir(filepath.Join(root, "PO-100 Vendor"), 0755); err != nil {
		t.Fatal(err)
	}
	req := poFileRequest(t, "PO-100", "missing.txt")
	rec := httptest.NewRecorder()
	h.POFile(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestPOFile_PDFInline(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.POFolderRoot = root
	base := filepath.Join(root, "PO-100 Vendor")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, base, "doc.pdf", "%PDF-1.4")
	req := poFileRequest(t, "PO-100", "doc.pdf")
	rec := httptest.NewRecorder()
	h.POFile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
	// See ServeLocalFile/ServeSupplierFile in files.go (#839): force
	// revalidation so a replaced file isn't served stale from cache.
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-cache")
	}
}

func TestPOFile_OtherAttachment(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.POFolderRoot = root
	base := filepath.Join(root, "PO-100 Vendor")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, base, "notes.txt", "hi")
	req := poFileRequest(t, "PO-100", "notes.txt")
	rec := httptest.NewRecorder()
	h.POFile(rec, req)
	got := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(got, "attachment") || !strings.Contains(got, "notes.txt") {
		t.Errorf("Content-Disposition = %q, want attachment with filename notes.txt", got)
	}
}

func TestPOFile_NonASCIIFilenameEncoded(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.POFolderRoot = root
	base := filepath.Join(root, "PO-100 Vendor")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	// A non-ASCII filename would produce a malformed header with naive
	// `filename="` + name + `"` concatenation (#363); mime.FormatMediaType
	// RFC 6266-encodes it correctly.
	writeTempFile(t, base, "café.txt", "hi")
	req := poFileRequest(t, "PO-100", "café.txt")
	rec := httptest.NewRecorder()
	h.POFile(rec, req)
	got := rec.Header().Get("Content-Disposition")
	if _, params, err := mime.ParseMediaType(got); err != nil {
		t.Fatalf("Content-Disposition = %q is not valid RFC 6266: %v", got, err)
	} else if params["filename"] != "café.txt" {
		t.Errorf("filename param = %q, want %q", params["filename"], "café.txt")
	}
}

func TestPOFile_ImageInline(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.POFolderRoot = root
	base := filepath.Join(root, "PO-100 Vendor")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, base, "pic.png", "fake-png")
	req := poFileRequest(t, "PO-100", "pic.png")
	rec := httptest.NewRecorder()
	h.POFile(rec, req)
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
}

// A traversal-shaped {id} must be rejected before any directory is created (#170).
func TestPOOpenFolder_RejectsTraversalID(t *testing.T) {
	h := filesTestHandler()
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	h.cfg.POFolderRoot = root
	for _, id := range []string{"..", `..\escaped`, "../escaped"} {
		req := httptest.NewRequest(http.MethodPost, "/po/x/open-folder", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.POOpenFolder(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("id %q: status = %d, want 400", id, rec.Code)
		}
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 1 {
		t.Errorf("unexpected entries created outside root: %v", entries)
	}
}
