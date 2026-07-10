package main

import (
	"fmt"
	"html/template"
	"net/http"
	"regexp"
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
		m["CSRFToken"] = h.csrfToken(w, r)
		m["CompanyLogo"] = h.companyLogoURL()
		m["AccentThemeClass"] = h.accentThemeClass()
		m["Title"] = "Arx: Test Records"
		m["Favicon"] = "/static/shared/icons/records.svg"
		m["FaviconType"] = "image/svg+xml"
		m["ExtraScript"] = "/static/records/app.js"
	}
	tmpl, err := template.New("").Funcs(trTemplateFuncs()).ParseFS(h.tmplFS,
		"templates/shared/layout.html",
		"templates/records/"+page,
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
		"templates/records/"+page,
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
			// sanitizeFileNamePart must match the folder name APIRecordPasteResultImage
			// writes to, or the link 404s for any part number with an illegal filename char.
			return "/images/" + sanitizeFileNamePart(partNumber) + "/" + val
		},
		// sanitizedPartNumber exposes the same folder-name sanitization to JS via a
		// data attribute, so client-side image URL building (paste_result_image.js)
		// matches the write path without needing a round-trip through the server.
		"sanitizedPartNumber": sanitizeFileNamePart,
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

// imageResultRe matches a test-record image filename: either the legacy
// dashed VBA shape (SN-..._rID-4..._tID-5...) or the new no-dash shape written
// by the Go paste feature (SN..._rID45_tID6...). "-?\d+" after RID/TID
// requires a digit run right after the token (with an optional dash before
// it), so free text that merely contains the substrings "_RID"/"_TID" without
// being followed by digits (e.g. "SNAPSHOT_RIDGE_TIDY") does not match.
var imageResultRe = regexp.MustCompile(`(?i)^SN[A-Z0-9-]*_RID-?\d+.*_TID-?\d+`)

// imageResult returns true for a result value that is a test-record image
// filename (see imageResultRe). Legacy values may lack a file extension
// (ServeImage auto-appends ".PNG"), so this stays pattern-based rather than
// checking urlutil.IsImage.
func imageResult(val string) bool {
	return imageResultRe.MatchString(val)
}

// isImageRow returns true for a Level-0 result row that holds a pasted
// image: either a pf_type="attach" step with a recorded value (the
// authoritative signal on the edit page), or a value matching the legacy/new
// image filename shape via imageResult (for rows whose test_definition
// predates the #587 migration to pf_type="attach"). Checking both keeps the
// Screenshots gallery (records.go's ImageRows) consistent with what
// record_edit.html shows as an image step.
func isImageRow(row models.ResultRow) bool {
	val := row.EffectiveValue()
	if val == "" {
		return false
	}
	if row.Step != nil && row.Step.PFType == "attach" {
		return true
	}
	return imageResult(val)
}
