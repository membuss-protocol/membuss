package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type closerFunc func() error

func (f closerFunc) Close() error {
	return f()
}

// TestSelfUpdate_AtomicRenameAndReplace tests the atomic rename-aside and replacement sequence.
func TestSelfUpdate_AtomicRenameAndReplace(t *testing.T) {
	tempDir := t.TempDir()

	exeExt := ""
	if runtime.GOOS == "windows" {
		exeExt = ".exe"
	}

	targetExe := filepath.Join(tempDir, "membuss-desktop"+exeExt)
	newBinary := filepath.Join(tempDir, "new-desktop"+exeExt)

	origContent := []byte("original binary v1.0.0 content")
	newContent := []byte("upgraded binary v2.0.0 content")

	if err := os.WriteFile(targetExe, origContent, 0755); err != nil {
		t.Fatalf("failed to write original binary: %v", err)
	}
	if err := os.WriteFile(newBinary, newContent, 0755); err != nil {
		t.Fatalf("failed to write new binary: %v", err)
	}

	sum := NewSelfUpdateManager()

	// Perform replacement
	if err := sum.replaceExecutable(targetExe, newBinary); err != nil {
		t.Fatalf("replaceExecutable failed: %v", err)
	}

	// Verify targetExe now has newContent
	got, err := os.ReadFile(targetExe)
	if err != nil {
		t.Fatalf("failed to read target executable: %v", err)
	}
	if string(got) != string(newContent) {
		t.Errorf("expected %q, got %q", string(newContent), string(got))
	}

	// Verify .old backup exists
	oldExe := targetExe + ".old"
	oldGot, err := os.ReadFile(oldExe)
	if err != nil {
		t.Fatalf("expected .old backup to exist: %v", err)
	}
	if string(oldGot) != string(origContent) {
		t.Errorf("expected old backup %q, got %q", string(origContent), string(oldGot))
	}
}

// TestSelfUpdate_LockedHandleSimulation simulates a running open file handle on Windows
// and tests that renaming the open file aside and writing a replacement succeeds.
func TestSelfUpdate_LockedHandleSimulation(t *testing.T) {
	tempDir := t.TempDir()

	exeExt := ""
	if runtime.GOOS == "windows" {
		exeExt = ".exe"
	}

	targetExe := filepath.Join(tempDir, "membuss-running"+exeExt)
	newBinary := filepath.Join(tempDir, "membuss-new"+exeExt)

	origContent := []byte("running executable payload")
	newContent := []byte("new replacement executable payload")

	if err := os.WriteFile(targetExe, origContent, 0755); err != nil {
		t.Fatalf("failed to write target executable: %v", err)
	}
	if err := os.WriteFile(newBinary, newContent, 0755); err != nil {
		t.Fatalf("failed to write new binary: %v", err)
	}

	// Open file simulating a running executable
	handle, err := openSimulatedRunningExecutable(targetExe)
	if err != nil {
		t.Fatalf("failed to open simulated handle: %v", err)
	}
	defer handle.Close()

	sum := NewSelfUpdateManager()

	// Attempt replacement while running/open handle exists
	if err := sum.replaceExecutable(targetExe, newBinary); err != nil {
		t.Fatalf("replaceExecutable with open handle failed: %v", err)
	}

	// Read new executable
	got, err := os.ReadFile(targetExe)
	if err != nil {
		t.Fatalf("failed to read replaced executable: %v", err)
	}
	if string(got) != string(newContent) {
		t.Errorf("expected new content %q, got %q", string(newContent), string(got))
	}
}

// TestSelfUpdate_RollbackOnFailure tests that if the new binary cannot be copied,
// the original executable is restored to its original place.
func TestSelfUpdate_RollbackOnFailure(t *testing.T) {
	tempDir := t.TempDir()

	exeExt := ""
	if runtime.GOOS == "windows" {
		exeExt = ".exe"
	}

	targetExe := filepath.Join(tempDir, "membuss-app"+exeExt)
	nonExistentBinary := filepath.Join(tempDir, "does-not-exist"+exeExt)

	origContent := []byte("original uncorrupted payload")
	if err := os.WriteFile(targetExe, origContent, 0755); err != nil {
		t.Fatalf("failed to write original binary: %v", err)
	}

	sum := NewSelfUpdateManager()

	// Should fail because nonExistentBinary does not exist
	err := sum.replaceExecutable(targetExe, nonExistentBinary)
	if err == nil {
		t.Fatalf("expected error when replacing with non-existent binary, got nil")
	}

	// Verify targetExe was restored via rollback
	got, err := os.ReadFile(targetExe)
	if err != nil {
		t.Fatalf("failed to read restored target executable: %v", err)
	}
	if string(got) != string(origContent) {
		t.Errorf("expected restored content %q, got %q", string(origContent), string(got))
	}
}

// TestIsDesktopBinary tests the desktop binary name detection heuristic.
func TestIsDesktopBinary(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"membuss-desktop.exe", true},
		{"membuss-desktop", true},
		{"MEMBUSS-DESKTOP.EXE", true},
		{"membus-desktop.exe", true},
		{"membuss.exe", false},
		{"membuss", false},
		{"mem.exe", false},
		{"config.yaml", false},
	}

	for _, tt := range tests {
		got := IsDesktopBinary(tt.name)
		if got != tt.expected {
			t.Errorf("IsDesktopBinary(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

// TestExtractZipWithDualBinaries tests extracting an archive containing both daemon and desktop binaries.
func TestExtractZipWithDualBinaries(t *testing.T) {
	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "release.zip")
	destDir := filepath.Join(tempDir, "bin")

	// Create a mock zip with membuss.exe and membuss-desktop.exe
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	files := map[string]string{
		"membuss.exe":         "daemon binary code",
		"membuss-desktop.exe": "desktop app binary code",
	}

	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write content to zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}

	if err := os.WriteFile(archivePath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("failed to write test zip: %v", err)
	}

	// Extract
	if err := extractZip(archivePath, destDir); err != nil {
		t.Fatalf("extractZip failed: %v", err)
	}

	// Verify both binaries exist
	for name, expectedContent := range files {
		p := filepath.Join(destDir, name)
		got, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("failed to read extracted %s: %v", name, err)
			continue
		}
		if string(got) != expectedContent {
			t.Errorf("for %s expected %q, got %q", name, expectedContent, string(got))
		}
	}
}

// TestCleanExecutablePath tests stripping .old from paths.
func TestCleanExecutablePath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`C:\Program Files\Membuss\membuss-app.exe.old`, `C:\Program Files\Membuss\membuss-app.exe`},
		{`/usr/bin/membuss-desktop.old`, `/usr/bin/membuss-desktop`},
		{`C:\path\membuss-desktop.exe`, `C:\path\membuss-desktop.exe`},
		{"", ""},
	}
	for _, c := range cases {
		got := cleanExecutablePath(c.in)
		if got != c.want {
			t.Errorf("cleanExecutablePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFindExtractedDesktopBinary tests locating the desktop binary within an extracted directory.
func TestFindExtractedDesktopBinary(t *testing.T) {
	tempDir := t.TempDir()
	exeExt := ""
	if runtime.GOOS == "windows" {
		exeExt = ".exe"
	}

	desktopPath := filepath.Join(tempDir, "membuss-desktop"+exeExt)
	if err := os.WriteFile(desktopPath, []byte("desktop binary content"), 0755); err != nil {
		t.Fatalf("failed to write mock desktop binary: %v", err)
	}

	found := findExtractedDesktopBinary(tempDir)
	if found != desktopPath {
		t.Fatalf("findExtractedDesktopBinary = %q, want %q", found, desktopPath)
	}
}
