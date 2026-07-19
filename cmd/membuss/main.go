// Command membuss is the single, unified Membuss binary: it is
// both the node daemon and the operator client.
//
// Two entry modes share one executable:
//
//   - CLI mode (default): the cobra command tree — add, get,
//     seal, stat, peers, dht, gc, anchor, memns, init, daemon,
//     … — dials a running daemon over gRPC / HTTP.
//
//   - Daemon mode: `membuss daemon start` (foreground, Ctrl+C to
//     stop) boots the node in-process via the daemon package.
//     For backward compatibility with every existing launcher
//     (the desktop app, the container entrypoint, `make
//     run-daemon`, and the e2e harness), invoking the binary
//     with a leading daemon flag — e.g. `membuss -config X
//     -datadir Y` or `membuss --in-memory` — is dispatched
//     straight to the daemon runtime, exactly as the old
//     standalone `membuss` binary behaved.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/nnlgsakib/membuss/cmd/membuss/daemon"
	"github.com/nnlgsakib/membuss/config"
	"github.com/nnlgsakib/membuss/core/memlink"
	"github.com/nnlgsakib/membuss/core/version"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

var (
	// globalAddr is the gRPC endpoint the CLI dials. Resolved
	// from --addr, then MEMBUSS_ADDR, then the config file.
	globalAddr string
	// globalConfigPath is the YAML config used to discover
	// the gRPC endpoint when --addr is not given.
	globalConfigPath string
	// globalAPIAddr is the HTTP API endpoint the CLI uses
	// for MemFS commands (ls, get with path, add with
	// --wrap-dir, add <directory>). Resolved from --api-addr,
	// then $MEMBUSS_API_ADDR, then the config file's
	// APIAddr, then 127.0.0.1:5001.
	globalAPIAddr string
)

func main() {
	// Legacy / standalone daemon dispatch. The old `membuss`
	// binary was invoked as `membuss -config X -datadir Y`,
	// `membuss --in-memory`, etc. Those callers (desktop app,
	// container entrypoint, make run-daemon, e2e harness) pass a
	// daemon flag as the first argument, which is not a cobra
	// subcommand. When we detect that shape, hand the whole arg
	// slice to the daemon runtime so nothing downstream breaks.
	if isLegacyDaemonInvocation(os.Args[1:]) {
		if err := daemon.Run(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "membuss:", err)
			os.Exit(1)
		}
		return
	}

	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "membuss:", err)
		os.Exit(1)
	}
}

// isLegacyDaemonInvocation reports whether the process was
// started in the historical standalone-daemon form: a run made
// up entirely of the daemon's own flags with no CLI subcommand,
// e.g. `membuss -config X -datadir Y`, `membuss --in-memory`, or
// `membuss -datadir X -no-anchor`. Those callers (the desktop
// app, the container entrypoint, `make run-daemon`, and the e2e
// harness) predate the unified binary and must keep booting the
// node directly.
//
// It scans the whole argument list rather than only args[0] so
// that a leading *global* flag followed by a subcommand — the
// ordinary cobra form `membuss --datadir X init` — is routed to
// the CLI, not the daemon. The scan skips the value that a
// string flag consumes so the token after it is classified
// correctly. The generic help/version flags always fall through
// to cobra so the root usage renders.
func isLegacyDaemonInvocation(args []string) bool {
	if len(args) == 0 {
		return false
	}
	// String flags shared by the daemon runtime and the CLI root.
	// When one appears without an "=value", it consumes the next
	// argument, which must not be mistaken for a subcommand.
	valueFlags := map[string]bool{"config": true, "datadir": true, "build": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			// First bare token is a subcommand (add, init, daemon,
			// …) or garbage — either way cobra owns it.
			return false
		}
		name := strings.TrimLeft(a, "-")
		eq := strings.IndexByte(name, '=')
		bare := name
		if eq >= 0 {
			bare = name[:eq]
		}
		// Help/version render cobra's usage, never the daemon.
		if bare == "h" || bare == "help" || bare == "version" {
			return false
		}
		if eq < 0 && valueFlags[bare] {
			i++ // skip the flag's value
		}
	}
	// Every token was a daemon-shaped flag and none was a
	// subcommand or help/version request → historical form.
	return true
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "membuss",
		Short: "Membuss node and operator CLI",
		Long: `membuss is the unified Membuss binary: it runs the node and
talks to a locally-running node over gRPC.

Run the node:
  membuss daemon start          run the node in the foreground
  membuss -config membuss.yaml  legacy standalone form (still supported)

Operate a running node (mirrors the MembussNode service):
  add, get, seal, unseal, stat, peers, dht, gc, anchor.

Run "membuss init" first to set up the data directory.`,
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.PersistentFlags().StringVar(&globalAddr, "addr", "", "daemon gRPC address (default: from config)")
	root.PersistentFlags().StringVar(&globalAPIAddr, "api-addr", "", "daemon HTTP API address for MemFS commands (default: 127.0.0.1:5001)")
	root.PersistentFlags().StringVar(&globalConfigPath, "config", "membuss.yaml", "config file used to locate the daemon")
	root.PersistentFlags().String("datadir", "", "data directory (default $HOME/.memdata; overrides MEMBUSS_DATADIR)")

	root.AddCommand(
		newAddCmd(),
		newGetCmd(),
		newSealCmd(),
		newUnsealCmd(),
		newDeleteCmd(),
		newStatCmd(),
		newLsCmd(),
		newPeersCmd(),
		newDHTCmd(),
		newGCCmd(),
		newAnchorCmd(),
		newPingCmd(),
		newDaemonCmd(),
		newInitCmd(),
		newMemNSCmd(),
		newKeyRingCmd(),
		newDescriptorCmd(),
		newVersionCmd(),
	)
	return root
}

// --- connection helpers ---

// resolveAddr returns the gRPC endpoint the CLI should dial.
// Priority:
//  1. --addr flag
//  2. $MEMBUSS_ADDR
//  3. config.yaml in --datadir (or $MEMBUSS_DATADIR or $HOME/.memdata)
//  4. config.yaml at the legacy --config path
//  5. 127.0.0.1:50051
func resolveAddr() (string, error) {
	if globalAddr != "" {
		return globalAddr, nil
	}
	if v := os.Getenv("MEMBUSS_ADDR"); v != "" {
		return v, nil
	}
	if datadir := config.ResolveDataDir(""); datadir != "" {
		if cfg, err := config.LoadConfig(datadir); err == nil && cfg.GRPCAddr != "" {
			return cfg.GRPCAddr, nil
		}
	}
	if cfg, err := config.Load(globalConfigPath); err == nil && cfg.GRPCAddr != "" {
		return cfg.GRPCAddr, nil
	}
	return "127.0.0.1:50051", nil
}

// dial opens a gRPC connection to the daemon.
func dial() (membusspb.MembussNodeClient, membusspb.NodeClient, *grpc.ClientConn, error) {
	addr, err := resolveAddr()
	if err != nil {
		return nil, nil, nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return membusspb.NewMembussNodeClient(conn), membusspb.NewNodeClient(conn), conn, nil
}

// withConn runs fn with a connected client and closes it
// afterwards.
func withConn(fn func(m membusspb.MembussNodeClient, n membusspb.NodeClient) error) error {
	mc, nc, conn, err := dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(mc, nc)
}

// --- add ---

func newAddCmd() *cobra.Command {
	var (
		chunker   string
		chunkSize uint32
		noSeal    bool
		wrapDir   bool
		dirName   string
	)
	c := &cobra.Command{
		Use:   "add <file-or-dir>",
		Short: "Upload a file or directory, seal the root, return the MID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			// When the path is a directory, ingest it as a
			// single MemFS DIR tree. The default route is the
			// streaming AddDirStream gRPC (progress bar, same
			// transport as single-file add); a daemon that
			// predates the RPC transparently falls back to the
			// /add/dir HTTP endpoint.
			if fi, err := os.Stat(path); err == nil && fi.IsDir() {
				return withConn(func(mc membusspb.MembussNodeClient, _ membusspb.NodeClient) error {
					ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
					defer cancel()
					req := &membusspb.AddRequest{
						Path:      path,
						Chunker:   chunker,
						ChunkSize: chunkSize,
						NoSeal:    noSeal,
						Name:      dirName,
					}
					resp, err := addDirWithProgress(ctx, mc, req, cmd.ErrOrStderr())
					if err != nil {
						if status.Code(err) == codes.Unimplemented {
							return addDirectoryHTTP(cmd, path, dirName)
						}
						return err
					}
					tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
					fmt.Fprintf(tw, "MID\t%s\n", resp.Mid)
					fmt.Fprintf(tw, "size\t%s (%d bytes)\n", formatBytes(resp.Size), resp.Size)
					fmt.Fprintf(tw, "blocks\t%d\n", resp.Blocks)
					fmt.Fprintf(tw, "sealed\t%t\n", resp.Sealed)
					return tw.Flush()
				})
			}
			if wrapDir {
				return addFileHTTP(cmd, path, true)
			}
			return withConn(func(mc membusspb.MembussNodeClient, _ membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
				defer cancel()
				req := &membusspb.AddRequest{
					Path:      args[0],
					Chunker:   chunker,
					ChunkSize: chunkSize,
					NoSeal:    noSeal,
				}
				resp, err := addWithProgress(ctx, mc, req, cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(tw, "MID\t%s\n", resp.Mid)
				fmt.Fprintf(tw, "size\t%s (%d bytes)\n", formatBytes(resp.Size), resp.Size)
				fmt.Fprintf(tw, "blocks\t%d\n", resp.Blocks)
				fmt.Fprintf(tw, "sealed\t%t\n", resp.Sealed)
				return tw.Flush()
			})
		},
	}
	c.Flags().StringVar(&chunker, "chunker", "", "chunker: \"fixed\" (default) or \"rabin\"")
	c.Flags().Uint32Var(&chunkSize, "chunk-size", 0, "fixed chunk size in bytes (default 256 KiB)")
	c.Flags().BoolVar(&noSeal, "no-seal", false, "do not seal the root after ingest")
	c.Flags().BoolVar(&wrapDir, "wrap-dir", false, "wrap the file in a single-entry DIR node (MemFS)")
	c.Flags().StringVar(&dirName, "name", "", "custom name for the uploaded directory (defaults to directory basename)")
	return c
}

// addWithProgress ingests via the streaming AddStream RPC,
// drawing a byte-level progress bar (on a tty) while the daemon
// reads and chunks the file, and returns the final result. If
// the daemon is older and does not implement AddStream, it
// transparently falls back to the unary Add RPC.
func addWithProgress(ctx context.Context, mc membusspb.MembussNodeClient, req *membusspb.AddRequest, progressW io.Writer) (*membusspb.AddResponse, error) {
	stream, err := mc.AddStream(ctx, req)
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			return mc.Add(ctx, req)
		}
		return nil, err
	}

	bar := newProgressBar(progressW, "uploading "+filepath.Base(req.GetPath()))
	var final *membusspb.AddResponse
	for {
		frame, rerr := stream.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			// A daemon that advertises the method but streams
			// nothing (older stub) surfaces here; fall back.
			if status.Code(rerr) == codes.Unimplemented {
				return mc.Add(ctx, req)
			}
			bar.clear()
			return nil, rerr
		}
		if frame.GetDone() {
			final = &membusspb.AddResponse{
				Mid:    frame.GetMid(),
				Size:   frame.GetSize(),
				Blocks: frame.GetBlocks(),
				Sealed: frame.GetSealed(),
			}
			continue
		}
		bar.render(frame.GetBytesProcessed(), frame.GetTotalBytes(), 0, false)
	}
	if final == nil {
		bar.clear()
		return nil, errors.New("add: stream closed without a result")
	}
	// Land the bar on 100% before finishing.
	bar.render(final.Size, final.Size, 0, true)
	bar.finish()
	return final, nil
}

// addDirWithProgress ingests a directory via the streaming
// AddDirStream RPC, drawing a byte-level progress bar while the
// daemon walks and chunks the tree. It returns codes.Unimplemented
// verbatim when the daemon predates the RPC so the caller can fall
// back to the HTTP directory-upload path.
func addDirWithProgress(ctx context.Context, mc membusspb.MembussNodeClient, req *membusspb.AddRequest, progressW io.Writer) (*membusspb.AddResponse, error) {
	stream, err := mc.AddDirStream(ctx, req)
	if err != nil {
		return nil, err
	}

	label := req.GetName()
	if label == "" {
		label = filepath.Base(req.GetPath())
	}
	bar := newProgressBar(progressW, "uploading "+label+"/")
	var final *membusspb.AddResponse
	for {
		frame, rerr := stream.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			bar.clear()
			return nil, rerr
		}
		if frame.GetDone() {
			final = &membusspb.AddResponse{
				Mid:    frame.GetMid(),
				Size:   frame.GetSize(),
				Blocks: frame.GetBlocks(),
				Sealed: frame.GetSealed(),
			}
			continue
		}
		bar.render(frame.GetBytesProcessed(), frame.GetTotalBytes(), 0, false)
	}
	if final == nil {
		bar.clear()
		return nil, errors.New("add: stream closed without a result")
	}
	bar.render(final.Size, final.Size, 0, true)
	bar.finish()
	return final, nil
}

// --- get ---

func newGetCmd() *cobra.Command {
	var (
		outPath string
		offset  uint64
		limit   uint64
	)
	c := &cobra.Command{
		Use:   "get <MID> [-o file]",
		Short: "Download the content of a MID to a file (use -o - to stream to stdout)",
		Long: "Download the content of a MID.\n\n" +
			"By default the file is saved to the current directory using the\n" +
			"uploader-supplied name and type (the same metadata the explorer\n" +
			"uses), falling back to <MID>.bin when no name is recorded. A name\n" +
			"clash is resolved by appending \" (n)\" rather than overwriting.\n\n" +
			"Pass -o <file> to choose the path, -o <dir> to save into a directory\n" +
			"under the recorded name, or -o - to stream raw bytes to stdout for\n" +
			"piping.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(func(mc membusspb.MembussNodeClient, _ membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
				defer cancel()

				// Fetch the stored metadata once: name + mime,
				// mirroring how the explorer names downloads.
				var meta *membusspb.StatResponse
				if st, err := mc.Stat(ctx, &membusspb.StatRequest{Mid: args[0]}); err == nil {
					meta = st
				}

				// Probe if it is a directory via HTTP ls API
				lsURL := httpBase() + "/api/v1/ls/" + args[0]
				var isDir bool
				if resp, err := http.Get(lsURL); err == nil {
					defer resp.Body.Close()
					if resp.StatusCode == 200 {
						isDir = true
					}
				}

				if isDir {
					targetDir := outPath
					if targetDir == "" || targetDir == "-" {
						if meta != nil {
							targetDir = meta.Name
						}
						if targetDir == "" {
							targetDir = args[0]
						}
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "Downloading directory structure to %s...\n", targetDir)
					return downloadDirRecursive(ctx, mc, args[0], targetDir, cmd.ErrOrStderr())
				}

				stream, err := mc.Get(ctx, &membusspb.GetRequest{
					Mid:    args[0],
					Offset: offset,
					Limit:  limit,
				})
				if err != nil {
					return err
				}

				// Only an explicit "-o -" streams to stdout;
				// otherwise we always write a real file so a
				// bare `get` no longer floods the terminal with
				// raw bytes. The destination file is created
				// lazily once the metadata is known: the daemon
				// sends a header frame with the authoritative
				// name / MIME type / size, which is the only
				// source that also covers content pulled from
				// the network (the pre-Get Stat is empty for a
				// MID that was not local). We seed from the
				// local Stat as a fallback for older daemons
				// that do not emit a header frame.
				toStdout := outPath == "-"
				var (
					out      io.Writer
					f        *os.File
					destPath string
					name     = downloadName(args[0], meta)
					mimeType string
				)
				if meta != nil {
					mimeType = meta.MimeType
				}
				if toStdout {
					out = cmd.OutOrStdout()
				}
				// ensureOut creates the destination file on first
				// payload byte if the header frame did not already
				// trigger creation, so a download always lands
				// somewhere even against an older daemon.
				ensureOut := func() error {
					if toStdout || out != nil {
						return nil
					}
					var derr error
					destPath, derr = resolveDownloadPath(outPath, name)
					if derr != nil {
						return derr
					}
					f, derr = os.Create(destPath)
					if derr != nil {
						return derr
					}
					out = f
					return nil
				}
				defer func() {
					if f != nil {
						_ = f.Close()
					}
				}()

				// Progress is a single in-place line on stderr,
				// so it is safe even when the payload streams to
				// stdout. On a non-terminal stderr it stays
				// silent instead of spamming control characters.
				prog := newProgressBar(cmd.ErrOrStderr(), name)
				var (
					total      uint64
					received   uint64
					blocksRecv uint64
					fetching   bool
				)
				for {
					frame, err := stream.Recv()
					if err == io.EOF {
						break
					}
					if err != nil {
						prog.clear()
						return err
					}

					// Fetch-phase progress: the daemon is still
					// pulling blocks from the network; no payload
					// yet.
					if frame.GetProgressOnly() {
						fetching = true
						prog.renderFetch(frame.GetFetchedBlocks(), frame.GetTotalBlocks())
						continue
					}

					// Header frame: authoritative metadata. Adopt
					// the network-resolved name / mime / size and
					// (re)label the progress bar before payload.
					if frame.GetIsHeader() {
						if n := sanitizeDownloadName(frame.GetName()); n != "" {
							name = n
							prog.label = name
						}
						if frame.GetMimeType() != "" {
							mimeType = frame.GetMimeType()
						}
						if frame.GetTotalSize() > 0 {
							total = frame.GetTotalSize()
						}
						if fetching {
							// Clear the fetch line so the byte bar
							// starts fresh.
							prog.clear()
							prog.firstDraw = true
						}
						continue
					}

					if len(frame.Data) > 0 {
						if err := ensureOut(); err != nil {
							prog.clear()
							return err
						}
						if _, err := out.Write(frame.Data); err != nil {
							prog.clear()
							return err
						}
						received += uint64(len(frame.Data))
						blocksRecv++
					}
					if frame.Total > 0 {
						total = frame.Total
					}
					prog.render(received, total, blocksRecv, false)
				}
				// A zero-byte object still needs its file created.
				if err := ensureOut(); err != nil {
					prog.clear()
					return err
				}
				prog.render(received, total, blocksRecv, true)
				prog.finish()
				if !toStdout {
					if mimeType == "" {
						mimeType = "application/octet-stream"
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "Saved %s (%s, %s) to %s\n", name, mimeType, formatBytes(received), destPath)
				}
				return nil
			})
		},
	}
	c.Flags().StringVarP(&outPath, "out", "o", "", "output path: a file, a directory, or - for stdout (default: recorded filename in the current directory)")
	c.Flags().Uint64Var(&offset, "offset", 0, "byte offset to start at")
	c.Flags().Uint64Var(&limit, "limit", 0, "maximum bytes (0 = until EOF)")
	return c
}

// progressBar renders a single, in-place download progress line
// on an io.Writer (stderr). It solves two problems with the naive
// "\r"+Fprintf approach: (1) on a non-terminal writer it stays
// silent so redirected/piped output is not polluted with control
// bytes, and (2) it throttles redraws and fits the line to the
// terminal width so a long MID/filename can never wrap onto a new
// physical line (which is what turned one progress line into
// thousands).
type progressBar struct {
	w         io.Writer
	label     string // filename or MID being fetched
	tty       bool   // w is an interactive terminal
	start     time.Time
	last      time.Time // last redraw, for throttling
	lastLen   int       // width of the last line drawn, to erase cleanly
	firstDraw bool
}

func newProgressBar(w io.Writer, label string) *progressBar {
	return &progressBar{
		w:         w,
		label:     label,
		tty:       isTerminalWriter(w),
		start:     time.Now(),
		firstDraw: true,
	}
}

const progressBarWidth = 20

// render draws the bar. It is a no-op on a non-tty writer (unless
// force is set, which prints a final plain summary line). Redraws
// are throttled to ~15/s so a fast stream does not thrash the
// terminal.
func (p *progressBar) render(received, total, blocks uint64, force bool) {
	if !p.tty {
		return
	}
	now := time.Now()
	if !force && !p.firstDraw && now.Sub(p.last) < 66*time.Millisecond {
		return
	}
	p.firstDraw = false
	p.last = now

	elapsed := now.Sub(p.start).Seconds()
	var pct int
	var sizeStr string
	if total > 0 {
		pct = int(received * 100 / total)
		if pct > 100 {
			pct = 100
		}
		sizeStr = fmt.Sprintf("%s / %s", formatBytes(received), formatBytes(total))
	} else {
		sizeStr = fmt.Sprintf("%s / ?", formatBytes(received))
	}
	filled := pct * progressBarWidth / 100
	bar := strings.Repeat("=", filled) + strings.Repeat(" ", progressBarWidth-filled)
	var rate string
	if elapsed > 0 {
		rate = fmt.Sprintf(" %s/s", formatBytes(uint64(float64(received)/elapsed)))
	}

	line := fmt.Sprintf("%s [%s] %3d%% (%s)%s", p.label, bar, pct, sizeStr, rate)
	line = fitToWidth(line, terminalWidth(p.w))

	// Pad with spaces to erase any remnant of a longer prior line,
	// then carriage-return to the start (no newline).
	pad := ""
	if d := p.lastLen - len(line); d > 0 {
		pad = strings.Repeat(" ", d)
	}
	fmt.Fprintf(p.w, "\r%s%s", line, pad)
	p.lastLen = len(line)
}

// renderFetch draws progress for the network-fetch phase, where
// the daemon is pulling blocks from peers before any payload
// byte is available. It reports block resolution (fetched /
// total) rather than bytes, so the user sees the download is
// alive instead of a silent stall. Throttled and tty-gated like
// render.
func (p *progressBar) renderFetch(fetched, total uint64) {
	if !p.tty {
		return
	}
	now := time.Now()
	if !p.firstDraw && now.Sub(p.last) < 66*time.Millisecond {
		return
	}
	p.firstDraw = false
	p.last = now

	var pct int
	var countStr string
	if total > 0 {
		pct = int(fetched * 100 / total)
		if pct > 100 {
			pct = 100
		}
		countStr = fmt.Sprintf("%d / %d blocks", fetched, total)
	} else {
		countStr = fmt.Sprintf("%d blocks", fetched)
	}
	filled := pct * progressBarWidth / 100
	bar := strings.Repeat("=", filled) + strings.Repeat(" ", progressBarWidth-filled)

	line := fmt.Sprintf("%s [%s] %3d%% (fetching %s)", p.label, bar, pct, countStr)
	line = fitToWidth(line, terminalWidth(p.w))

	pad := ""
	if d := p.lastLen - len(line); d > 0 {
		pad = strings.Repeat(" ", d)
	}
	fmt.Fprintf(p.w, "\r%s%s", line, pad)
	p.lastLen = len(line)
}

// finish terminates the progress line with a newline so following
// output starts cleanly. No-op on non-tty (nothing was drawn).
func (p *progressBar) finish() {
	if p.tty {
		fmt.Fprint(p.w, "\n")
	}
}

// clear wipes the current progress line (used before printing an
// error so it does not get tangled with the bar).
func (p *progressBar) clear() {
	if p.tty && p.lastLen > 0 {
		fmt.Fprintf(p.w, "\r%s\r", strings.Repeat(" ", p.lastLen))
		p.lastLen = 0
	}
}

// fitToWidth truncates s (with an ellipsis) so it never exceeds
// width columns, keeping the whole line on one physical row. A
// width <= 0 (unknown) means no truncation.
func fitToWidth(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	if width <= 1 {
		return s[:width]
	}
	return s[:width-1] + "…"
}

// isTerminalWriter reports whether w is an interactive terminal we
// can safely draw an in-place progress bar on. Anything that is
// not an *os.File (e.g. a test buffer or a pipe) is treated as
// non-interactive so progress control bytes never leak into
// captured or redirected output.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// terminalWidth returns the column count of w's terminal, or 0 when
// it cannot be determined (in which case the caller does not
// truncate). We leave one column of slack so the cursor parking at
// the end of a full-width line cannot force a wrap.
func terminalWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	cols, _, err := term.GetSize(int(f.Fd()))
	if err != nil || cols <= 0 {
		return 0
	}
	return cols - 1
}

// downloadName picks the on-disk filename for a MID download,
// mirroring the gateway/explorer convention: the uploader-supplied
// name wins, falling back to "<mid>.bin". The result is sanitized
// so a hostile name cannot escape the target directory or inject
// path separators.
func downloadName(midStr string, meta *membusspb.StatResponse) string {
	name := ""
	if meta != nil {
		name = meta.Name
	}
	name = sanitizeDownloadName(name)
	if name == "" {
		name = midStr + ".bin"
	}
	return name
}

// resolveDownloadPath maps the -o value + derived name to a
// concrete, non-clobbering destination path:
//   - ""            -> <name> in the current directory
//   - existing dir  -> <name> inside that directory
//   - anything else -> used verbatim (the user chose it)
//
// For the first two cases a name clash is resolved by appending
// " (n)" before the extension, like a browser download.
func resolveDownloadPath(outPath, name string) (string, error) {
	if outPath == "" {
		return uniquePath(name), nil
	}
	if fi, err := os.Stat(outPath); err == nil && fi.IsDir() {
		return uniquePath(filepath.Join(outPath, name)), nil
	}
	// Explicit path: honor it as-is (overwrite allowed — the
	// caller named the file). Ensure the parent exists.
	if dir := filepath.Dir(outPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	return outPath, nil
}

// uniquePath returns p if nothing exists there, otherwise the
// first "<base> (n)<ext>" variant that is free.
func uniquePath(p string) string {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 1; ; i++ {
		cand := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

// sanitizeDownloadName strips path separators and control/reserved
// characters so a recorded name is safe to use as a bare filename.
// It matches the gateway's sanitizeFilename plus a defense against
// "." / ".." and any leading directory component.
func sanitizeDownloadName(s string) string {
	// Take only the final path element to defeat embedded
	// separators, then map out anything unsafe.
	s = filepath.Base(filepath.FromSlash(s))
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		switch r {
		case '"', '\\', '/', '<', '>', '|', ':', '*', '?':
			return '_'
		}
		return r
	}, s)
	if s == "." || s == ".." {
		return ""
	}
	return s
}

func downloadDirRecursive(ctx context.Context, mc membusspb.MembussNodeClient, midStr, localPath string, errWriter io.Writer) error {
	if err := os.MkdirAll(localPath, 0o755); err != nil {
		return err
	}

	resp, err := http.Get(httpBase() + "/api/v1/ls/" + midStr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ls %s: %s", midStr, string(body))
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Entries []struct {
				Name string `json:"name"`
				MID  string `json:"mid"`
				Type string `json:"type"`
				Size uint64 `json:"size"`
			} `json:"entries"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return err
	}
	if !env.OK {
		return fmt.Errorf("ls %s: %s", midStr, env.Error)
	}

	for _, e := range env.Data.Entries {
		childPath := filepath.Join(localPath, e.Name)
		if e.Type == "dir" {
			if err := downloadDirRecursive(ctx, mc, e.MID, childPath, errWriter); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(errWriter, "Downloading %s -> %s\n", e.Name, childPath)
			if err := downloadFile(ctx, mc, e.MID, childPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func downloadFile(ctx context.Context, mc membusspb.MembussNodeClient, midStr, localPath string) error {
	stream, err := mc.Get(ctx, &membusspb.GetRequest{Mid: midStr})
	if err != nil {
		return err
	}
	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if _, err := f.Write(frame.Data); err != nil {
			return err
		}
	}
	return nil
}

// --- seal / unseal ---

func newSealCmd() *cobra.Command {
	var recursive bool
	c := &cobra.Command{
		Use:   "seal <MID>",
		Short: "Pin a MID (and optionally all reachable blocks)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(func(mc membusspb.MembussNodeClient, _ membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				defer cancel()
				resp, err := mc.Seal(ctx, &membusspb.SealRequest{Mid: args[0], Recursive: recursive})
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(tw, "pinned\t%d\n", resp.Pinned)
				fmt.Fprintf(tw, "already\t%t\n", resp.Already)
				return tw.Flush()
			})
		},
	}
	c.Flags().BoolVar(&recursive, "recursive", true, "seal every block reachable from this MID")
	return c
}

func newUnsealCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unseal <MID>",
		Short: "Remove the pin on a MID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(func(mc membusspb.MembussNodeClient, _ membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				defer cancel()
				resp, err := mc.Unseal(ctx, &membusspb.UnsealRequest{Mid: args[0]})
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(tw, "removed\t%d\n", resp.Removed)
				return tw.Flush()
			})
		},
	}
}

func newDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <MID>",
		Short: "Delete a MID and all its reachable blocks recursively from the local node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(func(mc membusspb.MembussNodeClient, _ membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				defer cancel()
				resp, err := mc.Delete(ctx, &membusspb.DeleteRequest{Mid: args[0]})
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(tw, "deleted_blocks\t%d\n", resp.BlocksDeleted)
				fmt.Fprintf(tw, "bytes_freed\t%d\n", resp.BytesFreed)
				return tw.Flush()
			})
		},
	}
}

// --- stat ---

func newStatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stat <MID>",
		Short: "Show size, block count, and seal status for a MID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(func(mc membusspb.MembussNodeClient, _ membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				defer cancel()
				resp, err := mc.Stat(ctx, &membusspb.StatRequest{Mid: args[0]})
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(tw, "present\t%t\n", resp.Present)
				fmt.Fprintf(tw, "size\t%s (%d bytes)\n", formatBytes(resp.Size), resp.Size)
				fmt.Fprintf(tw, "blocks\t%d\n", resp.Blocks)
				fmt.Fprintf(tw, "sealed\t%t\n", resp.Sealed)
				fmt.Fprintf(tw, "sealers\t%d (anchors: %d)\n", resp.Sealers, resp.AnchorSealers)
				fmt.Fprintf(tw, "codec\t0x%x\n", resp.Codec)
				if resp.Erasure != nil {
					fmt.Fprintf(tw, "erasure\t%d+%d (%d shards)\n", resp.Erasure.DataShards, resp.Erasure.ParityShards, len(resp.Erasure.ShardMids))
				}
				return tw.Flush()
			})
		},
	}
}

// --- peers ---

func newPeersCmd() *cobra.Command {
	var limit uint32
	c := &cobra.Command{
		Use:   "peers",
		Short: "List peers known to the local PEX table",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(func(mc membusspb.MembussNodeClient, _ membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				defer cancel()
				resp, err := mc.Peers(ctx, &membusspb.PeersRequest{Limit: limit})
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(tw, "PEER ID\tANCHOR\tADDRS\n")
				for _, p := range resp.Peers {
					fmt.Fprintf(tw, "%s\t%t\t%s\n", p.PeerId, p.IsAnchor, strings.Join(p.Addrs, ","))
				}
				fmt.Fprintf(tw, "\n")
				fmt.Fprintf(tw, "total\t%d (showing %d)\n", resp.Total, len(resp.Peers))
				return tw.Flush()
			})
		},
	}
	c.Flags().Uint32Var(&limit, "limit", 0, "max entries to return (0 = all)")
	return c
}

// --- dht ---

func newDHTCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dht",
		Short: "DHT inspection commands",
	}
	cmd.AddCommand(newDHTPeekCmd())
	return cmd
}

func newDHTPeekCmd() *cobra.Command {
	var limit uint32
	c := &cobra.Command{
		Use:   "peek <MID>",
		Short: "Ask the local DHT who provides a MID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(func(mc membusspb.MembussNodeClient, _ membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				defer cancel()
				resp, err := mc.DHTPeek(ctx, &membusspb.DHTPeekRequest{Mid: args[0], Limit: limit})
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(tw, "PEER ID\tANCHOR\tADDRS\n")
				for _, p := range resp.Providers {
					fmt.Fprintf(tw, "%s\t%t\t%s\n", p.PeerId, p.IsAnchor, strings.Join(p.Addrs, ","))
				}
				return tw.Flush()
			})
		},
	}
	c.Flags().Uint32Var(&limit, "limit", 0, "max entries to return (0 = all)")
	return c
}

// --- gc ---

func newGCCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:   "gc",
		Short: "Run garbage collection on the local store",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(func(mc membusspb.MembussNodeClient, _ membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
				defer cancel()
				resp, err := mc.GC(ctx, &membusspb.GCRequest{All: all})
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(tw, "bytes_freed\t%s (%d bytes)\n", formatBytes(resp.BytesFreed), resp.BytesFreed)
				fmt.Fprintf(tw, "blocks_kept\t%d\n", resp.BlocksKept)
				return tw.Flush()
			})
		},
	}
	c.Flags().BoolVar(&all, "all", false, "reserved for future per-namespace GC flags")
	return c
}

// --- anchor ---

func newAnchorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "anchor",
		Short: "Anchor Node commands",
	}
	cmd.AddCommand(newAnchorStatusCmd())
	return cmd
}

func newAnchorStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show Anchor Node engine stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(func(mc membusspb.MembussNodeClient, _ membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				defer cancel()
				resp, err := mc.AnchorStatus(ctx, &membusspb.AnchorStatusRequest{})
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(tw, "peer_id\t%s\n", resp.PeerId)
				fmt.Fprintf(tw, "uptime\t%s\n", time.Duration(resp.UptimeSeconds)*time.Second)
				fmt.Fprintf(tw, "blocks_held\t%d\n", resp.BlocksHeld)
				fmt.Fprintf(tw, "anchors\t%d\n", resp.Anchors)
				fmt.Fprintf(tw, "backlog\t%d\n", resp.Backlog)
				fmt.Fprintf(tw, "synced\t%d\n", resp.Synced)
				return tw.Flush()
			})
		},
	}
}

// --- ping ---

func newPingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ping [message]",
		Short: "Send a connectivity probe to the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(func(_ membusspb.MembussNodeClient, nc membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
				defer cancel()
				msg := ""
				if len(args) > 0 {
					msg = strings.Join(args, " ")
				}
				resp, err := nc.Ping(ctx, &membusspb.PingRequest{Message: msg})
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(tw, "build\t%s\n", resp.Build)
				if resp.Message != "" {
					fmt.Fprintf(tw, "echo\t%s\n", resp.Message)
				}
				return tw.Flush()
			})
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print client and server version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "Client:")
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", version.String())

			fmt.Fprintln(cmd.OutOrStdout(), "Server:")
			err := withConn(func(_ membusspb.MembussNodeClient, nc membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
				defer cancel()
				resp, err := nc.Ping(ctx, &membusspb.PingRequest{Message: ""})
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  membuss daemon version: %s\n", resp.Build)
				return nil
			})
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  error contacting daemon: %v\n", err)
			}
		},
	}
}

// --- daemon ---

// newDaemonCmd exposes the node runtime through the unified
// binary. `membuss daemon start` boots the node in-process (this
// is the same runtime the legacy `membuss -config ...` form runs)
// and blocks in the foreground until Ctrl+C. `membuss daemon
// status` is an alias for `ping`.
func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run and inspect the local node",
	}

	start := &cobra.Command{
		Use:   "start [daemon flags]",
		Short: "Run the node in the foreground (Ctrl+C to stop)",
		Long: `start boots the Membuss node in this process and blocks until
it receives SIGINT/SIGTERM (Ctrl+C).

It accepts the daemon's own flags, which are passed through
verbatim:
  --config <path>    YAML config file (default: <datadir>/config.yaml)
  --datadir <path>   data directory (overrides --config)
  --build <id>       build identifier reported by Ping
  --in-memory        use an in-memory store (no on-disk state)
  --no-anchor        disable the anchor engine even if config enables it

Example:
  membuss daemon start --datadir ~/.memdata
  membuss daemon start --config membuss.yaml`,
		// The daemon parses its own flags with a dedicated
		// FlagSet, so disable cobra's parser and forward the raw
		// args. This also lets `--config`/`--datadir` reach the
		// daemon instead of colliding with the CLI's persistent
		// flags of the same name.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Cobra still intercepts a bare -h/--help even with
			// DisableFlagParsing; surface usage for those.
			for _, a := range args {
				if a == "-h" || a == "--help" {
					return cmd.Help()
				}
			}
			cmd.SilenceUsage = true
			return daemon.Run(args)
		},
	}
	cmd.AddCommand(start)

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Alias for `membuss ping`",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(func(_ membusspb.MembussNodeClient, nc membusspb.NodeClient) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
				defer cancel()
				resp, err := nc.Ping(ctx, &membusspb.PingRequest{Message: "status"})
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "ok\tbuild=%s\n", resp.Build)
				return nil
			})
		},
	})
	return cmd
}

// --- helpers ---

// formatBytes renders a byte count in a human-readable form.
// It mirrors the helper in rpc/server so the CLI output stays
// consistent with what stat/gc will report.
func formatBytes(n uint64) string {
	const (
		KiB = 1 << 10
		MiB = 1 << 20
		GiB = 1 << 30
	)
	switch {
	case n >= GiB:
		return fmt.Sprintf("%.2f GiB", float64(n)/float64(GiB))
	case n >= MiB:
		return fmt.Sprintf("%.2f MiB", float64(n)/float64(MiB))
	case n >= KiB:
		return fmt.Sprintf("%.2f KiB", float64(n)/float64(KiB))
	default:
		return strconv.FormatUint(n, 10) + " B"
	}
}

// --- Phase 17: MemFS commands (HTTP) ---

// resolveAPIAddr returns the HTTP API endpoint for MemFS
// commands. Priority:
//
//  1. --api-addr flag
//  2. $MEMBUSS_API_ADDR
//  3. config.yaml's APIAddr
//  4. 127.0.0.1:5001
func resolveAPIAddr() string {
	if globalAPIAddr != "" {
		return globalAPIAddr
	}
	if v := os.Getenv("MEMBUSS_API_ADDR"); v != "" {
		return v
	}
	if datadir := config.ResolveDataDir(""); datadir != "" {
		if cfg, err := config.LoadConfig(datadir); err == nil && cfg.APIAddr != "" {
			return cfg.APIAddr
		}
	}
	if cfg, err := config.Load(globalConfigPath); err == nil && cfg.APIAddr != "" {
		return cfg.APIAddr
	}
	return "127.0.0.1:5001"
}

// httpBase returns "http://<addr>" for the API host.
func httpBase() string {
	return "http://" + resolveAPIAddr()
}

// newLsCmd implements `membuss ls <MID>`. It calls
// GET /api/v1/ls/{mid} and prints a tabwriter table.
func newLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <MID>",
		Short: "List the entries of a MemFS directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mid := args[0]
			resp, err := http.Get(httpBase() + "/api/v1/ls/" + mid)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("ls: %s: %s", resp.Status, string(body))
			}
			var env struct {
				OK   bool `json:"ok"`
				Data struct {
					Entries []struct {
						Name string `json:"name"`
						MID  string `json:"mid"`
						Type string `json:"type"`
						Size uint64 `json:"size"`
					} `json:"entries"`
				} `json:"data"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("ls: %s", env.Error)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tTYPE\tSIZE\tMID")
			for _, e := range env.Data.Entries {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, e.Type, formatBytes(e.Size), e.MID)
			}
			return tw.Flush()
		},
	}
}

// addFileHTTP is the shared POST handler for single-file
// uploads. When wrapDir is true, the daemon returns a DIR
// MID that wraps the FILE node.
func addFileHTTP(cmd *cobra.Command, path string, wrapDir bool) error {
	url := httpBase() + "/api/v1/add"
	if wrapDir {
		url += "?wrap=dir"
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	req, err := http.NewRequest("POST", url, f)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("add: %s: %s", resp.Status, string(body))
	}
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Data  struct {
			MID  string `json:"mid"`
			Size uint64 `json:"size"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return err
	}
	if !env.OK {
		return fmt.Errorf("add: %s", env.Error)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", env.Data.MID)
	return nil
}

// addDirectoryHTTP uploads a directory as multipart/form-data
// to /api/v1/add/dir. Each part carries a X-Membuss-Path
// header with the file's relative path.
func addDirectoryHTTP(cmd *cobra.Command, root string, dirName string) error {
	name := dirName
	if name == "" {
		abs, err := filepath.Abs(root)
		if err == nil {
			name = filepath.Base(abs)
		} else {
			name = filepath.Base(root)
		}
		if name == "." || name == "/" || name == "\\" || name == "" {
			name = "dist"
		}
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	count := 0
	err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="file"; filename="%s"`,
				escapeQuotes(filepath.Base(rel))))
		h.Set("Content-Type", "application/octet-stream")
		h.Set("X-Membuss-Path", rel)
		fw, err := mw.CreatePart(h)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(fw, f); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("no files in directory")
	}
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequest("POST", httpBase()+"/api/v1/add/dir?name="+url.QueryEscape(name), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("add-dir: %s: %s", resp.Status, string(body))
	}
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Data  struct {
			MID string `json:"mid"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	if !env.OK {
		return fmt.Errorf("add-dir: %s", env.Error)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", env.Data.MID)
	return nil
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}

// APIMemRoute represents the API's route payload mapping.
type APIMemRoute struct {
	Target     string            `json:"target"`
	Weight     uint32            `json:"weight"`
	Label      string            `json:"label"`
	Conditions map[string]string `json:"conditions"`
}

func newKeyRingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keyring",
		Short: "Manage MemNS signing key pairs",
	}

	var keyType string
	genCmd := &cobra.Command{
		Use:   "gen <name>",
		Short: "Generate a new named key pair",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			body := map[string]any{
				"name": name,
				"type": keyType,
			}
			buf := &bytes.Buffer{}
			if err := json.NewEncoder(buf).Encode(body); err != nil {
				return err
			}
			resp, err := http.Post(httpBase()+"/api/v1/keyring/gen", "application/json", buf)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				bodyBytes, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("gen key failed: %s: %s", resp.Status, string(bodyBytes))
			}
			var env struct {
				OK   bool `json:"ok"`
				Data struct {
					Name      string `json:"name"`
					MemNSName string `json:"memns_name"`
				} `json:"data"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("gen key: %s", env.Error)
			}
			fmt.Fprintf(c.OutOrStdout(), "Generated key %q -> %s\n", env.Data.Name, env.Data.MemNSName)
			return nil
		},
	}
	genCmd.Flags().StringVar(&keyType, "type", "ed25519", "key type: ed25519 or rsa")
	cmd.AddCommand(genCmd)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all keys + their /memns/ names",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			resp, err := http.Get(httpBase() + "/api/v1/keyring/list")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				bodyBytes, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("list keys failed: %s: %s", resp.Status, string(bodyBytes))
			}
			var env struct {
				OK   bool `json:"ok"`
				Data []struct {
					Name      string    `json:"name"`
					MemNSName string    `json:"memns_name"`
					CreatedAt time.Time `json:"created_at"`
					PublicKey string    `json:"public_key"`
				} `json:"data"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("list keys: %s", env.Error)
			}

			tw := tabwriter.NewWriter(c.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tMEMNS NAME\tCREATED AT")
			for _, k := range env.Data {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", k.Name, k.MemNSName, k.CreatedAt.Local().Format(time.RFC3339))
			}
			return tw.Flush()
		},
	}
	cmd.AddCommand(listCmd)

	exportCmd := &cobra.Command{
		Use:   "export <name>",
		Short: "Export private key (PEM)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			resp, err := http.Get(httpBase() + "/api/v1/keyring/export/" + name)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				bodyBytes, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("export key failed: %s: %s", resp.Status, string(bodyBytes))
			}
			var env struct {
				OK   bool `json:"ok"`
				Data struct {
					PEM string `json:"pem"`
				} `json:"data"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("export key: %s", env.Error)
			}
			fmt.Fprint(c.OutOrStdout(), env.Data.PEM)
			return nil
		},
	}
	cmd.AddCommand(exportCmd)

	importCmd := &cobra.Command{
		Use:   "import <name> <file>",
		Short: "Import keypair",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			file := args[1]
			pemBytes, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			body := map[string]any{
				"name": name,
				"pem":  string(pemBytes),
			}
			buf := &bytes.Buffer{}
			if err := json.NewEncoder(buf).Encode(body); err != nil {
				return err
			}
			resp, err := http.Post(httpBase()+"/api/v1/keyring/import", "application/json", buf)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				bodyBytes, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("import key failed: %s: %s", resp.Status, string(bodyBytes))
			}
			var env struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("import key: %s", env.Error)
			}
			fmt.Fprintf(c.OutOrStdout(), "Imported key %q successfully\n", name)
			return nil
		},
	}
	cmd.AddCommand(importCmd)

	rmCmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete key",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			req, err := http.NewRequest("DELETE", httpBase()+"/api/v1/keyring/rm/"+name, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				bodyBytes, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("delete key failed: %s: %s", resp.Status, string(bodyBytes))
			}
			var env struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("delete key: %s", env.Error)
			}
			fmt.Fprintf(c.OutOrStdout(), "Deleted key %q successfully\n", name)
			return nil
		},
	}
	cmd.AddCommand(rmCmd)

	return cmd
}

func newMemNSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memns",
		Short: "Manage MemNS record publishing and resolution",
	}

	var ttl uint64
	var msg string
	var routes []string

	publishCmd := &cobra.Command{
		Use:   "publish <keyname> <MID>",
		Short: "Publish a new MemNS record",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			key := args[0]
			val := args[1]

			var apiRoutes []APIMemRoute
			for _, rStr := range routes {
				parts := strings.SplitN(rStr, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid route format: %s (expected label=target:weight)", rStr)
				}
				label := parts[0]
				targetWeight := parts[1]
				twParts := strings.SplitN(targetWeight, ":", 2)
				if len(twParts) != 2 {
					return fmt.Errorf("invalid route target/weight format: %s (expected label=target:weight)", rStr)
				}
				target := twParts[0]
				var weight uint64
				if _, err := fmt.Sscan(twParts[1], &weight); err != nil {
					return fmt.Errorf("invalid weight in route: %s", rStr)
				}
				apiRoutes = append(apiRoutes, APIMemRoute{
					Target:     target,
					Weight:     uint32(weight),
					Label:      label,
					Conditions: make(map[string]string),
				})
			}

			body := map[string]any{
				"key":     key,
				"value":   val,
				"ttl":     ttl,
				"message": msg,
				"routes":  apiRoutes,
			}
			buf := &bytes.Buffer{}
			if err := json.NewEncoder(buf).Encode(body); err != nil {
				return err
			}

			resp, err := http.Post(httpBase()+"/api/v1/memns/publish", "application/json", buf)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				bodyBytes, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("publish failed: %s: %s", resp.Status, string(bodyBytes))
			}

			var env struct {
				OK   bool `json:"ok"`
				Data struct {
					Name     string `json:"name"`
					Sequence uint64 `json:"sequence"`
				} `json:"data"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("publish: %s", env.Error)
			}

			fmt.Fprintf(c.OutOrStdout(), "Published %s -> %s at sequence %d\n", env.Data.Name, val, env.Data.Sequence)
			return nil
		},
	}
	publishCmd.Flags().Uint64Var(&ttl, "ttl", 3600, "TTL hints in seconds")
	publishCmd.Flags().StringVar(&msg, "message", "", "changelog message note")
	publishCmd.Flags().StringSliceVar(&routes, "route", nil, "routing targets in format: label=target:weight")
	cmd.AddCommand(publishCmd)

	var atSeq uint64

	resolveCmd := &cobra.Command{
		Use:   "resolve <name or domain>",
		Short: "Resolve a MemNS name or domain to MID",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			nameOrDomain := args[0]

			if atSeq > 0 {
				name := nameOrDomain
				if strings.Contains(name, ".") {
					resp, err := http.Get(httpBase() + "/api/v1/memlink/resolve/" + name)
					if err != nil {
						return err
					}
					defer resp.Body.Close()
					if resp.StatusCode != 200 {
						bodyBytes, _ := io.ReadAll(resp.Body)
						return fmt.Errorf("resolve memlink failed: %s: %s", resp.Status, string(bodyBytes))
					}
					var env struct {
						OK   bool `json:"ok"`
						Data struct {
							RawTxt      string `json:"raw_txt"`
							ResolvedMID string `json:"resolved_mid"`
						} `json:"data"`
						Error string `json:"error"`
					}
					if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
						return err
					}
					if !env.OK {
						return fmt.Errorf("resolve memlink: %s", env.Error)
					}
					rec, err := memlink.ParseTXTRecord(env.Data.RawTxt)
					if err != nil {
						return err
					}
					if rec.MemNSName == "" {
						return fmt.Errorf("historical resolve requires a mutable memns target, but domain resolved to static MID: %s", env.Data.ResolvedMID)
					}
					name = rec.MemNSName
				}

				if strings.HasPrefix(name, "/memns/") {
					name = name[7:]
				}

				resp, err := http.Get(httpBase() + "/api/v1/memns/log/" + name)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 {
					bodyBytes, _ := io.ReadAll(resp.Body)
					return fmt.Errorf("resolve log failed: %s: %s", resp.Status, string(bodyBytes))
				}
				var env struct {
					OK   bool `json:"ok"`
					Data struct {
						Entries []struct {
							Sequence  uint64 `json:"sequence"`
							MID       string `json:"mid"`
							Timestamp int64  `json:"timestamp"`
							Message   string `json:"message"`
						} `json:"entries"`
					} `json:"data"`
					Error string `json:"error"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
					return err
				}
				if !env.OK {
					return fmt.Errorf("resolve log: %s", env.Error)
				}
				var found bool
				var val string
				for _, e := range env.Data.Entries {
					if e.Sequence == atSeq {
						val = e.MID
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("sequence %d not found in changelog history", atSeq)
				}
				fullName := nameOrDomain
				if !strings.HasPrefix(fullName, "/memns/") && !strings.Contains(fullName, ".") {
					fullName = "/memns/" + fullName
				}
				fmt.Fprintf(c.OutOrStdout(), "Name:     %s\n", fullName)
				fmt.Fprintf(c.OutOrStdout(), "Value:    %s\n", val)
				fmt.Fprintf(c.OutOrStdout(), "Sequence: %d\n", atSeq)
				return nil
			}

			var name string
			var isDomain bool
			if strings.Contains(nameOrDomain, ".") {
				isDomain = true
				resp, err := http.Get(httpBase() + "/api/v1/memlink/resolve/" + nameOrDomain)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 {
					bodyBytes, _ := io.ReadAll(resp.Body)
					return fmt.Errorf("resolve memlink failed: %s: %s", resp.Status, string(bodyBytes))
				}
				var env struct {
					OK   bool `json:"ok"`
					Data struct {
						RawTxt      string `json:"raw_txt"`
						ResolvedMID string `json:"resolved_mid"`
					} `json:"data"`
					Error string `json:"error"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
					return err
				}
				if !env.OK {
					return fmt.Errorf("resolve memlink: %s", env.Error)
				}

				rec, err := memlink.ParseTXTRecord(env.Data.RawTxt)
				if err == nil && rec.MemNSName != "" {
					name = rec.MemNSName
				} else {
					fmt.Fprintf(c.OutOrStdout(), "Domain:   %s\n", nameOrDomain)
					fmt.Fprintf(c.OutOrStdout(), "Value:    %s\n", env.Data.ResolvedMID)
					return nil
				}
			} else {
				name = nameOrDomain
			}

			if strings.HasPrefix(name, "/memns/") {
				name = name[7:]
			}

			resp, err := http.Get(httpBase() + "/api/v1/memns/resolve/" + name)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				bodyBytes, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("resolve failed: %s: %s", resp.Status, string(bodyBytes))
			}

			var env struct {
				OK   bool `json:"ok"`
				Data struct {
					Value    string `json:"value"`
					Sequence uint64 `json:"sequence"`
					Expires  string `json:"expires"`
					Routes   []struct {
						Target     string            `json:"target"`
						Weight     uint32            `json:"weight"`
						Label      string            `json:"label"`
						Conditions map[string]string `json:"conditions"`
					} `json:"routes"`
				} `json:"data"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("resolve: %s", env.Error)
			}

			fullName := "/memns/" + name
			if isDomain {
				fmt.Fprintf(c.OutOrStdout(), "Domain:   %s\n", nameOrDomain)
			}
			fmt.Fprintf(c.OutOrStdout(), "Name:     %s\n", fullName)
			fmt.Fprintf(c.OutOrStdout(), "Value:    %s\n", env.Data.Value)
			fmt.Fprintf(c.OutOrStdout(), "Sequence: %d\n", env.Data.Sequence)
			fmt.Fprintf(c.OutOrStdout(), "Expires:  %s\n", env.Data.Expires)
			fmt.Fprintf(c.OutOrStdout(), "TTL:      1h\n")

			if len(env.Data.Routes) > 0 {
				tw := tabwriter.NewWriter(c.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "LABEL\tTARGET\tWEIGHT")
				for _, r := range env.Data.Routes {
					fmt.Fprintf(tw, "%s\t%s\t%d\n", r.Label, r.Target, r.Weight)
				}
				_ = tw.Flush()
			}
			return nil
		},
	}
	resolveCmd.Flags().Uint64Var(&atSeq, "at-sequence", 0, "historical sequence number to resolve")
	cmd.AddCommand(resolveCmd)

	logCmd := &cobra.Command{
		Use:   "log <name>",
		Short: "Show the publishing history of a MemNS name",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			if strings.HasPrefix(name, "/memns/") {
				name = name[7:]
			}

			resp, err := http.Get(httpBase() + "/api/v1/memns/log/" + name)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				bodyBytes, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("log failed: %s: %s", resp.Status, string(bodyBytes))
			}

			var env struct {
				OK   bool `json:"ok"`
				Data struct {
					Entries []struct {
						Sequence  uint64 `json:"sequence"`
						MID       string `json:"mid"`
						Timestamp int64  `json:"timestamp"`
						Message   string `json:"message"`
					} `json:"entries"`
				} `json:"data"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("log: %s", env.Error)
			}

			tw := tabwriter.NewWriter(c.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SEQUENCE\tTIMESTAMP\tMID\tMESSAGE")
			for _, e := range env.Data.Entries {
				t := time.Unix(0, e.Timestamp).UTC().Format(time.RFC3339)
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", e.Sequence, t, e.MID, e.Message)
			}
			return tw.Flush()
		},
	}
	cmd.AddCommand(logCmd)

	delegateCmd := &cobra.Command{
		Use:   "delegate",
		Short: "Manage delegated keys authorized to publish to your MemNS name",
	}

	addDelCmd := &cobra.Command{
		Use:   "add <keyname> <pubkey-base64>",
		Short: "Authorize a delegate public key",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			pubkey := args[1]
			body := map[string]any{
				"name":     name,
				"delegate": pubkey,
			}
			buf := &bytes.Buffer{}
			if err := json.NewEncoder(buf).Encode(body); err != nil {
				return err
			}
			resp, err := http.Post(httpBase()+"/api/v1/memns/delegate/add", "application/json", buf)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				bodyBytes, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("delegate add failed: %s: %s", resp.Status, string(bodyBytes))
			}
			var env struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("delegate add: %s", env.Error)
			}
			fmt.Fprintf(c.OutOrStdout(), "Authorized delegate successfully\n")
			return nil
		},
	}
	delegateCmd.AddCommand(addDelCmd)

	rmDelCmd := &cobra.Command{
		Use:   "rm <keyname> <pubkey-base64>",
		Short: "Revoke authorization of a delegate public key",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			pubkey := args[1]
			body := map[string]any{
				"name":     name,
				"delegate": pubkey,
			}
			buf := &bytes.Buffer{}
			if err := json.NewEncoder(buf).Encode(body); err != nil {
				return err
			}
			resp, err := http.Post(httpBase()+"/api/v1/memns/delegate/rm", "application/json", buf)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				bodyBytes, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("delegate rm failed: %s: %s", resp.Status, string(bodyBytes))
			}
			var env struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("delegate rm: %s", env.Error)
			}
			fmt.Fprintf(c.OutOrStdout(), "Revoked delegate authorization successfully\n")
			return nil
		},
	}
	delegateCmd.AddCommand(rmDelCmd)

	listDelCmd := &cobra.Command{
		Use:   "list <keyname>",
		Short: "List all authorized delegates",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			keyname := args[0]
			resp, err := http.Get(httpBase() + "/api/v1/memns/delegate/list/" + keyname)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				bodyBytes, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("delegate list failed: %s: %s", resp.Status, string(bodyBytes))
			}
			var env struct {
				OK   bool `json:"ok"`
				Data struct {
					Delegates []string `json:"delegates"`
				} `json:"data"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("delegate list: %s", env.Error)
			}
			fmt.Fprintf(c.OutOrStdout(), "Delegates for key %q:\n", keyname)
			for _, d := range env.Data.Delegates {
				fmt.Fprintf(c.OutOrStdout(), "  - %s\n", d)
			}
			return nil
		},
	}
	delegateCmd.AddCommand(listDelCmd)

	cmd.AddCommand(delegateCmd)

	return cmd
}

// --- descriptor ---

func newDescriptorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "descriptor",
		Short: "BitTorrent-style .mbuss descriptor file management",
	}
	cmd.AddCommand(newDescriptorExportCmd())
	cmd.AddCommand(newDescriptorImportCmd())
	cmd.AddCommand(newDescriptorMetaCmd())
	return cmd
}

func newDescriptorExportCmd() *cobra.Command {
	var outPath string
	c := &cobra.Command{
		Use:   "export <MID> [-o file.mbuss]",
		Short: "Export a .mbuss descriptor file for a MID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			midStr := args[0]
			url := httpBase() + "/api/v1/descriptor/" + midStr
			resp, err := http.Get(url)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("export: %s: %s", resp.Status, string(body))
			}
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			if outPath == "" {
				outPath = midStr + ".mbuss"
			}
			if err := os.WriteFile(outPath, data, 0644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Exported descriptor to %s (%d bytes)\n", outPath, len(data))
			return nil
		},
	}
	c.Flags().StringVarP(&outPath, "output", "o", "", "output file path (default: <mid>.mbuss)")
	return c
}

func newDescriptorImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <file.mbuss>",
		Short: "Import a .mbuss descriptor file and verify all blocks are present",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			url := httpBase() + "/api/v1/descriptor/import"
			resp, err := http.Post(url, "application/octet-stream", bytes.NewReader(data))
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("import: %s: %s", resp.Status, string(body))
			}
			var env struct {
				OK   bool `json:"ok"`
				Data struct {
					MID string `json:"mid"`
				} `json:"data"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("import: %s", env.Error)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Imported descriptor: MID %s\n", env.Data.MID)
			return nil
		},
	}
}

func newDescriptorMetaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "meta <MID>",
		Short: "Show descriptor metadata for a MID (without block list)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			midStr := args[0]
			url := httpBase() + "/api/v1/descriptor/" + midStr + "/meta"
			resp, err := http.Get(url)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("meta: %s: %s", resp.Status, string(body))
			}
			var env struct {
				OK    bool                   `json:"ok"`
				Data  map[string]interface{} `json:"data"`
				Error string                 `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return err
			}
			if !env.OK {
				return fmt.Errorf("meta: %s", env.Error)
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(env.Data)
		},
	}
}
