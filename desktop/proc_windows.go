//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// Windows process-creation flags for daemon children.
const (
	createBreakawayFromJob = 0x01000000 // survive parent GUI exit / job teardown
	createNewProcessGroup  = 0x00000200
	createNoWindow         = 0x08000000
)

// hideConsoleWindow sets creation flags on Windows to prevent a console
// window and to detach the child from the desktop app's job object so a
// keep-alive daemon is not killed when the GUI closes.
func hideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow | createBreakawayFromJob | createNewProcessGroup,
		HideWindow:    true,
	}
}

// hideConsoleWindowSimple is the fallback when CREATE_BREAKAWAY_FROM_JOB is
// rejected by the parent job (some sandboxed launchers).
func hideConsoleWindowSimple(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}
}
