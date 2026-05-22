// Package urlutil provides URL and file-path helpers shared across Arx apps.
// All functions are pure (no side effects) and safe to call from templates.
package urlutil

import (
	"path/filepath"
	"strings"
)

// IsHTTPURL reports whether val is an HTTP or HTTPS URL.
func IsHTTPURL(val string) bool {
	return strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://")
}

// IsLocalFile reports whether val is a LOCAL: file reference (case-insensitive prefix).
func IsLocalFile(val string) bool {
	return strings.HasPrefix(strings.ToUpper(val), "LOCAL:")
}

// IsLocalDir reports whether val is a LOCAL: reference that points to a directory
// (trailing / or \).
func IsLocalDir(val string) bool {
	if !IsLocalFile(val) {
		return false
	}
	stripped := val[6:]
	return strings.HasSuffix(stripped, "/") || strings.HasSuffix(stripped, "\\")
}

// LocalFileURL converts a LOCAL:path value to a URL using the given prefix.
//
//	LocalFileURL("LOCAL:foo\\bar.pdf", "/local/") → "/local/foo/bar.pdf"
func LocalFileURL(val, urlPrefix string) string {
	rel := strings.ReplaceAll(val[6:], "\\", "/")
	rel = strings.TrimPrefix(rel, "/")
	return urlPrefix + rel
}

// LocalDirURL converts a LOCAL:path/ value to a URL using the given prefix.
//
//	LocalDirURL("LOCAL:foo\\", "/local-dir/") → "/local-dir/foo"
func LocalDirURL(val, urlPrefix string) string {
	rel := strings.ReplaceAll(val[6:], "\\", "/")
	rel = strings.Trim(rel, "/")
	return urlPrefix + rel
}

// FileBaseName returns the base file name from a path, handling both / and \.
func FileBaseName(val string) string {
	clean := strings.ReplaceAll(val, "\\", "/")
	return filepath.Base(clean)
}

// FileIcon returns a Bootstrap icon class for the given filename extension.
func FileIcon(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".pdf":
		return "bi-file-pdf text-danger"
	case ".doc", ".docx":
		return "bi-file-word text-primary"
	case ".xls", ".xlsx":
		return "bi-file-excel text-success"
	default:
		return "bi-file-earmark"
	}
}

// IsPDF reports whether filename has a .pdf extension (case-insensitive).
func IsPDF(filename string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".pdf"
}

// SafePathSegments splits rawPath on "/" and cleans each segment with
// filepath.Base to block directory traversal. Empty segments are dropped.
// Use this before joining with a root directory when serving user-supplied paths.
func SafePathSegments(rawPath string) []string {
	parts := strings.Split(rawPath, "/")
	safe := make([]string, 0, len(parts))
	for _, seg := range parts {
		cleaned := filepath.Base(seg)
		if cleaned == "" || cleaned == "." || cleaned == ".." {
			continue
		}
		safe = append(safe, cleaned)
	}
	return safe
}
