package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	arxbase "arx/internal/config"
)

// Characterization (#446): pins down ServeImage's pre-fix behavior of
// hardcoding Content-Type: image/png for every file under IMAGE_ROOT,
// regardless of extension — proves the PDF-specific branch added for #446 is
// additive, not a silent change for the existing image types.
func TestServeImage_ContentType_DefaultsToImagePNG(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.jpg"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	h := &Handler{}
	h.state.Store(&runtimeState{cfg: &arxbase.Config{ImageRoot: root}})
	rec := httptest.NewRecorder()
	h.ServeImage(rec, httptest.NewRequest(http.MethodGet, "/images/foo.jpg", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /images/foo.jpg = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want %q", ct, "image/png")
	}
}

// Red (#446): a .pdf-suffixed file under IMAGE_ROOT must be served as
// application/pdf, not the hardcoded image/png. Fails today (no PDF-specific
// branch exists in ServeImage).
func TestServeImage_PDFContentType(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cert.pdf"), []byte("%PDF-1.4"), 0644); err != nil {
		t.Fatal(err)
	}
	h := &Handler{}
	h.state.Store(&runtimeState{cfg: &arxbase.Config{ImageRoot: root}})
	rec := httptest.NewRecorder()
	h.ServeImage(rec, httptest.NewRequest(http.MethodGet, "/images/cert.pdf", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /images/cert.pdf = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/pdf")
	}
}

// A directory under IMAGE_ROOT must 404, not render a listing.
func TestServeImage_DirectoryNotListed(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if code := serveImage(t, root, "/images/sub"); code != http.StatusNotFound {
		t.Errorf("GET /images/sub = %d, want 404", code)
	}
}

func serveImage(t *testing.T, root, urlPath string) int {
	t.Helper()
	h := &Handler{}
	h.state.Store(&runtimeState{cfg: &arxbase.Config{ImageRoot: root}})
	rec := httptest.NewRecorder()
	h.ServeImage(rec, httptest.NewRequest(http.MethodGet, urlPath, nil))
	return rec.Code
}

// ServeImage must not follow symlinks out of IMAGE_ROOT, including via the
// .PNG fallback (#147).
func TestServeImage_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "pic.PNG"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.PNG"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "pic.PNG"), filepath.Join(root, "pic.PNG")); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"/images/link/secret.txt", "/images/pic", "/images/pic.PNG", "/images/"} {
		if code := serveImage(t, root, p); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, code)
		}
	}
	for _, p := range []string{"/images/ok", "/images/ok.PNG"} {
		if code := serveImage(t, root, p); code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", p, code)
		}
	}
}
