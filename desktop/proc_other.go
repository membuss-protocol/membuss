//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// hideConsoleWindow is a no-op on non-Windows platforms.
func hideConsoleWindow(cmd *exec.Cmd) {}

// hideConsoleWindowSimple is a no-op on non-Windows platforms.
func hideConsoleWindowSimple(cmd *exec.Cmd) {}

// isPidActive checks if a PID is active on Unix systems using signal 0.
//
// Note: this only tests whether *some* process owns the PID, not whether
// that process is membuss. Use isMembussPidAlive for daemon-liveness
// checks where PID reuse could produce a false positive.
func isPidActive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// isMembussPidAlive reports whether the given PID currently belongs to a
// membuss daemon process on Unix systems.
//
// Unlike isPidActive, this verifies the process name (not just PID
// liveness) so that PID reuse by an unrelated process can never produce a
// false "daemon already running" result. Half-dead zombie daemons are
// treated as "not alive" so the caller can clean them up and restart.
//
// It is deliberately multi-strategy and tolerant:
//   - On Linux, /proc is always mounted (no external command needed).
//   - On macOS/BSD, the POSIX `ps` utility is used instead.
//   - If neither is available, the function returns false rather than
//     risking a false positive, so the user can always restart their node.
func isMembussPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	// Linux: /proc is always present and needs no external command.
	if comm, isZombie, ok := readProcComm(pid); ok {
		if isZombie {
			return false
		}
		return isMembussName(comm)
	}

	// macOS / BSD / fallback: ps is POSIX and available everywhere ps exists.
	if comm, ok := psComm(pid); ok {
		return isMembussName(comm)
	}

	// Last resort: signal 0 only tells us *something* is running at this
	// PID. Avoid the false-positive trap by treating it as "not ours" —
	// safer to allow a restart than to wedge the user out of their node.
	return false
}

// readProcComm reads /proc/<pid>/comm and /proc/<pid>/status on Linux.
// Returns (comm, isZombie, ok). ok is false when /proc is unavailable
// (non-Linux), allowing the caller to fall back to another strategy.
func readProcComm(pid int) (string, bool, bool) {
	commPath := fmt.Sprintf("/proc/%d/comm", pid)
	data, err := os.ReadFile(commPath)
	if err != nil {
		return "", false, false
	}
	comm := strings.TrimSpace(string(data))

	// Check for zombie state so we never treat a defunct daemon as alive.
	// A zombie cannot be killed; only reaped by its parent. Treating it
	// as "not alive" lets Start() proceed and (eventually) init reaps it.
	isZombie := false
	if status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "State:") {
				if strings.Contains(line, "Z") {
					isZombie = true
				}
				break
			}
		}
	}
	return comm, isZombie, true
}

// psComm uses the POSIX ps utility to fetch the command name for a PID.
// Works on macOS, BSD, and Linux (as a fallback when /proc is absent).
// Returns (name, ok); ok is false if ps is missing or errors.
func psComm(pid int) (string, bool) {
	cmd := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "comm=")
	hideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// isMembussName reports whether a process name (from /proc/comm or ps)
// is the membuss daemon.
//
// /proc/comm is truncated to 15 chars (TASK_COMM_LEN); "membuss" is 7,
// so it fits exactly. The legacy separate CLI ("membuss-cli") is excluded
// by the exact-name match so a stale CLI PID is never mistaken for the
// daemon.
func isMembussName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	// ps may report a full path on some platforms; take the basename.
	name = filepath.Base(name)
	name = strings.TrimSuffix(name, ".exe")
	return strings.EqualFold(name, "membuss")
}

// killPid kills a process by PID on Unix systems.
func killPid(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
