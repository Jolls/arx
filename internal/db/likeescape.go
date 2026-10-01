package db

import "strings"

var likeGlob = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`, `*`, `%`)

// EscapeLike prepares a user search string for LIKE/ILIKE ... ESCAPE '\'. `*` is the one
// wildcard users get (any run of characters, as in the list-table filters); `%`, `_` and `\`
// match themselves.
func EscapeLike(s string) string {
	return likeGlob.Replace(s)
}
