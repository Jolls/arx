//go:build !windows

package main

// openDebugConsole is a no-op on non-Windows platforms; there is no
// separate console window to allocate.
func openDebugConsole() {}
