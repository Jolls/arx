package models

import (
	"strconv"
	"strings"
	"time"
)

// TestForm is a row in the Forms table.
// PartNumber and Title are joined from the PN table.
type TestForm struct {
	ID          int
	PNID        int
	Locked      bool
	Active      bool
	TestOrder   string // comma-separated test IDs in display order
	PartNumber  string // joined: PN.part_number
	Title       string // joined: PN.title
	RecordTypes string // comma-separated allowed record types; empty = free-text
}

// OrderedTestIDs parses TestOrder into a slice of integer step IDs.
func (f *TestForm) OrderedTestIDs() []int {
	return parseIDList(f.TestOrder)
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

// TestRecord is a row in the TestRecords table.
type TestRecord struct {
	ID               int
	FormID           int
	SerialNumber     string // TODO: change to int once DB column is migrated from VARCHAR(64)
	SerialNumberPN   string     // part number of the unit under test
	SerialNumberDesc string     // description of the unit under test
	RecordDate       *time.Time
	Comments         string // used as "Type" in the UI
	Locked           bool
	Active           bool
	TestOrder        string // comma-separated snapshot of test IDs at record creation
}

// OrderedTestIDs parses TestOrder into a slice of integer step IDs.
func (r *TestRecord) OrderedTestIDs() []int {
	return parseIDList(r.TestOrder)
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
	SpecMin       string // acceptance window lower bound (VARCHAR in DB)
	SpecMax       string // acceptance window upper bound (VARCHAR in DB)
	PFType        string // evaluator type: 'range' or empty = range check

	// TODO: debug fields — remove before shipping edit UI
	ArchiveID        int
	Revision         int
	Category         string
	SheetName        string
	SpecUnits        string
	SpecNom          string
	PFFormula        string
	ApplicableInstrs string
	Format           string
	StepComment      string
	StepCreatedAt    *time.Time
	StepUpdatedAt    *time.Time
}

// TestResult is a row in the TestResults table.
type TestResult struct {
	ID            int
	RecordID      int
	TestID        int
	Parameter     string     // snapshot of parameter at commit time
	Specification string     // snapshot
	Result        string
	PassFail      *bool
	Comment       string
	UpdatedAt     *time.Time
}

// ResultRow pairs a step definition with its recorded result for template rendering.
type ResultRow struct {
	Step        *TestStep
	Result      *TestResult // nil if no result recorded for this step
	Level       int         // mirrors Step.Type; 0=data, 1/2/3=heading
	RawSpecNom  string      // spec_nom with {record.X} resolved but {id} tokens kept — edit view only
	RawDefault  string      // default_result with {record.X} resolved but {id} tokens kept — edit view only
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

// ComputePassFail evaluates pass/fail for a result value against a step's spec bounds.
// Returns nil if the result cannot be evaluated (empty, non-numeric, no bounds).
// pf_type values:
//   "filled" — PASS if non-empty, nil (MISSING) if blank
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
	case "filled":
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

	// Show MISSING for filled type with no value, or range type with bounds but no value
	if val == "" {
		if pfType == "filled" {
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
