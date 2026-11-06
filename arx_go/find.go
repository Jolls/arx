package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"arx/internal/inventory"
)

// findRow is one pick-list row on the /find page (#416).
type findRow struct {
	Kind, Label, Number, PartNumber, PartDescription, Matched, URL string
}

// findURL is the page a find result opens: the lot or unit trace, the part, PO, company or
// contact detail page, the owning part's Sourcing tab for an MPN or supplier PN match, or for a
// build its output lot's trace (else the part's Build tab, as the build links in the lists do).
func findURL(r inventory.FindResult) string {
	switch {
	case r.Kind == "unit":
		return fmt.Sprintf("/part/%d/units/%d", r.PartID, r.ID)
	case r.Kind == "lot":
		return fmt.Sprintf("/part/%d/lots/%d", r.PartID, r.ID)
	case r.Kind == "part":
		return fmt.Sprintf("/part/%d", r.ID)
	case r.Kind == "po":
		// po.id isn't user-facing (the /po/{id} route keys on po.number, which is free text, not
		// guaranteed to be URL-safe).
		return "/po/" + url.PathEscape(r.Name)
	case r.Kind == "company":
		return fmt.Sprintf("/supplier/%d", r.ID)
	case r.Kind == "contact":
		return fmt.Sprintf("/contact/%d", r.ID)
	case r.Kind == "mfg_part" || r.Kind == "supplier_part":
		return fmt.Sprintf("/part/%d/suppliers", r.PartID)
	case r.OutputLotID != 0:
		return fmt.Sprintf("/part/%d/lots/%d", r.PartID, r.OutputLotID)
	}
	return fmt.Sprintf("/part/%d/build", r.PartID)
}

// matchedLabel names the fields a result matched on.
func matchedLabel(r inventory.FindResult) string {
	var fields []string
	if r.MatchedID {
		fields = append(fields, "ID")
	}
	if r.MatchedName {
		switch r.Kind {
		case "unit":
			fields = append(fields, "Serial number")
		case "part":
			fields = append(fields, "Part number")
		case "po":
			fields = append(fields, "PO number")
		case "company":
			fields = append(fields, "Company name")
		case "contact":
			fields = append(fields, "Contact name")
		case "mfg_part":
			fields = append(fields, "Manufacturer part number")
		case "supplier_part":
			fields = append(fields, "Supplier part number")
		default:
			fields = append(fields, "Lot number")
		}
	}
	return strings.Join(fields, ", ")
}

// Find — GET /find?q=. Exact lookup of a lot, unit, build, part, PO, company, contact, MPN or
// supplier PN by id, lot number, serial, part number, PO number, company name, contact name,
// MPN or supplier PN (#416, #2): one match redirects to it, several list a pick list, none
// shows a message.
func (h *Handler) Find(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	data := map[string]any{"ActiveTab": "records", "TestMode": h.cfg().TestMode, "Q": q}
	if q != "" {
		res, err := h.inventory().FindRecords(r.Context(), q)
		if err != nil {
			h.renderError(w, r, "Error searching: "+err.Error())
			return
		}
		switch len(res) {
		case 0:
			data["NotFound"] = true
		case 1:
			http.Redirect(w, r, findURL(res[0]), http.StatusFound)
			return
		default:
			rows := make([]findRow, len(res))
			for i, x := range res {
				kind := strings.ToUpper(x.Kind[:1]) + x.Kind[1:]
				label := fmt.Sprintf("%s %d", kind, x.ID)
				switch x.Kind {
				case "po":
					// po.id isn't user-facing (the /po/{id} route keys on po.number); label with
					// the number the user actually searched for instead.
					kind = "PO"
					label = kind + " " + x.Name
				case "mfg_part":
					// Matches land on the owning part's Sourcing tab, not a page of their own.
					kind = "MPN"
					label = fmt.Sprintf("Part %d", x.ID)
				case "supplier_part":
					kind = "Supplier PN"
					label = fmt.Sprintf("Part %d", x.ID)
				}
				rows[i] = findRow{Kind: kind, Label: label, Number: x.Name,
					PartNumber: x.PartNumber, PartDescription: x.PartDescription, Matched: matchedLabel(x), URL: findURL(x)}
			}
			data["Results"] = rows
		}
	}
	h.render(w, r, "parts/find.html", data)
}
