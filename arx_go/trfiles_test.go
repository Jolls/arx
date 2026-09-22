package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	arxbase "arx/arxlib/config"
)

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
	h := &Handler{cfg: &arxbase.Config{ImageRoot: root}}
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
