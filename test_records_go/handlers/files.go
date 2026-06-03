package handlers

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"arx/arxlib/urlutil"
)

// ServeLocalFile — GET /local/*
// Serves files from DOC_CONTROL_ROOT. PDFs open inline; everything else downloads.
func (h *Handler) ServeLocalFile(w http.ResponseWriter, r *http.Request) {
	root := h.cfg.DocControlRoot
	if root == "" {
		http.Error(w, "DOC_CONTROL_ROOT is not configured — set it in Settings", http.StatusServiceUnavailable)
		return
	}
	fullPath := resolveUnder(root, strings.TrimPrefix(r.URL.Path, "/local/"))
	if fullPath == "" {
		http.NotFound(w, r)
		return
	}
	if strings.ToLower(filepath.Ext(fullPath)) == ".pdf" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(fullPath)}))
	}
	http.ServeFile(w, r, fullPath)
}

// ServeImage — GET /images/*
// Serves image files from IMAGE_ROOT. Auto-appends .PNG if the exact path is missing.
func (h *Handler) ServeImage(w http.ResponseWriter, r *http.Request) {
	root := h.cfg.ImageRoot
	if root == "" {
		http.Error(w, "IMAGE_ROOT is not configured — set it in Settings", http.StatusServiceUnavailable)
		return
	}
	fullPath := resolveUnder(root, strings.TrimPrefix(r.URL.Path, "/images/"))
	if fullPath == "" {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		pngPath := fullPath + ".PNG"
		if _, err2 := os.Stat(pngPath); os.IsNotExist(err2) {
			http.NotFound(w, r)
			return
		}
		fullPath = pngPath
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", "inline")
	http.ServeFile(w, r, fullPath)
}

// resolveUnder cleans each segment of rawPath via urlutil.SafePathSegments and
// joins it under root. Returns "" if the file does not exist.
func resolveUnder(root, rawPath string) string {
	segs := urlutil.SafePathSegments(rawPath)
	if len(segs) == 0 {
		return ""
	}
	full := filepath.Join(append([]string{root}, segs...)...)
	if _, err := os.Stat(full); os.IsNotExist(err) {
		return ""
	}
	return full
}
