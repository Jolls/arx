package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	arxbase "arx/internal/config"
)

// filesTestHandler builds a Handler with no DB, real templates, and a session
// secret — enough for both the file-serving handlers (no render) and the
// directory-listing handlers (which call h.render, needing templatesFS and a
// working session store for csrfToken).
func filesTestHandler() *Handler {
	cfg := &arxbase.Config{}
	cfg.SessionSecret = "test-secret"
	h := New(nil, nil, cfg, templatesFS, nil)
	if err := h.loadTemplates(); err != nil {
		panic(err)
	}
	return h
}

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", p, err)
	}
	return p
}

// ── ServeLocalFile ───────────────────────────────────────────────────────────

func TestServeLocalFile_RootUnconfigured(t *testing.T) {
	h := filesTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/local/foo.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalFile(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestServeLocalFile_NotFound(t *testing.T) {
	h := filesTestHandler()
	h.cfg().DocControlRoot = t.TempDir()
	req := httptest.NewRequest(http.MethodGet, "/local/missing.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalFile(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServeLocalFile_DirectoryIsNotFound(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().DocControlRoot = root
	if err := os.Mkdir(filepath.Join(root, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/local/sub", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalFile(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// The 400 here comes from a different guard than it looks like: safePath
// itself strips ".." segments rather than rejecting them (see TestSafePath in
// helpers_test.go — ok is always true), so the resolved path is safely under
// root by the time it reaches http.ServeFile. But http.ServeFile has its own
// independent precaution: it rejects any request whose r.URL.Path (the raw,
// unsanitized path) contains a ".." element at all, regardless of where the
// resolved name argument actually points. That guard is what fires here, not
// our own safePath-based check.
func TestServeLocalFile_DotDotInURLRejectedByServeFile(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().DocControlRoot = root
	etcDir := filepath.Join(root, "etc")
	if err := os.Mkdir(etcDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, etcDir, "secret.txt", "under-root")
	req := httptest.NewRequest(http.MethodGet, "/local/../../etc/secret.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalFile(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (http.ServeFile rejects \"..\" in r.URL.Path), body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid URL path") {
		t.Errorf("body = %q, want to contain %q", rec.Body.String(), "invalid URL path")
	}
}

func TestServeLocalFile_PDFInline(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().DocControlRoot = root
	writeTempFile(t, root, "doc.pdf", "%PDF-1.4")
	req := httptest.NewRequest(http.MethodGet, "/local/doc.pdf", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalFile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-cache")
	}
}

func TestServeLocalFile_ImageInline(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().DocControlRoot = root
	writeTempFile(t, root, "pic.png", "fake-png")
	req := httptest.NewRequest(http.MethodGet, "/local/pic.png", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalFile(rec, req)
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
}

func TestServeLocalFile_OtherAttachment(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().DocControlRoot = root
	writeTempFile(t, root, "notes.txt", "hi")
	req := httptest.NewRequest(http.MethodGet, "/local/notes.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalFile(rec, req)
	got := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(got, "attachment") || !strings.Contains(got, "notes.txt") {
		t.Errorf("Content-Disposition = %q, want attachment with filename notes.txt", got)
	}
}

// ── ServeSupplierFile ────────────────────────────────────────────────────────

func TestServeSupplierFile_RootUnconfigured(t *testing.T) {
	h := filesTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/supplier-local/foo.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierFile(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestServeSupplierFile_FallsBackToDocControlRoot(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().DocControlRoot = root
	writeTempFile(t, root, "doc.pdf", "%PDF-1.4")
	req := httptest.NewRequest(http.MethodGet, "/supplier-local/doc.pdf", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierFile(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestServeSupplierFile_NotFound(t *testing.T) {
	h := filesTestHandler()
	h.cfg().SupplierFilesRoot = t.TempDir()
	req := httptest.NewRequest(http.MethodGet, "/supplier-local/missing.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierFile(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// See TestServeLocalFile_DotDotInURLRejectedByServeFile: this 400 comes from
// http.ServeFile's own guard on r.URL.Path, not from safePath (which strips
// ".." rather than rejecting it).
func TestServeSupplierFile_DotDotInURLRejectedByServeFile(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().SupplierFilesRoot = root
	etcDir := filepath.Join(root, "etc")
	if err := os.Mkdir(etcDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, etcDir, "secret.txt", "under-root")
	req := httptest.NewRequest(http.MethodGet, "/supplier-local/../../etc/secret.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierFile(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (http.ServeFile rejects \"..\" in r.URL.Path), body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid URL path") {
		t.Errorf("body = %q, want to contain %q", rec.Body.String(), "invalid URL path")
	}
}

func TestServeSupplierFile_PDFInline(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().SupplierFilesRoot = root
	writeTempFile(t, root, "doc.pdf", "%PDF-1.4")
	req := httptest.NewRequest(http.MethodGet, "/supplier-local/doc.pdf", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierFile(rec, req)
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
}

// ServeSupplierFile now inlines images the same way ServeLocalFile does (#861).
func TestServeSupplierFile_ImageInline(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().SupplierFilesRoot = root
	writeTempFile(t, root, "pic.png", "fake-png")
	req := httptest.NewRequest(http.MethodGet, "/supplier-local/pic.png", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierFile(rec, req)
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
}

// ── ServeSupplierDir ─────────────────────────────────────────────────────────

func TestServeSupplierDir_RootUnconfigured(t *testing.T) {
	h := filesTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/supplier-local-dir/", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierDir(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestServeSupplierDir_NotFound(t *testing.T) {
	h := filesTestHandler()
	h.cfg().SupplierFilesRoot = t.TempDir()
	req := httptest.NewRequest(http.MethodGet, "/supplier-local-dir/missing", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierDir(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServeSupplierDir_FileIsNotFound(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().SupplierFilesRoot = root
	writeTempFile(t, root, "file.txt", "hi")
	req := httptest.NewRequest(http.MethodGet, "/supplier-local-dir/file.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierDir(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServeSupplierDir_DotDotSegmentsStayUnderRoot(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().SupplierFilesRoot = root
	if err := os.Mkdir(filepath.Join(root, "etc"), 0755); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/supplier-local-dir/../../etc", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierDir(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (resolves to root/etc), body=%s", rec.Code, rec.Body.String())
	}
}

func TestServeSupplierDir_FallsBackToDocControlRoot(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().DocControlRoot = root
	req := httptest.NewRequest(http.MethodGet, "/supplier-local-dir/", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierDir(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestServeSupplierDir_SortOrderAndListing(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().SupplierFilesRoot = root
	if err := os.Mkdir(filepath.Join(root, "zzz-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Aaa-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, root, "Banana.txt", "b")
	writeTempFile(t, root, "apple.txt", "a")

	req := httptest.NewRequest(http.MethodGet, "/supplier-local-dir/", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierDir(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// dirs first (case-insensitive: Aaa-dir, zzz-dir), then files (apple.txt, Banana.txt)
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
	if strings.Contains(body, "Up one level") {
		t.Error("root listing should not show an Up one level link")
	}
}

func TestServeSupplierDir_ParentURLAtDepth(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().SupplierFilesRoot = root
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0755); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/supplier-local-dir/a", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierDir(rec, req)
	if !strings.Contains(rec.Body.String(), "Up one level") {
		t.Error("depth-1 listing should show an Up one level link")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/supplier-local-dir/a/b", nil)
	rec2 := httptest.NewRecorder()
	h.ServeSupplierDir(rec2, req2)
	if !strings.Contains(rec2.Body.String(), "Up one level") {
		t.Error("depth-2 listing should show an Up one level link")
	}
}

// ── ServeLocalDir ────────────────────────────────────────────────────────────

func TestServeLocalDir_RootUnconfigured(t *testing.T) {
	h := filesTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/local-dir/", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalDir(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestServeLocalDir_NotFound(t *testing.T) {
	h := filesTestHandler()
	h.cfg().DocControlRoot = t.TempDir()
	req := httptest.NewRequest(http.MethodGet, "/local-dir/missing", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalDir(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServeLocalDir_FileIsNotFound(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().DocControlRoot = root
	writeTempFile(t, root, "file.txt", "hi")
	req := httptest.NewRequest(http.MethodGet, "/local-dir/file.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalDir(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServeLocalDir_DotDotSegmentsStayUnderRoot(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().DocControlRoot = root
	if err := os.Mkdir(filepath.Join(root, "etc"), 0755); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/local-dir/../../etc", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalDir(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (resolves to root/etc), body=%s", rec.Code, rec.Body.String())
	}
}

func TestServeLocalDir_SortOrderAndListing(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().DocControlRoot = root
	if err := os.Mkdir(filepath.Join(root, "zzz-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Aaa-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, root, "Banana.txt", "b")
	writeTempFile(t, root, "apple.txt", "a")

	req := httptest.NewRequest(http.MethodGet, "/local-dir/", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalDir(rec, req)
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

func TestServeLocalDir_ParentURLAtDepth(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg().DocControlRoot = root
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0755); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/local-dir/", nil)
	rec := httptest.NewRecorder()
	h.ServeLocalDir(rec, req)
	if strings.Contains(rec.Body.String(), "Up one level") {
		t.Error("root listing should not show an Up one level link")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/local-dir/a", nil)
	rec2 := httptest.NewRecorder()
	h.ServeLocalDir(rec2, req2)
	if !strings.Contains(rec2.Body.String(), "Up one level") {
		t.Error("depth-1 listing should show an Up one level link")
	}

	req3 := httptest.NewRequest(http.MethodGet, "/local-dir/a/b", nil)
	rec3 := httptest.NewRecorder()
	h.ServeLocalDir(rec3, req3)
	if !strings.Contains(rec3.Body.String(), "Up one level") {
		t.Error("depth-2 listing should show an Up one level link")
	}
}
