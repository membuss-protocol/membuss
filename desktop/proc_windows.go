//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
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

// isPidActive checks if a PID is running on Windows.
func isPidActive(pid int) bool {
	// Tasklist eq matches the exact PID
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH")
	hideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), fmt.Sprintf("%d", pid))
}

// killPid kills a process by PID on Windows.
func killPid(pid int) error {
	cmd := exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid))
	hideConsoleWindow(cmd)
	return cmd.Run()
}
