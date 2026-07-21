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
//
// Note: this only tests whether *some* process owns the PID, not whether
// that process is membuss. Use isMembussPidAlive for daemon-liveness
// checks where PID reuse could produce a false positive.
func isPidActive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Tasklist eq matches the exact PID
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH")
	hideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), fmt.Sprintf("%d", pid))
}

// isMembussPidAlive reports whether the given PID currently belongs to a
// membuss daemon process on Windows.
//
// Unlike isPidActive, this verifies the process image name (not just PID
// liveness) so that PID reuse by an unrelated process can never produce a
// false "daemon already running" result. tasklist is asked for CSV output
// so the image name is an exact, quoted field rather than a substring of
// a free-form line.
//
// If tasklist is unavailable (extremely rare on Windows), the function
// returns false so the caller tolerates the obstacle and proceeds to
// (re)start the daemon rather than wedging the user out of their node.
func isMembussPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
	hideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return false
	}
	// tasklist emits an informational line (e.g. "INFO: No tasks are
	// running which match...") when nothing matches the filter; that is
	// not a running process.
	if lower := strings.ToLower(line); strings.HasPrefix(lower, "info:") || strings.HasPrefix(lower, "error:") {
		return false
	}
	// CSV row: "membuss.exe","4821","Console","1","12,288 K"
	// Take the first comma-separated field and strip its quotes.
	name := line
	if i := strings.Index(name, ","); i >= 0 {
		name = name[:i]
	}
	name = strings.Trim(name, "\"")
	return strings.EqualFold(name, "membuss.exe")
}

// killPid kills a process by PID on Windows.
func killPid(pid int) error {
	cmd := exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid))
	hideConsoleWindow(cmd)
	return cmd.Run()
}
