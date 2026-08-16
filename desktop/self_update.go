package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// SelfUpdateManager handles safe, cross-platform replacement and relaunch
// of the running desktop application executable.
type SelfUpdateManager struct{}

// NewSelfUpdateManager returns a new SelfUpdateManager instance.
func NewSelfUpdateManager() *SelfUpdateManager {
	return &SelfUpdateManager{}
}

// CleanupOldExecutables cleans up any leftover .old executable files
// from previous self-update cycles. This is called during app startup.
func CleanupOldExecutables() {
	currentExe, err := os.Executable()
	if err != nil {
		return
	}
	currentExe = resolveSymlinks(currentExe)
	oldExe := currentExe + ".old"
	if _, err := os.Stat(oldExe); err == nil {
		_ = os.Remove(oldExe)
	}
}

// resolveSymlinks follows symlinks to the canonical path if possible.
func resolveSymlinks(p string) string {
	if eval, err := filepath.EvalSymlinks(p); err == nil {
		return eval
	}
	return p
}

// ApplySelfUpdate safely replaces the current running desktop executable
// with the new binary at newBinaryPath.
//
// On Windows (where running executables cannot be directly overwritten),
// it renames the running binary to <name>.old, then copies the new binary
// into the original path.
//
// On Unix/macOS, it uses atomic rename/replacement.
func (sum *SelfUpdateManager) ApplySelfUpdate(newBinaryPath string) error {
	if newBinaryPath == "" {
		return fmt.Errorf("new binary path cannot be empty")
	}

	fi, err := os.Stat(newBinaryPath)
	if err != nil {
		return fmt.Errorf("new binary not found at %s: %w", newBinaryPath, err)
	}
	if fi.IsDir() || fi.Size() == 0 {
		return fmt.Errorf("invalid new binary at %s (empty or directory)", newBinaryPath)
	}

	currentExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate current running executable: %w", err)
	}
	currentExe = resolveSymlinks(currentExe)

	// If the new binary is already at the target path, nothing to do
	if filepath.Clean(currentExe) == filepath.Clean(newBinaryPath) {
		return nil
	}

	return sum.replaceExecutable(currentExe, newBinaryPath)
}

// replaceExecutable performs the atomic rename-aside and copy sequence.
func (sum *SelfUpdateManager) replaceExecutable(targetExe, newBinary string) error {
	oldExe := targetExe + ".old"

	// 1. Remove any previous .old file
	_ = os.Remove(oldExe)

	// 2. Rename current running executable to .old (NTFS allows renaming open files)
	if err := os.Rename(targetExe, oldExe); err != nil {
		return fmt.Errorf("failed to rename running binary to backup: %w", err)
	}

	// 3. Copy the new binary to the target executable path
	if err := copyExecutableFile(newBinary, targetExe); err != nil {
		// Rollback on failure
		_ = os.Rename(oldExe, targetExe)
		return fmt.Errorf("failed to write new executable to %s (rolled back): %w", targetExe, err)
	}

	// 4. Ensure executable permissions on Unix/macOS
	if runtime.GOOS != "windows" {
		_ = os.Chmod(targetExe, 0755)
	}

	return nil
}

// copyExecutableFile copies binary content preserving executable mode.
func copyExecutableFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	srcInfo, err := in.Stat()
	if err != nil {
		return err
	}

	// Create new executable file with write permissions
	mode := srcInfo.Mode()
	if mode == 0 {
		mode = 0755
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// RelaunchApp launches the newly updated desktop executable and cleanly
// terminates the current process.
func (sum *SelfUpdateManager) RelaunchApp(args ...string) error {
	currentExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate current executable: %w", err)
	}
	currentExe = resolveSymlinks(currentExe)

	cmd := exec.Command(currentExe, args...)
	cmd.Dir = filepath.Dir(currentExe)

	// Set OS-specific detachment flags so the child process outlives the parent
	setupDetachedProcess(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to spawn updated process: %w", err)
	}

	// Exit the current process cleanly
	go func() {
		// Give the OS 200ms to spawn the new window before exit
		// (allows Wails IPC response to return to frontend if needed)
		if runtime.GOOS != "windows" {
			_ = cmd.Process.Release()
		}
		os.Exit(0)
	}()

	return nil
}

func setupDetachedProcess(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
		}
	}
}

// IsDesktopBinary returns true if the filename represents the desktop application.
func IsDesktopBinary(name string) bool {
	lower := strings.ToLower(filepath.Base(name))
	return strings.HasPrefix(lower, "membuss-desktop") ||
		strings.HasPrefix(lower, "membus-desktop")
}
