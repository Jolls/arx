package config

import (
	"os"
	"regexp"
	"strings"
)

// userProfileToken replaces the current user's profile directory in stored
// folder-root paths so config/local.json (shared across users when Arx.exe
// runs from a shared OneDrive folder) doesn't hardcode one user's path (#731).
const userProfileToken = "%USERPROFILE%"

var multiBackslash = regexp.MustCompile(`\\{2,}`)

// normalizeSeparators converts forward slashes to backslashes and collapses
// doubled/repeated backslashes to one, so paths saved with different styles
// (folder-picker backslashes, forward slashes from .env or manual entry,
// accidentally double-escaped legacy entries) compare and match consistently.
// A leading UNC "\\server\share" double-backslash is preserved.
func normalizeSeparators(path string) string {
	p := strings.ReplaceAll(path, "/", `\`)
	if strings.HasPrefix(p, `\\`) {
		return `\\` + multiBackslash.ReplaceAllString(p[2:], `\`)
	}
	return multiBackslash.ReplaceAllString(p, `\`)
}

// TokenizeUserPath replaces a leading %USERPROFILE% (from os.Getenv) prefix
// with the literal token %USERPROFILE%, so the path stored in local.json
// resolves under whichever user's profile loads it. Paths outside the
// profile directory (e.g. C:\DocControl, \\server\share) are left unchanged.
// Separators are normalized before comparing so forward slashes or doubled
// backslashes still match the profile prefix.
func TokenizeUserPath(path string) string {
	profile := os.Getenv("USERPROFILE")
	if profile == "" || path == "" {
		return path
	}
	normPath := normalizeSeparators(path)
	normProfile := normalizeSeparators(profile)
	if len(normPath) < len(normProfile) {
		return path
	}
	if strings.EqualFold(normPath[:len(normProfile)], normProfile) {
		return userProfileToken + normPath[len(normProfile):]
	}
	return path
}

// ExpandUserPath replaces a leading %USERPROFILE% token with the current
// user's profile directory. A path with no token, or an unresolvable token
// (USERPROFILE unset), is returned unchanged so a bad value surfaces
// visibly rather than collapsing to a relative path.
func ExpandUserPath(path string) string {
	if len(path) < len(userProfileToken) || !strings.EqualFold(path[:len(userProfileToken)], userProfileToken) {
		return path
	}
	profile := os.Getenv("USERPROFILE")
	if profile == "" {
		return path
	}
	return profile + path[len(userProfileToken):]
}
