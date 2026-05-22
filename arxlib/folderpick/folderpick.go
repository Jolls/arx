// Package folderpick opens a native Windows folder-picker dialog via PowerShell.
package folderpick

import (
	"os/exec"
	"strings"
)

// BrowseFolder opens a native Windows folder-picker dialog and returns the
// selected path. Returns an empty string on cancel or error.
func BrowseFolder() string {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Add-Type -AssemblyName System.Windows.Forms; `+
			`$d = New-Object System.Windows.Forms.FolderBrowserDialog; `+
			`$d.Description = 'Select folder'; `+
			`if ($d.ShowDialog() -eq 'OK') { $d.SelectedPath } else { '' }`).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
