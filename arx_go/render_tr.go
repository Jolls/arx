package main

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"arx/arxlib/urlutil"
	"arx/arx_go/models"
)

// renderTR renders a Test Records page using TR's layout and template funcs.
func (h *Handler) renderTR(w http.ResponseWriter, r *http.Request, page string, data any) {
	if m, ok := data.(map[string]any); ok {
		m["AppVersion"] = h.cfg.Version
		m["SchemaMismatch"] = h.schemaMismatch
		m["PartsMasterURL"] = h.cfg.PartsMasterURL
		m["CurrentUser"] = h.currentUser(r)
	}
	tmpl, err := template.New("").Funcs(trTemplateFuncs()).ParseFS(h.tmplFS,
		"templates/tr/layout.html",
		"templates/tr/"+page,
	)
	if err != nil {
		http.Error(w, "template parse error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "template execute error: "+err.Error(), http.StatusInternalServerError)
	}
}

// renderPrintTR renders a standalone TR print template (no layout wrapper).
func (h *Handler) renderPrintTR(w http.ResponseWriter, page string, data any) {
	tmpl, err := template.New("").Funcs(trTemplateFuncs()).ParseFS(h.tmplFS,
		"templates/tr/"+page,
	)
	if err != nil {
		http.Error(w, "template parse error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, page, data); err != nil {
		http.Error(w, "template execute error: "+err.Error(), http.StatusInternalServerError)
	}
}

// trTemplateFuncs returns the template function map for Test Records views.
func trTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"formatDate": trFormatDate,
		"formatDateInput": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("2006-01-02T15:04")
		},
		"formatDateTime": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("2006-01-02 15:04")
		},
		"formatDateTimeFull": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("2006-01-02 3:04 PM")
		},
		"isQuerySpec": func(s string) bool { return strings.HasPrefix(s, "query:") },
		"isHTTPURL":    urlutil.IsHTTPURL,
		"isLocalFile":  urlutil.IsLocalFile,
		"localFileURL": func(val string) string { return urlutil.LocalFileURL(val, "/local/") },
		"fileBaseName": urlutil.FileBaseName,
		"fileIcon":     urlutil.FileIcon,
		"isPDF":        urlutil.IsPDF,
		"imageResult":  imageResult,
		"imageURL": func(partNumber, val string) string {
			return "/images/" + partNumber + "/" + val
		},
		"pfBadge": func(res *models.TestResult) template.HTML {
			if res == nil {
				return `<span class="badge bg-secondary">—</span>`
			}
			if res.Result == "" {
				return `<span class="badge bg-warning text-dark">MISSING</span>`
			}
			if res.PassFail != nil && *res.PassFail {
				return `<span class="badge bg-success">PASS</span>`
			}
			return `<span class="badge bg-danger">FAIL</span>`
		},
		"applyFormat": func(val, format string) string {
			if val == "" {
				return val
			}
			f := strings.TrimSpace(format)
			if f == "" || f == "General" || f == "@" {
				return val
			}
			isPercent := strings.HasSuffix(f, "%")
			numFmt := strings.TrimSuffix(f, "%")
			if numFmt != "0" && !strings.HasPrefix(numFmt, "0.") {
				return val
			}
			decimals := 0
			if idx := strings.Index(numFmt, "."); idx >= 0 {
				decimals = len(numFmt) - idx - 1
			}
			v, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return val
			}
			if isPercent {
				v *= 100
			}
			result := fmt.Sprintf("%.*f", decimals, v)
			if isPercent {
				result += "%"
			}
			return result
		},
	}
}

func trFormatDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// imageResult returns true for VBA image filenames: SN-..._rID-..._tID-...
func imageResult(val string) bool {
	upper := strings.ToUpper(val)
	return strings.HasPrefix(upper, "SN-") &&
		strings.Contains(upper, "_RID-") &&
		strings.Contains(upper, "_TID-")
}
