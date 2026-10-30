package main

import "fmt"

// Column filter definitions for every list table that renders a filter row (#411).
// Pages call {{template "filter_row" (filterRow "name" dyn)}}; the markup lives
// once in templates/shared/layout.html.

type FilterOption struct {
	Value, Label string
	Selected     bool
}

// FilterCol describes one <th> of a filter row. Kind: "text" (the default input),
// "date" (engine-rendered date popover), "select" (static Options), "datalist"
// (text input suggesting FilterRow.Dyn strings), "dynselect" (select of
// FilterRow.Dyn part categories) or "none" (empty cell).
type FilterCol struct {
	Key     string
	Kind    string
	Aria    string // set: the Bootstrap form-control variant with this aria-label
	ID      string // select id
	ListID  string // datalist id
	Options []FilterOption
}

// FilterRow is the argument to the filter_row partial; Dyn feeds the datalist and
// dynselect columns, which are the only per-request content in a filter row.
type FilterRow struct {
	Cols []FilterCol
	Dyn  any
}

func ftext(key string) FilterCol     { return FilterCol{Key: key, Kind: "text"} }
func fbs(key, aria string) FilterCol { return FilterCol{Key: key, Kind: "text", Aria: aria} }
func fdate(key string) FilterCol     { return FilterCol{Key: key, Kind: "date"} }
func fnone(key string) FilterCol     { return FilterCol{Key: key, Kind: "none"} }
func fselect(key string, opts ...FilterOption) FilterCol {
	return FilterCol{Key: key, Kind: "select", Options: opts}
}

func fopts(pairs ...string) []FilterOption {
	out := make([]FilterOption, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, FilterOption{Value: pairs[i], Label: pairs[i+1]})
	}
	return out
}

// recordStatusSelect is the WIP/Complete/Approved status filter shared by the records tables.
func recordStatusSelect(key string) FilterCol {
	c := fselect(key, fopts("", "All", "wip", "WIP", "complete", "Complete", "approved", "Approved")...)
	c.Aria = "Status filter"
	return c
}

func recordTypeFilter(key, listID string) FilterCol {
	return FilterCol{Key: key, Kind: "datalist", ListID: listID, Aria: "Type filter"}
}

// scopedRecordCols is the column set of the part/lot/unit-scoped records tables.
func scopedRecordCols(typeListID string) []FilterCol {
	return []FilterCol{
		fbs("col-sn", "Filter serial number"), fbs("col-pn", "Filter part number"), fbs("col-desc", "Filter description"),
		fdate("col-date"), recordTypeFilter("col-type", typeListID), recordStatusSelect("col-status"),
		fnone("col-form-rev"), fbs("col-form", "Filter form"),
	}
}

var filterRows = map[string][]FilterCol{
	"suppliers": {ftext("col-name"), ftext("col-status"), ftext("col-country"), ftext("col-links"), ftext("col-pos"), ftext("col-contact"), ftext("col-stub")},
	"contacts": {ftext("col-supplier"), ftext("col-name"), ftext("col-email"), ftext("col-country"), ftext("col-state"), ftext("col-city"),
		ftext("col-phone"), ftext("col-web"), fdate("col-modified"), ftext("col-notes"), ftext("col-active")},
	"parts": {ftext("col-pn"), ftext("col-rev"), ftext("col-title"), ftext("col-detail"), ftext("col-reqby"), fdate("col-date"),
		{Key: "col-cat", Kind: "dynselect"}, fdate("col-modified"), ftext("col-attach"), ftext("col-polines")},
	"pos": {ftext("col-num"),
		fselect("col-status", fopts("", "All", "draft", "Draft", "open", "Open", "sent", "Sent", "partially_received", "Partially Received",
			"closed", "Closed", "cancelled", "Cancelled")...),
		ftext("col-vendor"), fdate("col-ordered"), fdate("col-closed"), ftext("col-orderer"), ftext("col-cost")},
	"rfqs": {ftext("col-num"), fselect("col-status", fopts("", "All", "open", "Open", "awarded", "Awarded", "declined", "Declined")...),
		ftext("col-quotes"), ftext("col-vendors"), fdate("col-requested"), ftext("col-cost")},
	"forms": {ftext("col-pn"), ftext("col-desc"), ftext("col-rev"), ftext("col-status")},
	"test_report": {fbs("col-sn", "Filter serial number"), fbs("col-pn", "Filter part number"), fdate("col-record-date"), fdate("col-result-date"),
		fbs("col-result", "Filter result"), fbs("col-pf", "Filter pass/fail"), fbs("col-comment", "Filter comment")},
	"records_index": {fnone("col-select"), fbs("col-sn", "Filter serial number"), fbs("col-pn", "Filter part number"),
		fbs("col-desc", "Filter description"), fdate("col-date"), recordTypeFilter("col-type", "type-options"),
		{Key: "col-status", Kind: "select", ID: "status-filter", Aria: "Status filter",
			Options: []FilterOption{{Value: "wip", Label: "WIP", Selected: true}, {Value: "complete", Label: "Complete"},
				{Value: "approved", Label: "Approved"}, {Value: "", Label: "All"}}},
		fnone("col-form-rev")},
	"part_records":    scopedRecordCols("type-options"),
	"part_lot_trace":  scopedRecordCols("lot-type-options"),
	"part_unit_trace": scopedRecordCols("unit-type-options"),
	"lots": {ftext("col-lot"), ftext("col-vendorlot"), ftext("col-lotdesc"), ftext("col-pn"), ftext("col-partdesc"), ftext("col-notes"),
		// "active*" is a whole-field glob: a plain "active" substring would also match "inactive".
		fdate("col-created"), fselect("col-status", fopts("", "All", "active*", "Active", "inactive", "Inactive")...)},
	"units": {ftext("col-sn"), ftext("col-pn"), ftext("col-partdesc"), ftext("col-lot"), fdate("col-created"),
		fselect("col-status", fopts("", "All", "active", "Active", "scrapped", "Scrapped")...)},
	"builds": {ftext("col-build"), ftext("col-pn"), ftext("col-partdesc"), ftext("col-qty"), fdate("col-date"), ftext("col-lot"), ftext("col-note")},
}

// filterRow returns the filter_row argument for a named table. dyn is the optional
// per-request list (record types, or part categories) for datalist/dynselect columns.
func filterRow(name string, dyn ...any) (FilterRow, error) {
	cols, ok := filterRows[name]
	if !ok {
		return FilterRow{}, fmt.Errorf("filterRow: unknown table %q", name)
	}
	r := FilterRow{Cols: cols}
	if len(dyn) > 0 {
		r.Dyn = dyn[0]
	}
	return r, nil
}
