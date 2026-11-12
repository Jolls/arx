package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// attachContentTypeByExt maps a non-image attach-step file extension to its
// Content-Type for ServeImage, derived from the non-image entries of
// attachResultExts (api.go) — currently just PDF — so the two can't drift
// apart and a future non-image attach type needs no further ServeImage
// change. Deliberately excludes attachResultExts' image/* entries: every
// image extension (including .jpg/.webp/.gif, which aren't actually PNG)
// keeps ServeImage's original blanket image/png Content-Type, since that
// predates #446 and isn't this issue's to fix.
var attachContentTypeByExt = func() map[string]string {
	m := map[string]string{}
	for mimeType, ext := range attachResultExts {
		if !strings.HasPrefix(mimeType, "image/") {
			m[strings.ToLower(ext)] = mimeType
		}
	}
	return m
}()

// ServeImage — GET /images/*
// Serves image files from IMAGE_ROOT. Auto-appends .PNG if the exact path is missing.
func (h *Handler) ServeImage(w http.ResponseWriter, r *http.Request) {
	root := h.cfg().ImageRoot
	if root == "" {
		http.Error(w, "IMAGE_ROOT is not configured — set it in Settings", http.StatusServiceUnavailable)
		return
	}
	splat := strings.TrimPrefix(r.URL.Path, "/images/")
	fullPath, ok := safePath(root, splat)
	if !ok || fullPath == root {
		http.NotFound(w, r)
		return
	}
	if fi, err := os.Stat(fullPath); err != nil || fi.IsDir() {
		pngPath, ok := safePath(root, splat+".PNG")
		if !ok {
			http.NotFound(w, r)
			return
		}
		if fi2, err2 := os.Stat(pngPath); err2 != nil || fi2.IsDir() {
			http.NotFound(w, r)
			return
		}
		fullPath = pngPath
	}
	contentType := "image/png"
	if ct, ok := attachContentTypeByExt[strings.ToLower(filepath.Ext(fullPath))]; ok {
		contentType = ct
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "inline")
	http.ServeFile(w, r, fullPath)
}
