package main

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"arx/arx_go/models"
	"arx/arxlib/urlutil"
)

type DirEntry struct {
	Name  string
	IsDir bool
	URL   string
	Size  string
	Ext   string // uppercase, no dot — e.g. "PDF"
}

// safePath sanitises a URL splat and resolves it under root.
// Returns the absolute path and true, or ("", false) if the result
// would escape root (path traversal attempt).
func safePath(root, splat string) (string, bool) {
	var clean []string
	for _, seg := range strings.Split(splat, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		base := filepath.Base(seg)
		if base != "" && base != "." && base != ".." {
			clean = append(clean, base)
		}
	}

	result := root
	if len(clean) > 0 {
		result = filepath.Join(root, filepath.Join(clean...))
	}

	absRoot, err1 := filepath.Abs(root)
	absResult, err2 := filepath.Abs(result)
	if err1 != nil || err2 != nil {
		return "", false
	}
	// Ensure result is actually inside root
	if !strings.HasPrefix(absResult+string(filepath.Separator), absRoot+string(filepath.Separator)) &&
		absResult != absRoot {
		return "", false
	}
	return result, true
}

// fileServingParams parameterizes serveLocalizedFile by root and URL splat —
// the only real variation across the file-serving handlers (#864).
type fileServingParams struct {
	Root  string
	Splat string
}

// serveLocalizedFile serves a single file from p.Root, resolving p.Splat
// safely under it. PDFs and images open inline; everything else downloads.
func (h *Handler) serveLocalizedFile(w http.ResponseWriter, r *http.Request, p fileServingParams) {
	path, ok := safePath(p.Root, p.Splat)
	if !ok {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && info.IsDir()) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Error accessing file", http.StatusInternalServerError)
		return
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".pdf" || urlutil.IsImage(path) {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	}
	// Force revalidation on every request: an attachment's stored name (and
	// therefore this URL) stays the same when the underlying file is
	// replaced via Edit, so without this a browser can serve the old PDF
	// straight from cache and the replace looks like it silently failed (#839).
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}

// ServeLocalFile — GET /local/*
// Serves a single file from DOC_CONTROL_ROOT.
func (h *Handler) ServeLocalFile(w http.ResponseWriter, r *http.Request) {
	root := h.cfg.DocControlRoot
	if root == "" {
		http.Error(w, "DOC_CONTROL_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}
	splat := strings.TrimPrefix(r.URL.Path, "/local/")
	h.serveLocalizedFile(w, r, fileServingParams{Root: root, Splat: splat})
}

// ServeSupplierFile — GET /supplier-local/*
// Serves a single file from SUPPLIER_FILES_ROOT (falls back to DOC_CONTROL_ROOT).
func (h *Handler) ServeSupplierFile(w http.ResponseWriter, r *http.Request) {
	root := h.cfg.SupplierFilesRoot
	if root == "" {
		root = h.cfg.DocControlRoot
	}
	if root == "" {
		http.Error(w, "SUPPLIER_FILES_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}
	splat := strings.TrimPrefix(r.URL.Path, "/supplier-local/")
	h.serveLocalizedFile(w, r, fileServingParams{Root: root, Splat: splat})
}

// dirListingParams parameterizes renderDirListing by root resolution and URL
// prefixes — the only real variation across the directory-listing handlers (#864).
type dirListingParams struct {
	Path          string // absolute path to list; caller has validated existence/is-dir/containment
	RelParts      []string
	DirURLPrefix  string // no trailing slash
	FileURLPrefix string // no trailing slash
	DirName       string
	ParentURL     string
	PO            *models.PurchaseOrder // set only for PO-folder listings
	Supplier      *models.Supplier      // set only for supplier-folder listings
	ActiveTab     string
	ActiveSubTab  string
	NavBackURL    string
	NavBackLabel  string
}

// dirParentURL derives the "up one level" URL for a directory listing.
// joinPrefix + "/" + parent segments covers 2+ relative segments; rootURL is
// returned as-is for exactly 1 segment (splat-based routes need a trailing
// slash there, entity-scoped routes don't — callers pass what their own
// routing convention requires).
func dirParentURL(joinPrefix, rootURL string, relParts []string) string {
	switch {
	case len(relParts) > 1:
		return joinPrefix + "/" + strings.Join(relParts[:len(relParts)-1], "/")
	case len(relParts) == 1:
		return rootURL
	default:
		return ""
	}
}

// renderDirListing reads p.Path and renders shared/local_dir.html — the sort
// order, entry loop, and render map shared by all four directory-listing sites (#864).
func (h *Handler) renderDirListing(w http.ResponseWriter, r *http.Request, p dirListingParams) {
	rawEntries, err := os.ReadDir(p.Path)
	if err != nil {
		http.Error(w, "Error reading directory", http.StatusInternalServerError)
		return
	}
	sort.Slice(rawEntries, func(i, j int) bool {
		di, dj := rawEntries[i].IsDir(), rawEntries[j].IsDir()
		if di != dj {
			return di // dirs first
		}
		return strings.ToLower(rawEntries[i].Name()) < strings.ToLower(rawEntries[j].Name())
	})

	relPrefix := strings.Join(p.RelParts, "/")
	var entries []DirEntry
	var numDirs, numFiles int
	for _, e := range rawEntries {
		name := e.Name()
		isDir := e.IsDir()
		relURL := name
		if relPrefix != "" {
			relURL = relPrefix + "/" + name
		}

		entry := DirEntry{Name: name, IsDir: isDir}
		if isDir {
			entry.URL = p.DirURLPrefix + "/" + relURL
			numDirs++
		} else {
			entry.URL = p.FileURLPrefix + "/" + relURL
			entry.Ext = strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))
			if fi, err := e.Info(); err == nil {
				entry.Size = formatFileSize(fi.Size())
			}
			numFiles++
		}
		entries = append(entries, entry)
	}

	data := map[string]any{
		"DirName":      p.DirName,
		"FullPath":     p.Path,
		"ParentURL":    p.ParentURL,
		"Entries":      entries,
		"NumDirs":      numDirs,
		"NumFiles":     numFiles,
		"ActiveTab":    p.ActiveTab,
		"ActiveSubTab": p.ActiveSubTab,
		"NavBackURL":   p.NavBackURL,
		"NavBackLabel": p.NavBackLabel,
		"TestMode":     h.cfg.TestMode,
	}
	if p.PO != nil {
		data["PO"] = p.PO
	}
	if p.Supplier != nil {
		data["Supplier"] = p.Supplier
	}
	h.render(w, r, "shared/local_dir.html", data)
}

// relSegments returns path's segments relative to root, split on the OS
// path separator with empties dropped. Used by handlers whose root/path pair
// come from safePath rather than a splat already split into subParts (#864).
func relSegments(root, path string) []string {
	absRoot, _ := filepath.Abs(root)
	absPath, _ := filepath.Abs(path)
	rel, _ := filepath.Rel(absRoot, absPath)
	var relParts []string
	if rel != "." && rel != "" {
		for _, p := range strings.Split(rel, string(filepath.Separator)) {
			if p != "" {
				relParts = append(relParts, p)
			}
		}
	}
	return relParts
}

// ServeSupplierDir — GET /supplier-local-dir/*
// Renders a directory listing from SUPPLIER_FILES_ROOT (falls back to DOC_CONTROL_ROOT).
func (h *Handler) ServeSupplierDir(w http.ResponseWriter, r *http.Request) {
	root := h.cfg.SupplierFilesRoot
	if root == "" {
		root = h.cfg.DocControlRoot
	}
	if root == "" {
		http.Error(w, "SUPPLIER_FILES_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}

	splat := strings.TrimPrefix(r.URL.Path, "/supplier-local-dir/")
	path, ok := safePath(root, splat)
	if !ok {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Error accessing directory", http.StatusInternalServerError)
		return
	}

	relParts := relSegments(root, path)
	parentURL := dirParentURL("/supplier-local-dir", "/supplier-local-dir/", relParts)

	dirName := filepath.Base(path)
	if dirName == "." || dirName == "" {
		dirName = filepath.Base(root)
	}

	h.renderDirListing(w, r, dirListingParams{
		Path: path, RelParts: relParts,
		DirURLPrefix: "/supplier-local-dir", FileURLPrefix: "/supplier-local",
		DirName: dirName, ParentURL: parentURL,
	})
}

// ServeLocalDir — GET /local-dir/*
// Renders a directory listing from DOC_CONTROL_ROOT.
func (h *Handler) ServeLocalDir(w http.ResponseWriter, r *http.Request) {
	root := h.cfg.DocControlRoot
	if root == "" {
		http.Error(w, "DOC_CONTROL_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}

	splat := strings.TrimPrefix(r.URL.Path, "/local-dir/")
	path, ok := safePath(root, splat)
	if !ok {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Error accessing directory", http.StatusInternalServerError)
		return
	}

	relParts := relSegments(root, path)
	parentURL := dirParentURL("/local-dir", "/local-dir/", relParts)

	dirName := filepath.Base(path)
	if dirName == "." || dirName == "" {
		dirName = filepath.Base(root)
	}

	h.renderDirListing(w, r, dirListingParams{
		Path: path, RelParts: relParts,
		DirURLPrefix: "/local-dir", FileURLPrefix: "/local",
		DirName: dirName, ParentURL: parentURL,
	})
}
