package main

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

// coreTabDirs are the tab-based template subfolders rendered via render()/renderPrint()
// (i.e. everything except templates/shared and templates/records).
var coreTabDirs = []string{"parts", "suppliers", "pos", "contacts", "settings", "reports"}

// corePrintPages are core-tab pages rendered standalone via renderPrint (no layout).
var corePrintPages = []string{"parts/part_bom_paste_preview.html", "pos/po_print.html"}

// parseTemplates parses every page once. Keys: "layout:<dir>/<page>" (render),
// "records:<page>" (renderRecords), "print:<dir>/<page>" (renderPrint),
// "recordsprint:<page>" (renderPrintRecords), "login" (renderLogin).
func parseTemplates(fsys fs.FS) (map[string]*template.Template, error) {
	out := map[string]*template.Template{}
	core, records := coreTemplateFuncs(), recordsTemplateFuncs()
	add := func(key string, funcs template.FuncMap, files ...string) error {
		t, err := template.New("").Funcs(funcs).ParseFS(fsys, files...)
		if err != nil {
			return fmt.Errorf("parse %s: %w", key, err)
		}
		out[key] = t
		return nil
	}
	standalone := map[string]bool{}
	for _, p := range corePrintPages {
		standalone[p] = true
		if err := add("print:"+p, core, "templates/"+p); err != nil {
			return nil, err
		}
	}
	dirs := append(append([]string{}, coreTabDirs...), "shared")
	for _, dir := range dirs {
		pages, err := fs.Glob(fsys, "templates/"+dir+"/*.html")
		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			rel := strings.TrimPrefix(page, "templates/")
			switch rel {
			case "shared/layout.html", "shared/partials.html":
				continue
			case "shared/login.html":
				if err := add("login", nil, page); err != nil {
					return nil, err
				}
				continue
			}
			if standalone[rel] {
				continue
			}
			if err := add("layout:"+rel, core, "templates/shared/layout.html", "templates/shared/partials.html", page); err != nil {
				return nil, err
			}
		}
	}
	pages, err := fs.Glob(fsys, "templates/records/*.html")
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		base := strings.TrimPrefix(page, "templates/records/")
		if base == "record_print.html" {
			if err := add("recordsprint:"+base, records, page); err != nil {
				return nil, err
			}
			continue
		}
		if err := add("records:"+base, records, "templates/shared/layout.html", page); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// loadTemplates parses all page templates into h.tmpls; call once at startup.
func (h *Handler) loadTemplates() error {
	m, err := parseTemplates(h.tmplFS)
	if err != nil {
		return err
	}
	h.tmpls = m
	return nil
}

// tmpl returns the pre-parsed template for key, or writes a 500 and returns nil.
func (h *Handler) tmpl(w http.ResponseWriter, key string) *template.Template {
	t := h.tmpls[key]
	if t == nil {
		serverError(w, "template not found", fmt.Errorf("%s", key))
	}
	return t
}
