package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"arx/arx_go/models"
)

func TestValidateFolderStub(t *testing.T) {
	cases := []struct {
		input   string
		wantErr bool
	}{
		{"", false},
		{"ACME", false},
		{"ACME-01", false},
		{".", true},
		{"..", true},
		{"..\\evil", true},
		{"../evil", true},
		{"a/b", true},
		{"a\\b", true},
		{"a:b", true},
		{"a*b", true},
		{"a?b", true},
		{"a\"b", true},
		{"a<b", true},
		{"a>b", true},
		{"a|b", true},
	}
	for _, c := range cases {
		err := validateFolderStub(c.input)
		if (err != nil) != c.wantErr {
			t.Errorf("validateFolderStub(%q) error = %v, wantErr %v", c.input, err, c.wantErr)
		}
	}
}

// ── renderSupplierFolder ─────────────────────────────────────────────────────
// Called directly with a manually-built Supplier, bypassing the DB-backed
// fetchSupplier (see filesTestHandler in files_test.go for the DB-free Handler
// used throughout this file).

func TestRenderSupplierFolder_RootUnconfigured(t *testing.T) {
	h := filesTestHandler()
	s := models.Supplier{ID: 1, SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/folder", nil)
	rec := httptest.NewRecorder()
	h.renderSupplierFolder(rec, req, s, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRenderSupplierFolder_EmptySupplierCode(t *testing.T) {
	h := filesTestHandler()
	h.cfg.SupplierFilesRoot = t.TempDir()
	s := models.Supplier{ID: 1}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/folder", nil)
	rec := httptest.NewRecorder()
	h.renderSupplierFolder(rec, req, s, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// Unlike files.go's safePath (which strips ".." segments before joining),
// renderSupplierFolder joins raw subParts and then checks containment — so
// passing ".." segments directly here (bypassing SupplierFolderSub's
// per-segment filepath.Base sanitizing) does reach the 400 branch.
func TestRenderSupplierFolder_PathTraversalRejected(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	s := models.Supplier{ID: 1, SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/folder/../../etc", nil)
	rec := httptest.NewRecorder()
	h.renderSupplierFolder(rec, req, s, []string{"..", "..", "etc"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRenderSupplierFolder_NotFound(t *testing.T) {
	h := filesTestHandler()
	h.cfg.SupplierFilesRoot = t.TempDir()
	s := models.Supplier{ID: 1, SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/folder", nil)
	rec := httptest.NewRecorder()
	h.renderSupplierFolder(rec, req, s, nil)
	// No matching supplier subfolder was ever created under root, so this
	// renders shared/error.html (200), unlike files.go's http.NotFound (404) —
	// pin that drift.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (renders error page), body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Vendor folder not found") {
		t.Errorf("body missing 'Vendor folder not found', got:\n%s", rec.Body.String())
	}
}

func TestRenderSupplierFolder_SortOrderAndListing(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	base := filepath.Join(root, "ACME")
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

	s := models.Supplier{ID: 1, SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/folder", nil)
	rec := httptest.NewRecorder()
	h.renderSupplierFolder(rec, req, s, nil)
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

func TestRenderSupplierFolder_ParentURLAtDepth(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	base := filepath.Join(root, "ACME")
	if err := os.MkdirAll(filepath.Join(base, "a", "b"), 0755); err != nil {
		t.Fatal(err)
	}
	s := models.Supplier{ID: 1, SUSupplierCode: "ACME"}

	req := httptest.NewRequest(http.MethodGet, "/supplier/1/folder", nil)
	rec := httptest.NewRecorder()
	h.renderSupplierFolder(rec, req, s, nil)
	if strings.Contains(rec.Body.String(), "Up one level") {
		t.Error("depth-0 listing should not show an Up one level link")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/supplier/1/folder/a", nil)
	rec2 := httptest.NewRecorder()
	h.renderSupplierFolder(rec2, req2, s, []string{"a"})
	if !strings.Contains(rec2.Body.String(), "Up one level") {
		t.Error("depth-1 listing should show an Up one level link")
	}

	req3 := httptest.NewRequest(http.MethodGet, "/supplier/1/folder/a/b", nil)
	rec3 := httptest.NewRecorder()
	h.renderSupplierFolder(rec3, req3, s, []string{"a", "b"})
	if !strings.Contains(rec3.Body.String(), "Up one level") {
		t.Error("depth-2 listing should show an Up one level link")
	}
}

// ── serveSupplierFile ────────────────────────────────────────────────────────
// Called directly with a manually-built Supplier, bypassing the DB-backed
// fetchSupplier (#863 — SupplierFile calls fetchSupplier before any of this
// logic, and that call panics on a nil *sql.DB).

func TestServeSupplierFile_RootUnconfiguredNoDB(t *testing.T) {
	h := filesTestHandler()
	s := models.Supplier{SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/file/doc.pdf", nil)
	rec := httptest.NewRecorder()
	h.serveSupplierFile(rec, req, s, "1")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestServeSupplierFile_EmptySupplierCode(t *testing.T) {
	h := filesTestHandler()
	h.cfg.SupplierFilesRoot = t.TempDir()
	s := models.Supplier{}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/file/doc.pdf", nil)
	rec := httptest.NewRecorder()
	h.serveSupplierFile(rec, req, s, "1")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// http.ServeFile rejects any request whose r.URL.Path contains a ".."
// element outright, independent of safePath's own (permissive) handling —
// see TestServeLocalFile_DotDotInURLRejectedByServeFile in files_test.go.
func TestServeSupplierFile_DotDotInURLRejectedByServeFileNoDB(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	base := filepath.Join(root, "ACME")
	etcDir := filepath.Join(base, "etc")
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, etcDir, "secret.txt", "under-root")
	s := models.Supplier{SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/file/../../etc/secret.txt", nil)
	rec := httptest.NewRecorder()
	h.serveSupplierFile(rec, req, s, "1")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (http.ServeFile rejects \"..\" in r.URL.Path), body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid URL path") {
		t.Errorf("body = %q, want to contain %q", rec.Body.String(), "invalid URL path")
	}
}

func TestServeSupplierFile_NotFoundNoDB(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	if err := os.Mkdir(filepath.Join(root, "ACME"), 0755); err != nil {
		t.Fatal(err)
	}
	s := models.Supplier{SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/file/missing.txt", nil)
	rec := httptest.NewRecorder()
	h.serveSupplierFile(rec, req, s, "1")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServeSupplierFile_PDFInlineNoDB(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	base := filepath.Join(root, "ACME")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, base, "doc.pdf", "%PDF-1.4")
	s := models.Supplier{SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/file/doc.pdf", nil)
	rec := httptest.NewRecorder()
	h.serveSupplierFile(rec, req, s, "1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
	// Unlike files.go's ServeSupplierFile, this codepath sets no Cache-Control
	// header at all — pin that drift.
	if got := rec.Header().Get("Cache-Control"); got != "" {
		t.Errorf("Cache-Control = %q, want empty (no header set)", got)
	}
}

func TestServeSupplierFile_OtherAttachmentNoDB(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	base := filepath.Join(root, "ACME")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, base, "notes.txt", "hi")
	s := models.Supplier{SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/file/notes.txt", nil)
	rec := httptest.NewRecorder()
	h.serveSupplierFile(rec, req, s, "1")
	// Raw string format here, not mime.FormatMediaType like files.go — pin as-is.
	want := `attachment; filename="notes.txt"`
	if got := rec.Header().Get("Content-Disposition"); got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
}
