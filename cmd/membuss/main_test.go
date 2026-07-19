// Tests for the CLI's gRPC plumbing. We boot a tiny gRPC
// server in-process (using a custom in-memory Backend) and
// point the CLI at it via --addr. Each test captures stdout
// and asserts the human-readable output is well-formed.
package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"

	memex "github.com/nnlgsakib/membuss/net/memex_v2"
	serverpkg "github.com/nnlgsakib/membuss/rpc/server"
)

// fakeBackend mirrors the rpc/server test backend so we can
// drive the CLI against a known state without bringing up a
// full libp2p stack.
type fakeBackend struct {
	root        string
	rootSize    uint64
	leafBlocks  uint64
	sealedSet   map[string]bool
	dhtPeekProv []serverpkg.NodePeerInfo
	peers       []serverpkg.NodePeerInfo
	anchor      serverpkg.AnchorInfo
	// name/mime are the stored ObjectInfo the daemon echoes
	// back on Stat; the CLI uses them to name downloads.
	name string
	mime string
}

func (f *fakeBackend) Add(ctx context.Context, path, chunker string, chunkSize uint32, sealRoot bool, name, mimeType string) (serverpkg.AddResult, error) {
	f.root = "memfake"
	f.rootSize = 42
	f.leafBlocks = 3
	if sealRoot {
		f.sealedSet = map[string]bool{f.root: true}
	}
	return serverpkg.AddResult{MID: f.root, Size: f.rootSize, Blocks: f.leafBlocks, Sealed: sealRoot}, nil
}

func (f *fakeBackend) AddWithProgress(ctx context.Context, path, chunker string, chunkSize uint32, sealRoot bool, name, mimeType string, progressFn func(processed, total uint64)) (serverpkg.AddResult, error) {
	res, err := f.Add(ctx, path, chunker, chunkSize, sealRoot, name, mimeType)
	if err == nil && progressFn != nil {
		progressFn(res.Size, res.Size)
	}
	return res, err
}

func (f *fakeBackend) AddDirWithProgress(ctx context.Context, path, chunker string, chunkSize uint32, sealRoot bool, name string, progressFn func(processed, total uint64)) (serverpkg.AddResult, error) {
	res, err := f.Add(ctx, path, chunker, chunkSize, sealRoot, name, "inode/directory")
	if err == nil && progressFn != nil {
		progressFn(res.Size, res.Size)
	}
	return res, err
}

func (f *fakeBackend) Get(ctx context.Context, midStr string, offset, limit uint64) (io.ReadCloser, error) {
	rc, _, err := f.GetWithProgress(ctx, midStr, offset, limit, nil)
	return rc, err
}

func (f *fakeBackend) GetWithProgress(ctx context.Context, midStr string, offset, limit uint64, progressFn func(update memex.ProgressUpdate)) (io.ReadCloser, serverpkg.ContentMeta, error) {
	body := []byte("the quick brown fox jumps over the lazy dog\n")
	if progressFn != nil {
		progressFn(memex.ProgressUpdate{BlocksResolved: uint64(len(body)), BlocksTotal: uint64(len(body))})
	}
	meta := serverpkg.ContentMeta{Name: f.name, MimeType: f.mime, Size: uint64(len(body))}
	return io.NopCloser(bytes.NewReader(body)), meta, nil
}

func (f *fakeBackend) Seal(ctx context.Context, midStr string, recursive bool) (serverpkg.SealResult, error) {
	if f.sealedSet == nil {
		f.sealedSet = map[string]bool{}
	}
	if f.sealedSet[midStr] {
		return serverpkg.SealResult{Pinned: 0, Already: true}, nil
	}
	f.sealedSet[midStr] = true
	return serverpkg.SealResult{Pinned: 1, Already: false}, nil
}

func (f *fakeBackend) Unseal(ctx context.Context, midStr string) (uint64, error) {
	if !f.sealedSet[midStr] {
		return 0, nil
	}
	delete(f.sealedSet, midStr)
	return 1, nil
}

func (f *fakeBackend) Stat(ctx context.Context, midStr string) (serverpkg.StatInfo, error) {
	if midStr != f.root {
		return serverpkg.StatInfo{}, nil
	}
	return serverpkg.StatInfo{Present: true, Size: f.rootSize, Blocks: f.leafBlocks, Sealed: f.sealedSet[midStr], Codec: 0x55, Name: f.name, MimeType: f.mime}, nil
}

func (f *fakeBackend) Peers(limit uint32) ([]serverpkg.NodePeerInfo, uint32, error) {
	out := f.peers
	if limit > 0 && uint32(len(out)) > limit {
		out = out[:limit]
	}
	return out, uint32(len(f.peers)), nil
}

func (f *fakeBackend) DHTPeek(ctx context.Context, midStr string, limit uint32) ([]serverpkg.NodePeerInfo, error) {
	return f.dhtPeekProv, nil
}

func (f *fakeBackend) GC(ctx context.Context, all bool) (serverpkg.GCInfo, error) {
	return serverpkg.GCInfo{BytesFreed: 4096, BlocksKept: 12}, nil
}

func (f *fakeBackend) Delete(ctx context.Context, midStr string) (serverpkg.DeleteResult, error) {
	return serverpkg.DeleteResult{BlocksDeleted: 1, BytesFreed: 4096}, nil
}

func (f *fakeBackend) AnchorStatus() serverpkg.AnchorInfo {
	return f.anchor
}

// startTestServer boots a gRPC server on a free loopback port
// and returns its address. The caller is responsible for calling
// the returned cleanup function.
func startTestServer(t *testing.T, b serverpkg.Backend) (addr string, cleanup func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	serverpkg.NewServer(b).Register(srv)
	go func() { _ = srv.Serve(lis) }()
	return lis.Addr().String(), func() { srv.Stop() }
}

// withCLI invokes the CLI's Execute() with os.Args replaced
// with the supplied args. stdout is captured.
func withCLI(t *testing.T, args []string, stdin io.Reader) (stdout, stderr string, err error) {
	t.Helper()

	oldStdout, oldStderr, oldStdin := os.Stdout, os.Stderr, os.Stdin
	defer func() { os.Stdout, os.Stderr, os.Stdin = oldStdout, oldStderr, oldStdin }()

	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr
	if stdin != nil {
		os.Stdin = os.NewFile(uintptr(0), "/dev/stdin") // best-effort, ignored if nil
		_ = os.Stdin
	}

	// Copy both pipes concurrently. We must wait for BOTH goroutines
	// to finish before reading the buffers, otherwise reading
	// errBuf.String() races the stderr copy still writing into it.
	outDone := make(chan struct{})
	errDone := make(chan struct{})
	var outBuf, errBuf bytes.Buffer
	go func() { _, _ = io.Copy(&outBuf, rOut); close(outDone) }()
	go func() { _, _ = io.Copy(&errBuf, rErr); close(errDone) }()

	// Replace os.Args for the duration of this test.
	oldArgs := os.Args
	os.Args = append([]string{"membuss"}, args...)
	defer func() { os.Args = oldArgs }()

	err = newRootCmd().Execute()
	_ = wOut.Close()
	_ = wErr.Close()
	<-outDone
	<-errDone
	return outBuf.String(), errBuf.String(), err
}

func TestCLI_Ping(t *testing.T) {
	addr, stop := startTestServer(t, &fakeBackend{})
	defer stop()

	out, _, err := withCLI(t, []string{"--addr", addr, "ping", "hello"}, nil)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !strings.Contains(out, "build") {
		t.Errorf("expected build row; got %q", out)
	}
	if !strings.Contains(out, "echo") || !strings.Contains(out, "hello") {
		t.Errorf("expected echoed message; got %q", out)
	}
}

func TestCLI_Add(t *testing.T) {
	addr, stop := startTestServer(t, &fakeBackend{})
	defer stop()

	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(p, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	out, _, err := withCLI(t, []string{"--addr", addr, "add", p}, nil)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out, "memfake") {
		t.Errorf("expected MID in output; got %q", out)
	}
	if !strings.Contains(out, "blocks") {
		t.Errorf("expected blocks row; got %q", out)
	}
}

func TestCLI_Get_Stdout(t *testing.T) {
	addr, stop := startTestServer(t, &fakeBackend{})
	defer stop()
	out, _, err := withCLI(t, []string{"--addr", addr, "get", "memfake", "-o", "-"}, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(out, "the quick brown fox") {
		t.Errorf("expected content; got %q", out)
	}
}

func TestCLI_Get_ToFile(t *testing.T) {
	addr, stop := startTestServer(t, &fakeBackend{})
	defer stop()
	dir := t.TempDir()
	p := filepath.Join(dir, "out.bin")
	_, _, err := withCLI(t, []string{"--addr", addr, "get", "memfake", "-o", p}, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "quick brown fox") {
		t.Errorf("file content wrong: %q", got)
	}
}

// chdirTemp switches the process into a fresh temp dir for the
// duration of the test so default (cwd) downloads are isolated.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return dir
}

// A bare `get` (no -o) must NOT dump raw bytes to stdout; it
// downloads to a file named from the stored metadata, mirroring
// the explorer.
func TestCLI_Get_DefaultDownloadsToNamedFile(t *testing.T) {
	b := &fakeBackend{root: "memfake", name: "report.txt", mime: "text/plain"}
	addr, stop := startTestServer(t, b)
	defer stop()
	dir := chdirTemp(t)

	out, errStr, err := withCLI(t, []string{"--addr", addr, "get", "memfake"}, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if strings.Contains(out, "quick brown fox") {
		t.Errorf("raw payload leaked to stdout: %q", out)
	}
	if !strings.Contains(errStr, "Saved") || !strings.Contains(errStr, "report.txt") {
		t.Errorf("expected save notice mentioning filename; got stderr %q", errStr)
	}

	got, err := os.ReadFile(filepath.Join(dir, "report.txt"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !strings.Contains(string(got), "quick brown fox") {
		t.Errorf("downloaded content wrong: %q", got)
	}
}

// With no recorded name, the fallback is "<mid>.bin".
func TestCLI_Get_DefaultFallbackName(t *testing.T) {
	addr, stop := startTestServer(t, &fakeBackend{})
	defer stop()
	dir := chdirTemp(t)

	if _, _, err := withCLI(t, []string{"--addr", addr, "get", "memfake"}, nil); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memfake.bin")); err != nil {
		t.Errorf("expected fallback memfake.bin: %v", err)
	}
}

// A name clash appends " (n)" instead of overwriting.
func TestCLI_Get_DefaultAvoidsClobber(t *testing.T) {
	b := &fakeBackend{root: "memfake", name: "data.txt"}
	addr, stop := startTestServer(t, b)
	defer stop()
	dir := chdirTemp(t)

	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := withCLI(t, []string{"--addr", addr, "get", "memfake"}, nil); err != nil {
		t.Fatalf("get: %v", err)
	}
	// Original untouched.
	if got, _ := os.ReadFile(filepath.Join(dir, "data.txt")); string(got) != "existing" {
		t.Errorf("original file was clobbered: %q", got)
	}
	// New file written alongside.
	if _, err := os.Stat(filepath.Join(dir, "data (1).txt")); err != nil {
		t.Errorf("expected non-clobbering variant data (1).txt: %v", err)
	}
}

// -o pointing at a directory saves under the recorded name.
func TestCLI_Get_OutDirUsesRecordedName(t *testing.T) {
	b := &fakeBackend{root: "memfake", name: "photo.jpg"}
	addr, stop := startTestServer(t, b)
	defer stop()
	dir := t.TempDir()

	if _, _, err := withCLI(t, []string{"--addr", addr, "get", "memfake", "-o", dir}, nil); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "photo.jpg")); err != nil {
		t.Errorf("expected photo.jpg inside output dir: %v", err)
	}
}

func TestCLI_FitToWidth(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"short", 0, "short"},   // unknown width: untouched
		{"short", 100, "short"}, // fits: untouched
		{"abcdefghij", 5, "abcd…"},
		{"abcdefghij", 1, "a"},
	}
	for _, c := range cases {
		if got := fitToWidth(c.in, c.width); got != c.want {
			t.Errorf("fitToWidth(%q,%d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
}

// On a non-terminal writer (like a bytes.Buffer) the progress bar
// must emit nothing — no carriage returns, no bar frames — so
// redirected/piped output stays clean.
func TestCLI_ProgressBar_SilentOnNonTTY(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressBar(&buf, "file.bin")
	for i := uint64(1); i <= 100; i++ {
		p.render(i*1000, 100*1000, i, false)
	}
	p.render(100*1000, 100*1000, 100, true)
	p.finish()
	p.clear()
	if buf.Len() != 0 {
		t.Errorf("expected no output on non-tty writer, got %q", buf.String())
	}
}

func TestCLI_SanitizeDownloadName(t *testing.T) {
	cases := map[string]string{
		"clean.txt":         "clean.txt",
		"a/b/evil.txt":      "evil.txt",
		"..":                "",
		".":                 "",
		"with:bad*chars?.x": "with_bad_chars_.x",
		"":                  "",
	}
	for in, want := range cases {
		if got := sanitizeDownloadName(in); got != want {
			t.Errorf("sanitizeDownloadName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCLI_Seal_Idempotent(t *testing.T) {
	addr, stop := startTestServer(t, &fakeBackend{})
	defer stop()
	out1, _, err := withCLI(t, []string{"--addr", addr, "seal", "memfake"}, nil)
	if err != nil {
		t.Fatalf("seal1: %v", err)
	}
	if !strings.Contains(out1, "pinned") || !strings.Contains(out1, "1") {
		t.Errorf("first seal: expected pinned=1; got %q", out1)
	}
	out2, _, err := withCLI(t, []string{"--addr", addr, "seal", "memfake"}, nil)
	if err != nil {
		t.Fatalf("seal2: %v", err)
	}
	if !strings.Contains(out2, "already") || !strings.Contains(out2, "true") {
		t.Errorf("second seal: expected already=true; got %q", out2)
	}
}

func TestCLI_Peers(t *testing.T) {
	b := &fakeBackend{peers: []serverpkg.NodePeerInfo{
		{PeerID: "12D3KooA", Addrs: []string{"/ip4/1.2.3.4/tcp/4001"}},
	}}
	addr, stop := startTestServer(t, b)
	defer stop()
	out, _, err := withCLI(t, []string{"--addr", addr, "peers"}, nil)
	if err != nil {
		t.Fatalf("peers: %v", err)
	}
	if !strings.Contains(out, "12D3KooA") || !strings.Contains(out, "total") {
		t.Errorf("expected peer + total; got %q", out)
	}
}

func TestCLI_DHTPeek(t *testing.T) {
	b := &fakeBackend{dhtPeekProv: []serverpkg.NodePeerInfo{
		{PeerID: "12D3KooB", Addrs: []string{"/ip4/1.2.3.4/tcp/4001"}},
	}}
	addr, stop := startTestServer(t, b)
	defer stop()
	out, _, err := withCLI(t, []string{"--addr", addr, "dht", "peek", "memx"}, nil)
	if err != nil {
		t.Fatalf("dht peek: %v", err)
	}
	if !strings.Contains(out, "12D3KooB") {
		t.Errorf("expected provider; got %q", out)
	}
}

func TestCLI_GC(t *testing.T) {
	addr, stop := startTestServer(t, &fakeBackend{})
	defer stop()
	out, _, err := withCLI(t, []string{"--addr", addr, "gc"}, nil)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if !strings.Contains(out, "bytes_freed") {
		t.Errorf("expected bytes_freed row; got %q", out)
	}
}

func TestCLI_AnchorStatus(t *testing.T) {
	b := &fakeBackend{anchor: serverpkg.AnchorInfo{
		PeerID:     "12D3KooC",
		UptimeSecs: 120,
		BlocksHeld: 99,
		Anchors:    4,
		Synced:     11,
	}}
	addr, stop := startTestServer(t, b)
	defer stop()
	out, _, err := withCLI(t, []string{"--addr", addr, "anchor", "status"}, nil)
	if err != nil {
		t.Fatalf("anchor status: %v", err)
	}
	if !strings.Contains(out, "12D3KooC") || !strings.Contains(out, "synced") || !strings.Contains(out, "11") {
		t.Errorf("expected anchor status; got %q", out)
	}
}

func TestCLI_Stat_MissingMID(t *testing.T) {
	addr, stop := startTestServer(t, &fakeBackend{})
	defer stop()
	out, _, err := withCLI(t, []string{"--addr", addr, "stat", "memdoesnotexist"}, nil)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !strings.Contains(out, "present") || !strings.Contains(out, "false") {
		t.Errorf("expected present=false; got %q", out)
	}
}

func TestCLI_FormatBytes(t *testing.T) {
	cases := []struct {
		n    uint64
		want string
	}{
		{0, "0 B"},
		{1024, "1.00 KiB"},
		{1024 * 1024, "1.00 MiB"},
	}
	for _, c := range cases {
		got := formatBytes(c.n)
		if got != c.want {
			t.Errorf("formatBytes(%d)=%q want %q", c.n, got, c.want)
		}
	}
}

func TestCLI_RejectsMissingArg(t *testing.T) {
	addr, stop := startTestServer(t, &fakeBackend{})
	defer stop()
	_, _, err := withCLI(t, []string{"--addr", addr, "add"}, nil)
	if err == nil {
		t.Fatal("expected error for missing file arg")
	}
}

func TestCLI_DaemonStatus_AliasesPing(t *testing.T) {
	addr, stop := startTestServer(t, &fakeBackend{})
	defer stop()
	out, _, err := withCLI(t, []string{"--addr", addr, "daemon", "status"}, nil)
	if err != nil {
		t.Fatalf("daemon status: %v", err)
	}
	if !strings.Contains(out, "ok\tbuild=") {
		t.Errorf("expected ok/build= line; got %q", out)
	}
}

// Sanity check that cobra's command tree has the documented
// subcommands. This guards against accidental removals during
// refactors.
func TestCLI_CommandTree(t *testing.T) {
	want := []string{"add", "get", "seal", "unseal", "stat", "peers", "dht", "gc", "anchor", "ping", "daemon"}
	root := newRootCmd()
	got := make(map[string]bool, len(root.Commands()))
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing command: %s", w)
		}
	}
}
