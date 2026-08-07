# Plan: Add missing `rows.Err()` check to `SuppliersRows` (issue #862)

File: `arx_go/suppliers.go`

Insert the `rows.Err()` check immediately after the closing `}` of the `for rows.Next() { ... }` loop and before the `log.Printf` line, matching the exact pattern used in `arx_go/parts.go` (`PartsRows`) and `arx_go/pos.go` (`PORows`).

old_string:
```go
		out = append(out, s)
	}
	log.Printf("[rows] suppliers: %d rows in %v", len(out), time.Since(start))
```

new_string:
```go
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[rows] suppliers: %d rows in %v", len(out), time.Since(start))
```

This is the sole change required. No other files, imports, or tests need modification — `log`, `net/http` etc. are already imported in `arx_go/suppliers.go`.

No open questions — the fix, location, and exact formatting are fully determined by the established pattern in `arx_go/parts.go` and `arx_go/pos.go`.
