package models

import (
	"strconv"
	"strings"
	"time"
)

// TestForm is a row in the form table.
// PartNumber and Title are joined from the part_number table.
type TestForm struct {
	ID          int
	PNID        int
	Locked      bool
	Active      bool
	TestOrder   string // comma-separated test IDs in display order
	PartNumber  string // joined: PN.part_number
	Title       string // joined: PN.title
	RecordTypes     string // comma-separated allowed record types; empty = free-text
	InstrumentTypes string // comma-separated valid instrument types for this form; empty = free-text
	Revision        int    // number of times this form has been released (locked); 0 = never released ("Draft"). Bumped on unlock->lock, never on save (#260).
}

// OrderedTestIDs parses TestOrder into a slice of integer step IDs.
func (f *TestForm) OrderedTestIDs() []int {
	return parseIDList(f.TestOrder)
}

// InstrumentTypeList splits InstrumentTypes into individual options for template rendering.
// Returns nil when InstrumentTypes is empty (caller shows a free-text input instead).
func (f TestForm) InstrumentTypeList() []string {
	if f.InstrumentTypes == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(f.InstrumentTypes, ",") {
		if t = trim(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// RecordTypeList splits RecordTypes into individual options for template rendering.
// Returns nil when RecordTypes is empty (caller shows a free-text input instead).
func (f TestForm) RecordTypeList() []string {
	if f.RecordTypes == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(f.RecordTypes, ",") {
		if t = trim(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// TestRecord is a row in the test_record table.
type TestRecord struct {
	ID               int
	FormID           int
	PartNumberID     int        // FK to part_number.id; 0 if NULL
	SerialNumber     string // #214: migrate to INT once DB column is migrated from VARCHAR(64)
	SerialNumberPN   string     // part number of the unit under test
	SerialNumberDesc string     // description of the unit under test
	RecordDate       *time.Time
	Comments         string // used as "Type" in the UI
	InstrumentType   string // free-text instrument type label; matched against test_definition.instrument_types to filter steps
	Locked           bool
	Approved         bool // 1 = reviewer-approved; only a TR reviewer may unlock (#249). Requires Locked.
	Active           bool
	TestOrder        string // comma-separated snapshot of test IDs at record creation
	FormRevision     *int   // snapshot of form.Revision at record creation; nil for pre-#260 records or legacy data
	LotID            *int   // lot the tested unit belongs to (#677); nil when the part is not lot-tracked or unlinked
	BuildID          *int   // build that produced the tested unit (#677); nil when not built or unlinked
}

// OrderedTestIDs parses TestOrder into a slice of integer step IDs.
func (r *TestRecord) OrderedTestIDs() []int {
	return parseIDList(r.TestOrder)
}

// FormRevLabel is the display label for the captured form revision: "" when unknown
// (nil — pre-#260 or legacy), "Draft" when captured against an unreleased form (0),
// or "Rev N" for a released revision (#260).
func (r TestRecord) FormRevLabel() string {
	if r.FormRevision == nil {
		return ""
	}
	if *r.FormRevision == 0 {
		return "Draft"
	}
	return "Rev " + strconv.Itoa(*r.FormRevision)
}

// TestStep is a row in the test_definition table.
// Type is the heading level: 0 = data row, 1/2/3 = section heading.
type TestStep struct {
	ID            int
	FormID        int
	Parameter     string
	Specification string
	DefaultResult string
	HideFormula   string
	Type          int    // 0=data, 1=Heading1, 2=Heading2, 3=Heading3
	Archived      bool   // true = retired step; hidden from new records and live def view, kept on historical records that recorded a result
	SpecMin       string // acceptance window lower bound (VARCHAR in DB)
	SpecMax       string // acceptance window upper bound (VARCHAR in DB)
	PFType        string // evaluator type: 'range' or empty = range check

	// debug fields still needed for form-def authoring — see FUTURE_GOALS.md (debug fields cleanup)
	ArchiveID        int
	Revision         int
	Category         string
	SheetName        string
	SpecUnits        string
	SpecNom          string
	InstrumentTypes  string
	Format           string
	StepComment      string
	StepCreatedAt    *time.Time
	StepUpdatedAt    *time.Time
}

// TestResult is a row in the test_result table.
// The snapshot fields (Parameter, Specification, SpecMin/Nom/Max, SpecUnits, PFType, Format)
// freeze the definition as it was when the result was recorded (#487). Stored resolved.
type TestResult struct {
	ID            int
	RecordID      int
	TestID        int
	Parameter     string     // snapshot of parameter at commit time (resolved)
	Specification string     // snapshot (resolved)
	Result        string
	PassFail      *bool
	Comment       string
	SpecMin       string // snapshot
	SpecNom       string // snapshot
	SpecMax       string // snapshot
	SpecUnits     string // snapshot
	PFType        string // snapshot of pf_type — saved records evaluate P/F against this
	Format        string // snapshot of format — controls how the recorded value renders
	Type          int    // snapshot of test_definition.type — 0=data, 1/2/3=heading
	HideFormula   string // snapshot of hide_formula — frozen visibility, evaluated vs the record's own results
	DefaultResult string // snapshot of default_result — used by the edit page for auto-calc; not shown on the view
	UpdatedAt     *time.Time
}

// HasSnapshot reports whether this result carries frozen definition fields (a record
// saved after #487). Old records have empty snapshot columns and fall back to the live def.
func (r *TestResult) HasSnapshot() bool {
	return r != nil && (r.PFType != "" || r.SpecMin != "" || r.SpecMax != "" ||
		r.SpecNom != "" || r.SpecUnits != "" || r.Format != "")
}

// ResultRow pairs a step definition with its recorded result for template rendering.
type ResultRow struct {
	Step        *TestStep
	Result      *TestResult // nil if no result recorded for this step
	Level       int         // mirrors Step.Type; 0=data, 1/2/3=heading
	RawSpecNom  string      // spec_nom with {record.X} resolved but {id} tokens kept — edit view only
	RawDefault  string      // default_result with {record.X} resolved but {id} tokens kept — edit view only
	Hidden      bool        // hide_formula currently evaluates to hidden — edit view renders it display:none for live toggling
	QueryDescription string // description of the named query backing this row's spec_nom, if any — edit view tooltip
}

// EffectiveParameter returns the result snapshot if available, else the step definition.
func (r ResultRow) EffectiveParameter() string {
	if r.Result != nil && r.Result.Parameter != "" {
		return r.Result.Parameter
	}
	if r.Step != nil {
		return r.Step.Parameter
	}
	return ""
}

// EffectiveSpec returns the result snapshot spec if available, else the step definition.
func (r ResultRow) EffectiveSpec() string {
	if r.Result != nil && r.Result.Specification != "" {
		return r.Result.Specification
	}
	if r.Step != nil {
		return r.Step.Specification
	}
	return ""
}

// FormEvent is a row in the form_events table.
// Records state changes on a Form (locked, unlocked, archived, activated, etc.).
type FormEvent struct {
	ID        int
	FormID    int
	EventType string
	Username  string
	EventDate *time.Time
	Comments  string
}

// RecordEvent is a row in the record_events table.
// Records state changes on a TestRecord (locked, unlocked, archived, activated, etc.).
type RecordEvent struct {
	ID           int
	TestRecordID int
	EventType    string
	Username     string
	EventDate    *time.Time
	Comments     string
}

// RecordResultSnapshot is one captured result value within a Complete-event snapshot (#251).
// Stored in record_event_results, keyed to a record_events row.
type RecordResultSnapshot struct {
	EventID       int
	TestID        int
	Parameter     string
	Specification string // resolved spec snapshot, frozen at completion (tokens already baked in)
	SpecUnits     string
	Result        string
	PassFail      *bool
	Comment       string
}

// PF renders the snapshot's pass/fail as "PASS", "FAIL", or "" for templates.
func (s RecordResultSnapshot) PF() string {
	if s.PassFail == nil {
		return ""
	}
	if *s.PassFail {
		return "PASS"
	}
	return "FAIL"
}

// SnapshotDiffRow is a snapshot row annotated with how it changed vs. the prior snapshot:
// "added" (test_id absent from the prior snapshot), "changed" (value/pass-fail/comment
// differ), or "unchanged".
type SnapshotDiffRow struct {
	RecordResultSnapshot
	Status string
}

// EventSnapshot pairs a Complete event with its result snapshot, already diffed against
// the previous Complete snapshot, for the record detail view.
type EventSnapshot struct {
	EventID int
	Rows    []SnapshotDiffRow
}

// ComputePassFail evaluates pass/fail for a result value against a step's spec bounds.
// Returns nil if the result cannot be evaluated (empty, non-numeric, no bounds).
// pf_type values:
//   "filled" — PASS if non-empty, nil (MISSING) if blank
//   "attach" — PASS if non-empty (an image has been pasted), nil (MISSING) if blank
//   "range"  — numeric range check against spec_min / spec_max (default)
func ComputePassFail(value string, step *TestStep) *bool {
	if step == nil {
		return nil
	}
	val := strings.TrimSpace(value)

	switch strings.ToLower(step.PFType) {
	case "comment":
		t := true
		return &t
	case "filled", "attach":
		if val == "" {
			return nil
		}
		t := true
		return &t
	default: // range
		if val == "" {
			return nil
		}
		hasMin := step.SpecMin != ""
		hasMax := step.SpecMax != ""
		if !hasMin && !hasMax {
			return nil
		}
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil
		}
		pass := true
		if hasMin {
			if min, err := strconv.ParseFloat(strings.TrimSpace(step.SpecMin), 64); err == nil {
				pass = pass && f >= min
			}
		}
		if hasMax {
			if max, err := strconv.ParseFloat(strings.TrimSpace(step.SpecMax), 64); err == nil {
				pass = pass && f <= max
			}
		}
		return &pass
	}
}

// CalcPF returns "PASS", "FAIL", "MISSING", or "" for template rendering.
func (r ResultRow) CalcPF() string {
	if r.Level > 0 || r.Step == nil {
		return ""
	}
	val := strings.TrimSpace(r.EffectiveValue())
	pfType := strings.ToLower(r.Step.PFType)

	if pfType == "comment" {
		return "PASS"
	}

	// Show MISSING for filled/attach type with no value, or range type with bounds but no value
	if val == "" {
		if pfType == "filled" || pfType == "attach" {
			return "MISSING"
		}
		if r.Step.SpecMin != "" || r.Step.SpecMax != "" {
			return "MISSING"
		}
		return ""
	}

	pf := ComputePassFail(val, r.Step)
	if pf == nil {
		return ""
	}
	if *pf {
		return "PASS"
	}
	return "FAIL"
}

// ListOptions parses "List:opt1;opt2" spec_nom into individual options.
// Returns nil if spec_nom is not a List: value.
func (s *TestStep) ListOptions() []string {
	if !strings.HasPrefix(s.SpecNom, "List:") {
		return nil
	}
	return strings.Split(strings.TrimPrefix(s.SpecNom, "List:"), ";")
}

// EffectiveValue returns the recorded result value, or the step default if none.
func (r ResultRow) EffectiveValue() string {
	if r.Result != nil && r.Result.Result != "" {
		return r.Result.Result
	}
	if r.Step != nil {
		return r.Step.DefaultResult
	}
	return ""
}

func parseIDList(s string) []int {
	if s == "" {
		return nil
	}
	var ids []int
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			tok := trim(s[start:i])
			if tok != "" {
				n := 0
				for _, c := range tok {
					if c >= '0' && c <= '9' {
						n = n*10 + int(c-'0')
					} else {
						n = 0
						break
					}
				}
				if n > 0 {
					ids = append(ids, n)
				}
			}
			start = i + 1
		}
	}
	return ids
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
