// Package urlutil provides URL and file-path helpers for Arx.
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

// IsAbsPath reports whether val is an absolute local path: a UNC path (\\server\...),
// a drive-letter path (C:\... or C:/...), or a file:// URL. These render as copy-path
// only; the server never streams them.
func IsAbsPath(val string) bool {
	if strings.HasPrefix(val, "file://") || strings.HasPrefix(val, "\\\\") {
		return true
	}
	// drive letter: X:\ or X:/
	return len(val) >= 3 && val[1] == ':' && (val[2] == '\\' || val[2] == '/') &&
		((val[0] >= 'A' && val[0] <= 'Z') || (val[0] >= 'a' && val[0] <= 'z'))
}

// StripLocalPrefix returns val with a leading LOCAL: prefix (case-insensitive) removed.
// If val has no LOCAL: prefix it is returned unchanged.
func StripLocalPrefix(val string) string {
	if IsLocalFile(val) {
		return val[6:]
	}
	return val
}

// NormalizeLink canonicalizes a user-entered attachment link for storage:
//   - trims surrounding whitespace; empty stays empty
//   - http(s):// URLs and absolute paths (UNC/drive/file://) are stored verbatim
//   - a LOCAL: prefix (any case) is re-emitted with an uppercase LOCAL: and the original
//     remainder (trailing \ or / preserved so directory-ness survives)
//   - any other bare relative token is assumed doc-control and gets a LOCAL: prefix
func NormalizeLink(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if IsHTTPURL(v) || IsAbsPath(v) {
		return v
	}
	if IsLocalFile(v) {
		return "LOCAL:" + v[6:]
	}
	return "LOCAL:" + v
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
// A leading LOCAL: prefix is stripped first so a doc-control file without a
// subdirectory (e.g. "LOCAL:spec.pdf") yields "spec.pdf", not "LOCAL:spec.pdf".
func FileBaseName(val string) string {
	clean := strings.ReplaceAll(StripLocalPrefix(val), "\\", "/")
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

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// IsImage reports whether filename has a displayable image extension (case-insensitive).
func IsImage(filename string) bool {
	return imageExts[strings.ToLower(filepath.Ext(filename))]
}
