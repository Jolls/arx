package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// isSafeQuery rejects anything that isn't a plain SELECT.
// Defense-in-depth â€” the DB user should also be read-only.
func isSafeQuery(q string) bool {
	upper := strings.ToUpper(strings.TrimSpace(q))
	if !strings.HasPrefix(upper, "SELECT") {
		return false
	}
	for _, bad := range []string{";", "INSERT", "UPDATE", "DELETE", "DROP", "EXEC", "TRUNCATE", "ALTER", "CREATE"} {
		if strings.Contains(upper, bad) {
			return false
		}
	}
	return true
}

// parseQuerySpec parses "query:name(@param1=value1,@param2=value2)".
// The caller is responsible for resolving {id} tokens in specNom before calling this.
// Returns the query name and a map of param name â†’ value.
func parseQuerySpec(specNom string) (name string, params map[string]string) {
	s := strings.TrimPrefix(specNom, "query:")
	params = map[string]string{}

	parenIdx := strings.Index(s, "(")
	if parenIdx < 0 {
		return strings.TrimSpace(s), params
	}

	name = strings.TrimSpace(s[:parenIdx])
	paramStr := strings.Trim(s[parenIdx:], "()")

	for _, p := range strings.Split(paramStr, ",") {
		p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "@"))
		eqIdx := strings.Index(p, "=")
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(p[:eqIdx])
		val := strings.TrimSpace(p[eqIdx+1:])
		params[key] = val
	}
	return name, params
}

// QueryRow is one result row from a named query.
// Value is the stored key (col 1). Label is the human-readable display (col 2, or same as Value if absent).
type QueryRow struct {
	Value string
	Label string
}

// QueryResult holds the output of a named query execution.
type QueryResult struct {
	Rows       []QueryRow
	ResultType string // 'list' or 'single' â€” drives UI behavior in data entry
}

// Values returns the stored values as a semicolon-joined string (for read-only display).
func (qr QueryResult) Values() string {
	vals := make([]string, len(qr.Rows))
	for i, r := range qr.Rows {
		vals[i] = r.Value
	}
	return strings.Join(vals, ";")
}

// runNamedQuery fetches a named query from the named_queries table and executes it.
// specNom must already have {id} tokens substituted (call substituteRefs first).
func (h *Handler) runNamedQuery(ctx context.Context, specNom string) (QueryResult, error) {
	name, params := parseQuerySpec(specNom)
	if name == "" {
		return QueryResult{}, fmt.Errorf("empty query name in spec_nom %q", specNom)
	}

	var storedSQL, resultType string
	err := h.queryRowContext(ctx,
		fmt.Sprintf("SELECT sql, result_type FROM %s WHERE name = @p1 AND active = 1", h.cfg.NamedQueriesTable()),
		name,
	).Scan(&storedSQL, &resultType)
	if err == sql.ErrNoRows {
		return QueryResult{}, fmt.Errorf("named query %q not found", name)
	}
	if err != nil {
		return QueryResult{}, fmt.Errorf("named query lookup: %w", err)
	}

	if !isSafeQuery(storedSQL) {
		return QueryResult{}, fmt.Errorf("named query %q failed safety check", name)
	}

	args := make([]any, 0, len(params))
	for k, v := range params {
		args = append(args, sql.Named(k, v))
	}

	rows, err := h.queryContext(ctx, storedSQL, args...)
	if err != nil {
		return QueryResult{}, fmt.Errorf("named query %q execution: %w", name, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return QueryResult{}, fmt.Errorf("named query %q columns: %w", name, err)
	}
	multiCol := len(cols) >= 2

	result := QueryResult{ResultType: resultType}
	for rows.Next() {
		var val, label sql.NullString
		if multiCol {
			if err := rows.Scan(&val, &label); err != nil {
				continue
			}
		} else {
			if err := rows.Scan(&val); err != nil {
				continue
			}
			label = val
		}
		if !val.Valid || val.String == "" {
			continue
		}
		lbl := label.String
		if !label.Valid || lbl == "" {
			lbl = val.String
		}
		result.Rows = append(result.Rows, QueryRow{Value: val.String, Label: lbl})
		if resultType == "single" {
			break // first row only
		}
	}
	return result, nil
}

// APINamedQuery â€” GET /api/named-query?spec=query:name(@param=value)
// Returns JSON picker options for a named query with already-resolved parameters.
func (h *Handler) APINamedQuery(w http.ResponseWriter, r *http.Request) {
	spec := r.URL.Query().Get("spec")
	if !strings.HasPrefix(spec, "query:") {
		http.Error(w, "spec must start with query:", http.StatusBadRequest)
		return
	}
	qr, err := h.runNamedQuery(r.Context(), spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type rowJSON struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}
	type resp struct {
		ResultType string    `json:"result_type"`
		Rows       []rowJSON `json:"rows"`
	}
	out := resp{ResultType: qr.ResultType}
	for _, row := range qr.Rows {
		out.Rows = append(out.Rows, rowJSON{Value: row.Value, Label: row.Label})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
