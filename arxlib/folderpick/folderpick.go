// Package folderpick opens a native Windows folder-picker dialog via PowerShell.
package folderpick

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// BrowseFolderContext opens a folder picker; ctx controls cancellation and timeout.
// Returns an empty string on cancel, timeout, or error.
func BrowseFolderContext(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Add-Type -AssemblyName System.Windows.Forms; `+
			`$d = New-Object System.Windows.Forms.FolderBrowserDialog; `+
			`$d.Description = 'Select folder'; `+
			`if ($d.ShowDialog() -eq 'OK') { $d.SelectedPath } else { '' }`).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// BrowseFolder opens a folder picker with a 5-minute timeout.
func BrowseFolder() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return BrowseFolderContext(ctx)
}

// BrowseFileContext opens a native file picker; ctx controls cancellation and
// timeout. Returns the selected absolute path, or an empty string on cancel,
// timeout, or error.
func BrowseFileContext(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Add-Type -AssemblyName System.Windows.Forms; `+
			`$d = New-Object System.Windows.Forms.OpenFileDialog; `+
			`$d.Title = 'Select file'; `+
			`if ($d.ShowDialog() -eq 'OK') { $d.FileName } else { '' }`).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// BrowseFile opens a file picker with a 5-minute timeout.
func BrowseFile() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return BrowseFileContext(ctx)
}
