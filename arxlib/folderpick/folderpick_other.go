//go:build !windows

package folderpick

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
