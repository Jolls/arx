package db

import (
	"fmt"
	"strings"
)

func isParamNameByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// scanNamedParams calls fn for each @name token in query outside '...' string
// literals, "..." quoted identifiers, -- and /* */ comments, and @@name tokens;
// fn returns the replacement text. Postgres $tag$ dollar-quoting is not
// recognized (#28).
func scanNamedParams(query string, fn func(name string) string) string {
	var sb strings.Builder
	for i := 0; i < len(query); {
		c := query[i]
		end := i + 1
		switch {
		case c == '\'' || c == '"':
			for end < len(query) {
				if query[end] == c {
					if end+1 < len(query) && query[end+1] == c { // doubled quote escape
						end += 2
						continue
					}
					end++
					break
				}
				end++
			}
		case c == '-' && strings.HasPrefix(query[i:], "--"):
			if nl := strings.IndexByte(query[i:], '\n'); nl >= 0 {
				end = i + nl
			} else {
				end = len(query)
			}
		case c == '/' && strings.HasPrefix(query[i:], "/*"):
			if cl := strings.Index(query[i+2:], "*/"); cl >= 0 {
				end = i + 2 + cl + 2
			} else {
				end = len(query)
			}
		case c == '@':
			if strings.HasPrefix(query[i:], "@@") {
				for end = i + 2; end < len(query) && isParamNameByte(query[end]); end++ {
				}
				break
			}
			for end < len(query) && isParamNameByte(query[end]) {
				end++
			}
			if end > i+1 {
				sb.WriteString(fn(query[i+1 : end]))
				i = end
				continue
			}
		}
		sb.WriteString(query[i:end])
		i = end
	}
	return sb.String()
}

// NamedParamRefs returns the distinct @name tokens in query (per
// scanNamedParams' rules), in order of first appearance.
func NamedParamRefs(query string) []string {
	var names []string
	seen := map[string]bool{}
	scanNamedParams(query, func(name string) string {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
		return ""
	})
	return names
}

// RewriteNamedParams rewrites @name placeholder tokens in query (stored
// named-query SQL text) to $<position> per each name's index in orderedNames —
// the distinct param names in the exact order the caller will bind args in —
// since Postgres has no @name placeholder syntax and binds positionally. A name
// with no entry in orderedNames is left as literal @name text, so a stale or
// renamed param still fails instead of silently matching.
func RewriteNamedParams(query string, orderedNames []string) string {
	pos := make(map[string]int, len(orderedNames))
	for i, name := range orderedNames {
		pos[name] = i + 1
	}
	return scanNamedParams(query, func(name string) string {
		if p, ok := pos[name]; ok {
			return fmt.Sprintf("$%d", p)
		}
		return "@" + name
	})
}
