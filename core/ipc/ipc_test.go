package ipc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestIPCListenerAndDialer(t *testing.T) {
	tempDir := t.TempDir()
	sockPath := filepath.Join(tempDir, "test_membuss.sock")

	lis, err := Listen(sockPath)
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer lis.Close()
	defer Cleanup(sockPath)

	// Verify file permissions on Unix
	if runtime.GOOS != "windows" {
		info, err := os.Stat(sockPath)
		if err != nil {
			t.Fatalf("Stat socket failed: %v", err)
		}
		mode := info.Mode().Perm()
		if mode != 0600 {
			t.Errorf("Expected socket permissions 0600, got %o", mode)
		}
	}

	// Echo server loop
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 128)
		n, _ := conn.Read(buf)
		_, _ = conn.Write(buf[:n])
	}()

	// Dial client
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := Dial(ctx, sockPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	msg := []byte("hello membuss ipc")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	reply := make([]byte, 128)
	n, err := conn.Read(reply)
	if err != nil && err != io.EOF {
		t.Fatalf("Read failed: %v", err)
	}

	if string(reply[:n]) != string(msg) {
		t.Errorf("Expected %q, got %q", string(msg), string(reply[:n]))
	}
}

func TestDefaultSocketPath(t *testing.T) {
	t.Setenv("MEMBUSS_IPC_PATH", "/custom/path.sock")
	if p := DefaultSocketPath(""); p != "/custom/path.sock" {
		t.Errorf("Expected custom path, got %s", p)
	}

	t.Setenv("MEMBUSS_IPC_PATH", "")
	dirPath := DefaultSocketPath("/var/data")
	if filepath.Base(dirPath) != "membuss.sock" {
		t.Errorf("Expected membuss.sock in dataDir, got %s", dirPath)
	}
}
