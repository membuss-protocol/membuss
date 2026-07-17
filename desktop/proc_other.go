//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// hideConsoleWindow is a no-op on non-Windows platforms.
func hideConsoleWindow(cmd *exec.Cmd) {}

// hideConsoleWindowSimple is a no-op on non-Windows platforms.
func hideConsoleWindowSimple(cmd *exec.Cmd) {}

// isPidActive checks if a PID is active on Unix systems using signal 0.
func isPidActive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// killPid kills a process by PID on Unix systems.
func killPid(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
