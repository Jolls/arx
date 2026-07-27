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
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Add-Type -AssemblyName System.Windows.Forms; `+
			`$o = New-Object System.Windows.Forms.Form -Property @{TopMost=$true; ShowInTaskbar=$false; FormBorderStyle='None'; StartPosition='Manual'; Left=-32000; Top=-32000; Width=1; Height=1; Opacity=0}; `+
			`$o.Add_Shown({$o.Activate()}); `+
			`$o.Show() | Out-Null; `+
			`$d = New-Object System.Windows.Forms.FolderBrowserDialog; `+
			`$d.Description = 'Select folder'; `+
			`$r = $d.ShowDialog($o); `+
			`$o.Close(); `+
			`if ($r -eq 'OK') { $d.SelectedPath } else { '' }`)
	hideWindow(cmd)
	out, err := cmd.Output()
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
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Add-Type -AssemblyName System.Windows.Forms; `+
			`$o = New-Object System.Windows.Forms.Form -Property @{TopMost=$true; ShowInTaskbar=$false; FormBorderStyle='None'; StartPosition='Manual'; Left=-32000; Top=-32000; Width=1; Height=1; Opacity=0}; `+
			`$o.Add_Shown({$o.Activate()}); `+
			`$o.Show() | Out-Null; `+
			`$d = New-Object System.Windows.Forms.OpenFileDialog; `+
			`$d.Title = 'Select file'; `+
			`$r = $d.ShowDialog($o); `+
			`$o.Close(); `+
			`if ($r -eq 'OK') { $d.FileName } else { '' }`)
	hideWindow(cmd)
	out, err := cmd.Output()
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
