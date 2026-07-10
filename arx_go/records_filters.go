package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// recordFilters holds the parsed, validated filter selections from the
// records-list query string. The zero value with Status defaulted to "wip"
// (as parseRecordFilters always sets) is the historic default WIP-only view.
type recordFilters struct {
	Status  string    // "wip" | "complete" | "approved" | "all"
	Type    string    // exact match on test_record.comments; "" = no filter
	From    time.Time // record_date lower bound; zero = no filter
	To      time.Time // record_date upper bound (inclusive day); zero = no filter
	FromStr string    // original YYYY-MM-DD input, for repopulating the form
	ToStr   string    // original YYYY-MM-DD input, for repopulating the form
}

const recordFilterDateLayout = "2006-01-02"

// parseRecordFilters reads the filter params from a records-list query string.
// Missing or unrecognized status falls back to "wip" (preserving the historic
// default view). Unparseable dates are ignored (and their *Str cleared).
func parseRecordFilters(q url.Values) recordFilters {
	f := recordFilters{
		Status: q.Get("status"),
		Type:   q.Get("type"),
	}
	switch f.Status {
	case "wip", "complete", "approved", "all":
		// valid as-is
	default:
		f.Status = "wip"
	}

	f.FromStr = q.Get("from")
	if t, err := time.Parse(recordFilterDateLayout, f.FromStr); err == nil {
		f.From = t
	} else {
		f.FromStr = ""
	}

	f.ToStr = q.Get("to")
	if t, err := time.Parse(recordFilterDateLayout, f.ToStr); err == nil {
		f.To = t
	} else {
		f.ToStr = ""
	}

	return f
}

// StatusWIP reports whether the WIP status filter is active. The records list
// only shows the row-select column and bulk-lock toolbar in this mode.
func (f recordFilters) StatusWIP() bool { return f.Status == "wip" }

// whereClauses builds the SQL WHERE fragments and positional args implied by the
// filters. Each fragment begins with " AND " so the caller can concatenate it
// onto an existing WHERE. Placeholders are numbered starting at startArg; the
// caller is responsible for @p1..@p(startArg-1) (formID is @p1, so pass 2).
func (f recordFilters) whereClauses(startArg int) (string, []any) {
	var sb strings.Builder
	var args []any
	n := startArg

	switch f.Status {
	case "wip":
		sb.WriteString(" AND is_locked = 0")
	case "complete":
		sb.WriteString(" AND is_locked = 1 AND is_approved = 0")
	case "approved":
		sb.WriteString(" AND is_approved = 1")
	case "all":
		// no clause
	}

	if f.Type != "" {
		fmt.Fprintf(&sb, " AND comments = @p%d", n)
		args = append(args, f.Type)
		n++
	}

	dateClause, dateArgs := f.dateRangeClauses(n)
	sb.WriteString(dateClause)
	args = append(args, dateArgs...)

	return sb.String(), args
}

// dateRangeClauses builds just the From/To record_date WHERE fragments (no
// status/type clauses), for callers that only want the date-range portion of
// recordFilters — e.g. a report scoped by a different set of base predicates.
// Placeholders are numbered starting at startArg; see whereClauses for the
// same startArg convention.
func (f recordFilters) dateRangeClauses(startArg int) (string, []any) {
	var sb strings.Builder
	var args []any
	n := startArg

	if !f.From.IsZero() {
		fmt.Fprintf(&sb, " AND record_date >= @p%d", n)
		args = append(args, f.From)
		n++
	}
	if !f.To.IsZero() {
		fmt.Fprintf(&sb, " AND record_date < DATEADD(day, 1, @p%d)", n)
		args = append(args, f.To)
		n++
	}

	return sb.String(), args
}
