package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExtractTarGz(t *testing.T) {
	// Create a temporary directory for test outputs
	tmpDir, err := os.MkdirTemp("", "membuss-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a mock tar.gz archive in memory
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	// Add a directory entry
	dirHeader := &tar.Header{
		Name:     "subdir/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	}
	if err := tw.WriteHeader(dirHeader); err != nil {
		t.Fatalf("failed to write dir header: %v", err)
	}

	// Add a file entry inside directory
	file1Content := []byte("hello daemon")
	file1Header := &tar.Header{
		Name:     "subdir/daemon",
		Typeflag: tar.TypeReg,
		Size:     int64(len(file1Content)),
		Mode:     0755,
	}
	if err := tw.WriteHeader(file1Header); err != nil {
		t.Fatalf("failed to write file1 header: %v", err)
	}
	if _, err := tw.Write(file1Content); err != nil {
		t.Fatalf("failed to write file1 content: %v", err)
	}

	// Add a file entry at root
	file2Content := []byte("hello cli")
	file2Header := &tar.Header{
		Name:     "cli",
		Typeflag: tar.TypeReg,
		Size:     int64(len(file2Content)),
		Mode:     0644,
	}
	if err := tw.WriteHeader(file2Header); err != nil {
		t.Fatalf("failed to write file2 header: %v", err)
	}
	if _, err := tw.Write(file2Content); err != nil {
		t.Fatalf("failed to write file2 content: %v", err)
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}

	// Write mock archive to disk
	archivePath := filepath.Join(tmpDir, "mock.tar.gz")
	if err := os.WriteFile(archivePath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("failed to write mock archive: %v", err)
	}

	// Extract mock archive using extractTarGz
	destDir := filepath.Join(tmpDir, "extracted")
	if err := extractTarGz(archivePath, destDir); err != nil {
		t.Fatalf("extractTarGz failed: %v", err)
	}

	// Validate subdirectory existence
	subDirPath := filepath.Join(destDir, "subdir")
	fi, err := os.Stat(subDirPath)
	if err != nil {
		t.Fatalf("expected subdirectory to exist: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("expected subdir to be a directory")
	}

	// Validate subdirectory file
	f1Path := filepath.Join(subDirPath, "daemon")
	f1Data, err := os.ReadFile(f1Path)
	if err != nil {
		t.Fatalf("failed to read extracted file1: %v", err)
	}
	if string(f1Data) != string(file1Content) {
		t.Errorf("expected content %q, got %q", file1Content, f1Data)
	}

	// Validate root level file
	f2Path := filepath.Join(destDir, "cli")
	f2Data, err := os.ReadFile(f2Path)
	if err != nil {
		t.Fatalf("failed to read extracted file2: %v", err)
	}
	if string(f2Data) != string(file2Content) {
		t.Errorf("expected content %q, got %q", file2Content, f2Data)
	}

	// On non-Windows platforms, check that file permissions are set to 0755
	if runtime.GOOS != "windows" {
		fi1, _ := os.Stat(f1Path)
		if fi1.Mode().Perm() != 0755 {
			t.Errorf("expected permission 0755 for f1, got %o", fi1.Mode().Perm())
		}
		fi2, _ := os.Stat(f2Path)
		if fi2.Mode().Perm() != 0755 {
			t.Errorf("expected permission 0755 for f2, got %o", fi2.Mode().Perm())
		}
	}
}

func TestIsPortFreeAndFindNextFreePort(t *testing.T) {
	// Bind to a port temporarily
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on dynamic port: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	// Check if the port is free (should be false since we are listening)
	if isPortFree(addr) {
		t.Errorf("expected port %s to be busy, but it was reported free", addr)
	}

	// Try to find the next free port starting from this busy one
	nextFree, err := findNextFreePort(addr)
	if err != nil {
		t.Fatalf("findNextFreePort failed: %v", err)
	}

	if nextFree == addr {
		t.Errorf("expected next free port to be different from %s, got same", addr)
	}

	// The next free port should actually be free
	if !isPortFree(nextFree) {
		t.Errorf("expected found port %s to be free, but it was reported busy", nextFree)
	}
}

// freeLocalAddr grabs an ephemeral port and immediately releases it so the
// daemon manager's API probe fails fast with "connection refused" instead
// of timing out. There is a tiny inherent race (another process could grab
// the port), but it is negligible for the tests below.
func freeLocalAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to grab ephemeral port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// TestIsMembussPidAlive_NonMembussPID ensures that a PID which exists but
// does not belong to the membuss daemon is reported as "not alive". This
// is the core of the false-positive "daemon already running" fix: a
// recycled PID (now owned by some unrelated process) must never be
// mistaken for the daemon.
func TestIsMembussPidAlive_NonMembussPID(t *testing.T) {
	// The test binary itself is running and is definitely not membuss.
	ourPid := os.Getpid()
	if isMembussPidAlive(ourPid) {
		t.Errorf("isMembussPidAlive(%d) = true; the test process is not membuss", ourPid)
	}

	// Invalid PIDs.
	if isMembussPidAlive(0) {
		t.Errorf("isMembussPidAlive(0) = true; want false")
	}
	if isMembussPidAlive(-1) {
		t.Errorf("isMembussPidAlive(-1) = true; want false")
	}

	// A PID that almost certainly does not exist. Use a very large number
	// rather than 1<<31 to stay within typical OS pid ceilings.
	if isMembussPidAlive(999999) {
		t.Errorf("isMembussPidAlive(999999) = true; want false for a non-existent PID")
	}
}

// TestIsRunning_StalePidFileRecycled is the regression test for the
// reported bug: the user taps "Finish & Start Node", the daemon is NOT
// running, but a stale daemon.pid points at a PID that has since been
// recycled to an unrelated process. The old code returned true (because
// isPidActive only checked PID liveness) and blocked Start() with
// "daemon is already running". The fix verifies the process image name,
// treats the foreign PID as stale, and cleans up the pid file.
func TestIsRunning_StalePidFileRecycled(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "membuss-stalepid-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Point the stale pid file at our own (test) process — a live PID
	// that is NOT membuss, simulating PID reuse after a daemon crash.
	pidPath := filepath.Join(tmpDir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		t.Fatalf("failed to write stale pid file: %v", err)
	}

	dm := NewDaemonManager(&DesktopConfig{
		DataDir: tmpDir,
		APIAddr: freeLocalAddr(t), // nothing listening → apiHealthy is false
	})

	if dm.IsRunning() {
		t.Fatalf("IsRunning() = true for a stale pid file pointing at a non-membuss process; " +
			"this is the false-positive 'daemon already running' bug")
	}

	// The stale pid file should have been cleaned up so the next Start()
	// attempt is not blocked either.
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("expected stale daemon.pid to be removed, but it still exists (err=%v)", err)
	}
}

// TestReadPidFile covers the pid-file parsing helper so corrupt/empty files
// are never misread as a running daemon.
func TestReadPidFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "membuss-pidfile-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dm := NewDaemonManager(&DesktopConfig{DataDir: tmpDir})

	// Missing file.
	if _, ok := dm.readPidFile(); ok {
		t.Errorf("readPidFile() = ok for a missing file; want false")
	}

	// Valid pid.
	pidPath := filepath.Join(tmpDir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte("4242"), 0644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	pid, ok := dm.readPidFile()
	if !ok || pid != 4242 {
		t.Errorf("readPidFile() = (%d, %v); want (4242, true)", pid, ok)
	}

	// Corrupt content.
	if err := os.WriteFile(pidPath, []byte("not-a-number"), 0644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	if _, ok := dm.readPidFile(); ok {
		t.Errorf("readPidFile() = ok for corrupt content; want false")
	}

	// Zero/negative pid is rejected.
	if err := os.WriteFile(pidPath, []byte("0"), 0644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	if _, ok := dm.readPidFile(); ok {
		t.Errorf("readPidFile() = ok for pid 0; want false")
	}

	// removePidFile deletes it.
	dm.removePidFile()
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("removePidFile did not delete the file (err=%v)", err)
	}
}
