package ipc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
)

// DefaultSocketPath returns the standard IPC socket location.
func DefaultSocketPath(dataDir string) string {
	if env := os.Getenv("MEMBUSS_IPC_PATH"); env != "" {
		return env
	}
	if dataDir != "" {
		return filepath.Join(dataDir, "membuss.sock")
	}
	home, err := os.UserHomeDir()
	if err == nil {
		dir := filepath.Join(home, ".membuss")
		_ = os.MkdirAll(dir, 0700)
		return filepath.Join(dir, "membuss.sock")
	}
	if runtime.GOOS == "windows" {
		return `\\.\pipe\membuss`
	}
	return "/tmp/membuss.sock"
}

// Listen constructs a cross-platform IPC socket listener.
// For Unix domain sockets, it cleans up stale socket files,
// creates necessary parent directories, and enforces strict owner-only (0600) permissions.
func Listen(socketPath string) (net.Listener, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("ipc: empty socket path")
	}

	// Remove stale socket file if present
	if _, err := os.Stat(socketPath); err == nil {
		_ = os.Remove(socketPath)
	}

	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("ipc: mkdir %s: %w", dir, err)
	}

	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen unix %s: %w", socketPath, err)
	}

	// Enforce owner-only permissions on Unix/Linux/macOS
	if runtime.GOOS != "windows" {
		_ = os.Chmod(socketPath, 0600)
	}

	return lis, nil
}

// Dial connects to an IPC socket listener using a context for timeout control.
func Dial(ctx context.Context, socketPath string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socketPath)
}

// Cleanup removes the socket file on shutdown.
func Cleanup(socketPath string) {
	if socketPath != "" && !strings.HasPrefix(socketPath, `\\.\pipe\`) {
		_ = os.Remove(socketPath)
	}
}

type preferexConn struct {
	net.Conn
	r io.Reader
}

func (c *preferexConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}

type pipeListener struct {
	addr   net.Addr
	ch     chan net.Conn
	closed atomic.Bool
}

func newPipeListener(addr net.Addr) *pipeListener {
	return &pipeListener{
		addr: addr,
		ch:   make(chan net.Conn, 128),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	conn, ok := <-l.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *pipeListener) Close() error {
	if l.closed.CompareAndSwap(false, true) {
		close(l.ch)
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return l.addr
}

// Multiplex demuxes an IPC listener into two virtual listeners:
// one for gRPC traffic (HTTP/2 with "PRI " preface) and one for HTTP REST API traffic.
func Multiplex(lis net.Listener) (grpcLis net.Listener, httpLis net.Listener) {
	grpcL := newPipeListener(lis.Addr())
	httpL := newPipeListener(lis.Addr())

	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				_ = grpcL.Close()
				_ = httpL.Close()
				return
			}

			br := bufio.NewReader(conn)
			prefix, _ := br.Peek(4)
			wrapped := &preferexConn{Conn: conn, r: br}

			if len(prefix) >= 4 && string(prefix) == "PRI " {
				select {
				case grpcL.ch <- wrapped:
				default:
					conn.Close()
				}
			} else {
				select {
				case httpL.ch <- wrapped:
				default:
					conn.Close()
				}
			}
		}
	}()

	return grpcL, httpL
}
