package main

import (
	"strings"

	"arx/arx_go/models"
)

// evaluateHide returns true if the step should be hidden.
//
// Supported syntax:
//
//	""  or  "SHOW"       → always visible
//	"HIDE"               → always hidden
//	"{token}=value"      → hidden if token resolves to value (case-insensitive, trimmed)
//	"{token}!=value"     → hidden if token does NOT resolve to value (case-insensitive, trimmed)
//
// {token} supports the same refs as substituteRefs: {12} (step result), {record.type},
// {record.pn}, {record.sn}, {record.date}, {form.id}, {form.pn}, etc.
// Unresolvable tokens are left as-is, so comparisons won't match and the step is shown.
// Pass nil for record or results when that context isn't available.
func evaluateHide(formula string, results map[int]*models.TestResult, steps map[int]*models.TestStep, record *models.TestRecord, form *models.TestForm) bool {
	resolved := strings.TrimSpace(substituteRefs(formula, results, steps, record, form))
	switch {
	case resolved == "" || strings.EqualFold(resolved, "SHOW"):
		return false
	case strings.EqualFold(resolved, "HIDE"):
		return true
	}
	// Check != before = to avoid splitting on the = within !=.
	if idx := strings.Index(resolved, "!="); idx >= 0 {
		lhs := strings.TrimSpace(resolved[:idx])
		rhs := strings.TrimSpace(resolved[idx+2:])
		return !strings.EqualFold(lhs, rhs)
	}
	if idx := strings.Index(resolved, "="); idx >= 0 {
		lhs := strings.TrimSpace(resolved[:idx])
		rhs := strings.TrimSpace(resolved[idx+1:])
		return strings.EqualFold(lhs, rhs)
	}
	return false // unknown expression → show
}
