package main

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// ServeLocalFile — GET /local/*
// Serves a single file from DOC_CONTROL_ROOT.
// PDFs open inline; everything else triggers a download.
func (h *Handler) ServeLocalFile(w http.ResponseWriter, r *http.Request) {
	root := h.cfg.DocControlRoot
	if root == "" {
		http.Error(w, "DOC_CONTROL_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}

	splat := strings.TrimPrefix(r.URL.Path, "/local/")
	path, ok := safePath(root, splat)
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
	path, ok := safePath(root, splat)
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

	if strings.ToLower(filepath.Ext(path)) == ".pdf" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	}
	// See ServeLocalFile: force revalidation so a replaced file isn't served
	// stale from the browser cache under its unchanged URL (#839).
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
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

	var parentURL string
	if len(relParts) > 1 {
		parentURL = "/supplier-local-dir/" + strings.Join(relParts[:len(relParts)-1], "/")
	} else if len(relParts) == 1 {
		parentURL = "/supplier-local-dir/"
	}

	dirName := filepath.Base(path)
	if dirName == "." || dirName == "" {
		dirName = filepath.Base(root)
	}

	rawEntries, err := os.ReadDir(path)
	if err != nil {
		http.Error(w, "Error reading directory", http.StatusInternalServerError)
		return
	}
	sort.Slice(rawEntries, func(i, j int) bool {
		di, dj := rawEntries[i].IsDir(), rawEntries[j].IsDir()
		if di != dj {
			return di
		}
		return strings.ToLower(rawEntries[i].Name()) < strings.ToLower(rawEntries[j].Name())
	})

	var entries []DirEntry
	var numDirs, numFiles int
	for _, e := range rawEntries {
		name := e.Name()
		isDir := e.IsDir()
		var relURL string
		if len(relParts) > 0 {
			relURL = strings.Join(append(relParts, name), "/")
		} else {
			relURL = name
		}

		entry := DirEntry{Name: name, IsDir: isDir}
		if isDir {
			entry.URL = "/supplier-local-dir/" + relURL
			numDirs++
		} else {
			entry.URL = "/supplier-local/" + relURL
			ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))
			entry.Ext = ext
			if fi, err := e.Info(); err == nil {
				entry.Size = formatFileSize(fi.Size())
			}
			numFiles++
		}
		entries = append(entries, entry)
	}

	h.render(w, r, "shared/local_dir.html", map[string]any{
		"DirName":   dirName,
		"FullPath":  path,
		"ParentURL": parentURL,
		"Entries":   entries,
		"NumDirs":   numDirs,
		"NumFiles":  numFiles,
		"ActiveTab": "",
		"TestMode":  h.cfg.TestMode,
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

	// Build relative segments for parent URL calculation
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

	var parentURL string
	if len(relParts) > 1 {
		parentURL = "/local-dir/" + strings.Join(relParts[:len(relParts)-1], "/")
	} else if len(relParts) == 1 {
		parentURL = "/local-dir/"
	}

	dirName := filepath.Base(path)
	if dirName == "." || dirName == "" {
		dirName = filepath.Base(root)
	}

	// Read and sort entries: dirs first, then files, both alphabetical
	rawEntries, err := os.ReadDir(path)
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

	var entries []DirEntry
	var numDirs, numFiles int
	for _, e := range rawEntries {
		name := e.Name()
		isDir := e.IsDir()
		var relURL string
		if len(relParts) > 0 {
			relURL = strings.Join(append(relParts, name), "/")
		} else {
			relURL = name
		}

		entry := DirEntry{Name: name, IsDir: isDir}
		if isDir {
			entry.URL = "/local-dir/" + relURL
			numDirs++
		} else {
			entry.URL = "/local/" + relURL
			ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))
			entry.Ext = ext
			if fi, err := e.Info(); err == nil {
				entry.Size = formatFileSize(fi.Size())
			}
			numFiles++
		}
		entries = append(entries, entry)
	}

	h.render(w, r, "shared/local_dir.html", map[string]any{
		"DirName":   dirName,
		"FullPath":  path,
		"ParentURL": parentURL,
		"Entries":   entries,
		"NumDirs":   numDirs,
		"NumFiles":  numFiles,
		"ActiveTab": "",
		"TestMode":  h.cfg.TestMode,
	})
}
