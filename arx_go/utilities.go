package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"arx/internal/reports"
	"arx/internal/urlutil"
)

// utilCheck is one diagnostic on the Settings → Utilities page: a titled group of
// offending rows. Count == 0 means the check passed. All checks are read-only.
type utilCheck struct {
	Title string
	Desc  string
	Count int
	Rows  []utilRow
}

// utilRow is one flagged item: a clickable label and a human-readable problem.
type utilRow struct {
	Label  string // primary identifier (part number, PO number, company name)
	Detail string // what's wrong
	URL    string // link to the owning record's detail page ("" = no link)
}

// UtilitiesReport renders the read-only data-integrity dashboard (GET
// /settings/utilities). Each check runs independently; a failing check is
// reported without aborting the others.
func (h *Handler) UtilitiesReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var checks []utilCheck
	var errs []string

	add := func(c utilCheck, err error) {
		if err != nil {
			errs = append(errs, c.Title+": "+err.Error())
			return
		}
		checks = append(checks, c)
	}
	add(h.checkDeadLinks(ctx))
	add(h.checkOrphanPointers(ctx))
	add(h.checkSoftDeletedAttachmentPointers(ctx))
	add(h.checkPOActiveDrift(ctx))

	h.render(w, r, "settings/utilities.html", map[string]any{
		"ActiveTab": "settings",
		"TestMode":  h.cfg().TestMode,
		"Checks":    checks,
		"Errors":    errs,
	})
}

// deadLocalTarget reports whether a LOCAL: attachment link points at a file or
// directory that is missing (or the wrong type) under root. It returns a reason
// and true when the target is dead. http links, absolute paths, and empty values
// are not checked (dead == false). A type mismatch (file link → folder, or vice
// versa) is flagged because it fails to serve at runtime the same way a missing
// file does (see ServeLocalFile / ServeLocalDir in files.go).
func deadLocalTarget(root, link string) (reason string, dead bool) {
	if root == "" || !urlutil.IsLocalFile(link) {
		return "", false
	}
	rel := strings.ReplaceAll(urlutil.StripLocalPrefix(link), "\\", "/")
	path, ok := safePath(root, rel)
	if !ok {
		return "unresolvable path: " + link, true
	}
	info, err := os.Stat(path)
	wantDir := urlutil.IsLocalDir(link)
	switch {
	case os.IsNotExist(err):
		return "missing on disk: " + link, true
	case err != nil:
		return "unreadable: " + link, true
	case wantDir && !info.IsDir():
		return "link is a folder but target is a file: " + link, true
	case !wantDir && info.IsDir():
		return "link is a file but target is a folder: " + link, true
	}
	return "", false
}

// checkDeadLinks flags active attachments whose LOCAL: file/folder is missing.
// Part attachments resolve against DOC_CONTROL_ROOT; company attachments against
// SUPPLIER_FILES_ROOT (falling back to DOC_CONTROL_ROOT), mirroring how the app
// serves each (see ServeLocalFile vs ServeSupplierFile).
func (h *Handler) checkDeadLinks(ctx context.Context) (utilCheck, error) {
	check := utilCheck{
		Title: "Dead attachment file links",
		Desc:  "Active attachments whose LOCAL: file or folder no longer exists on disk. External http(s) links are not checked.",
	}

	// scan flags rows whose LOCAL: target is dead under root, linking each to urlPrefix+id.
	scan := func(links []reports.AttachmentLink, root, urlPrefix string) {
		for _, l := range links {
			if reason, dead := deadLocalTarget(root, l.Link); dead {
				check.Rows = append(check.Rows, utilRow{
					Label:  l.Label,
					Detail: reason,
					URL:    urlPrefix + strconv.Itoa(l.ID),
				})
			}
		}
	}

	// Part attachments resolve against DOC_CONTROL_ROOT.
	partLinks, err := h.reports().PartAttachmentLinks(ctx)
	if err != nil {
		return check, err
	}
	scan(partLinks, h.cfg().DocControlRoot, "/part/")

	// Company attachments resolve against SUPPLIER_FILES_ROOT (fallback DOC_CONTROL_ROOT).
	supplierRoot := h.cfg().SupplierFilesRoot
	if supplierRoot == "" {
		supplierRoot = h.cfg().DocControlRoot
	}
	compLinks, err := h.reports().CompanyAttachmentLinks(ctx)
	if err != nil {
		return check, err
	}
	scan(compLinks, supplierRoot, "/supplier/")

	check.Count = len(check.Rows)
	return check, nil
}

// checkOrphanPointers flags parts whose soft-FK pointer references a row that no
// longer exists. Unset values are NULL; the `> 0` guard excludes them since
// `NULL > 0` is never true.
func (h *Handler) checkOrphanPointers(ctx context.Context) (utilCheck, error) {
	check := utilCheck{
		Title: "Orphaned part pointers",
		Desc:  "Parts whose supplier / price / primary-attachment pointer references a row that no longer exists.",
	}
	rpt := h.reports()
	specs := []struct {
		column, target string
		list           func(context.Context) ([]reports.Orphan, error)
	}{
		{"default_supplier_id", "company", rpt.OrphanDefaultSuppliers},
		{"price_id", "price", rpt.OrphanPrices},
		{"primary_attachment_id", "part_attachment", rpt.OrphanPrimaryAttachments},
	}
	for _, s := range specs {
		orphans, err := s.list(ctx)
		if err != nil {
			return check, err
		}
		for _, o := range orphans {
			check.Rows = append(check.Rows, utilRow{
				Label:  o.PartNumber,
				Detail: fmt.Sprintf("%s → missing %s #%d", s.column, s.target, o.Value),
				URL:    fmt.Sprintf("/part/%d", o.ID),
			})
		}
	}
	check.Count = len(check.Rows)
	return check, nil
}

// checkSoftDeletedAttachmentPointers flags parts/companies whose primary
// attachment points at an attachment row that still exists but is soft-deleted
// (is_active = 0). Distinct from checkOrphanPointers, which catches rows that are
// gone entirely; the enforced FK on company.primary_attachment_id prevents the
// "gone entirely" case there but not this one.
func (h *Handler) checkSoftDeletedAttachmentPointers(ctx context.Context) (utilCheck, error) {
	check := utilCheck{
		Title: "Primary attachment points at a deleted attachment",
		Desc:  "Parts or companies whose primary attachment references an attachment that has been soft-deleted (is_active = 0).",
	}

	// flag flags every row, linking each to urlPrefix+id.
	flag := func(owners []reports.Labeled, urlPrefix string) {
		for _, o := range owners {
			check.Rows = append(check.Rows, utilRow{
				Label:  o.Label,
				Detail: "primary attachment is soft-deleted",
				URL:    urlPrefix + strconv.Itoa(o.ID),
			})
		}
	}

	parts, err := h.reports().PartsWithDeletedPrimary(ctx)
	if err != nil {
		return check, err
	}
	flag(parts, "/part/")

	companies, err := h.reports().CompaniesWithDeletedPrimary(ctx)
	if err != nil {
		return check, err
	}
	flag(companies, "/supplier/")

	check.Count = len(check.Rows)
	return check, nil
}

// checkPOActiveDrift flags POs whose is_active flag disagrees with what status
// implies. status is authoritative; is_active is an app-maintained convenience
// bit. Reuses statusIsActive so the expected mapping stays in one place.
func (h *Handler) checkPOActiveDrift(ctx context.Context) (utilCheck, error) {
	check := utilCheck{
		Title: "Purchase order is_active drift",
		Desc:  "POs whose is_active flag disagrees with their status. status is authoritative; is_active should be kept in sync by the app.",
	}
	pos, err := h.reports().POActiveStates(ctx)
	if err != nil {
		return check, err
	}
	for _, po := range pos {
		if want := statusIsActive(po.Status); po.IsActive != want {
			check.Rows = append(check.Rows, utilRow{
				Label:  po.Number,
				Detail: fmt.Sprintf("status %q implies is_active=%v, but stored value is %v", po.Status, want, po.IsActive),
				URL:    fmt.Sprintf("/po/%d", po.ID),
			})
		}
	}
	check.Count = len(check.Rows)
	return check, nil
}
