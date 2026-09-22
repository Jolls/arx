package main

import (
	"net/http"
	"os"
	"strings"
)

// ServeImage — GET /images/*
// Serves image files from IMAGE_ROOT. Auto-appends .PNG if the exact path is missing.
func (h *Handler) ServeImage(w http.ResponseWriter, r *http.Request) {
	root := h.cfg.ImageRoot
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
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", "inline")
	http.ServeFile(w, r, fullPath)
}
