package db

import "strings"

var (
	likeBackslash = strings.NewReplacer(`\`, `\\`)
	likeAlone     = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
)

// EscapeLike prepares a user search string for LIKE/ILIKE ... ESCAPE '\'. `%` and `_` stay
// wildcards (multi- and single-character) so users can search "ABC%123" or "ABC_23", except when
// s is just one of them, which is searched as the literal character. Backslashes are always
// escaped, since they are the escape character.
func EscapeLike(s string) string {
	if s == "%" || s == "_" {
		return likeAlone.Replace(s)
	}
	return likeBackslash.Replace(s)
}
