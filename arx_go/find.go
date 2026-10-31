package main

import (
	"fmt"
	"net/http"
	"strings"

	"arx/internal/inventory"
)

// findRow is one pick-list row on the /find page (#416).
type findRow struct {
	Kind, Label, Number, PartNumber, PartDescription, Matched, URL string
}

// findURL is the page a find result opens: the lot or unit trace, or for a build its output lot's
// trace (else the part's Build tab, as the build links in the lists do).
func findURL(r inventory.FindResult) string {
	switch {
	case r.Kind == "unit":
		return fmt.Sprintf("/part/%d/units/%d", r.PartID, r.ID)
	case r.Kind == "lot":
		return fmt.Sprintf("/part/%d/lots/%d", r.PartID, r.ID)
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
		if r.Kind == "unit" {
			fields = append(fields, "Serial number")
		} else {
			fields = append(fields, "Lot number")
		}
	}
	return strings.Join(fields, ", ")
}

// Find — GET /find?q=. Exact lookup of a lot, unit or build by id, lot number or serial (#416):
// one match redirects to it, several list a pick list, none shows a message.
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
				rows[i] = findRow{Kind: kind, Label: fmt.Sprintf("%s %d", kind, x.ID), Number: x.Name,
					PartNumber: x.PartNumber, PartDescription: x.PartDescription, Matched: matchedLabel(x), URL: findURL(x)}
			}
			data["Results"] = rows
		}
	}
	h.render(w, r, "parts/find.html", data)
}
