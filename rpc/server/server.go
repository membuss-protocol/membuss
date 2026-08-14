// The MembussNode gRPC server. All RPCs route through the
// Backend interface so the daemon can swap in real
// implementations (BadgerDB store, libp2p host, DHT, PEX,
// memex engine, anchor engine) while the server stays unit
// testable against an in-memory Backend.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/nnlgsakib/membuss/core/version"
	memex "github.com/nnlgsakib/membuss/net/memex_v2"
	membusspb "github.com/nnlgsakib/membuss/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Backend is the contract the server depends on. The daemon
// supplies a real Backend wired to its subsystems; tests
// supply an in-memory Backend.
type Backend interface {
	// Add ingests a local file. The chunker selection
	// (chunk.NewFixed or chunk.NewRabin) and chunk size are
	// honored when non-zero. If sealRoot is true, the root is
	// sealed before returning. Name and mimeType are
	// persisted as the per-MID ObjectInfo so the gateway
	// can reproduce the user-facing metadata on download.
	Add(ctx context.Context, path, chunker string, chunkSize uint32, sealRoot bool, name, mimeType string) (AddResult, error)
	// AddWithProgress ingests a local file exactly like Add,
	// invoking progressFn as bytes are read from the source so
	// the daemon can stream ingest progress to the client.
	// progressFn receives the running byte count and the total
	// file size (total is 0 when the size is unknown).
	AddWithProgress(ctx context.Context, path, chunker string, chunkSize uint32, sealRoot bool, name, mimeType string, progressFn func(processed, total uint64)) (AddResult, error)
	// AddDirWithProgress ingests a local directory as a single
	// MemFS DIR tree. The daemon walks the directory from its own
	// filesystem (path is a local directory path, exactly like
	// AddWithProgress reads a local file). name optionally
	// overrides the root directory name; when empty the directory
	// basename is used. progressFn receives the running byte count
	// aggregated across every file in the tree and the total tree
	// size (total is 0 until the walk has sized every file).
	AddDirWithProgress(ctx context.Context, path, chunker string, chunkSize uint32, sealRoot bool, name string, progressFn func(processed, total uint64)) (AddResult, error)
	// Get resolves a MID locally if present; otherwise it falls
	// back to fetching via Memex. The returned ReadCloser
	// streams the bytes.
	Get(ctx context.Context, midStr string, offset, limit uint64) (io.ReadCloser, error)
	// GetWithProgress resolves a MID locally if present;
	// otherwise it falls back to fetching via Memex.
	// progressFn is called as blocks arrive with the
	// running total of bytes received and total bytes
	// (total may be 0 until all blocks are known).
	//
	// The returned ContentMeta carries the object's
	// name / MIME type / size, resolved after any network
	// fetch completes, so the server can send them to the
	// client as a header frame. The fetch is driven live:
	// progressFn fires while blocks are still arriving, not
	// only at the end.
	GetWithProgress(ctx context.Context, midStr string, offset, limit uint64, progressFn func(update memex.ProgressUpdate)) (io.ReadCloser, ContentMeta, error)
	// Seal pins the given MID, optionally recursive.
	Seal(ctx context.Context, midStr string, recursive bool) (SealResult, error)
	// Unseal removes the pin on a MID.
	Unseal(ctx context.Context, midStr string) (uint64, error)
	// Stat returns a snapshot describing a MID.
	Stat(ctx context.Context, midStr string) (StatInfo, error)
	// Peers returns the local PEX peer table.
	Peers(limit uint32) ([]NodePeerInfo, uint32, error)
	// DHTPeek returns the providers the DHT knows for a MID.
	DHTPeek(ctx context.Context, midStr string, limit uint32) ([]NodePeerInfo, error)
	// GC runs garbage collection on the local store.
	GC(ctx context.Context, all bool) (GCInfo, error)
	// Delete recursively removes the given MID and its children from the store.
	Delete(ctx context.Context, midStr string) (DeleteResult, error)
	// AnchorStatus returns the anchor engine's stats.
	AnchorStatus() AnchorInfo
}

// AddResult is the return value of Backend.Add.
type AddResult struct {
	MID    string
	Size   uint64
	Blocks uint64
	Sealed bool
	// Name and MimeType echo back the values the
	// caller passed in (after defaults / sniffing
	// applied by the daemon) so the CLI / explorer
	// can show them without a second round-trip.
	Name     string
	MimeType string
}

// ContentMeta carries the user-facing metadata for a
// resolved object, returned by GetWithProgress once the
// content is available locally (after any network fetch).
// Fields are best-effort: Name / MimeType may be empty for
// content added by an older daemon or ingested without a
// name, in which case the client applies its own fallback.
type ContentMeta struct {
	Name     string
	MimeType string
	Size     uint64
}

// SealResult is the return value of Backend.Seal.
type SealResult struct {
	Pinned  uint64
	Already bool
}

// StatInfo is the return value of Backend.Stat.
type StatInfo struct {
	Present bool
	Size    uint64
	Blocks  uint64
	Sealed  bool
	Codec   uint64
	Erasure *ErasureInfo
	// Name and MimeType are the per-MID ObjectInfo
	// captured at Add time, or empty for content
	// added by an older daemon.
	Name          string
	MimeType      string
	Sealers       int
	AnchorSealers int
}

// ErasureInfo mirrors the ErasureInfo proto, kept separate so
// the Backend contract does not leak protobuf types to
// non-rpc callers.
type ErasureInfo struct {
	DataShards      uint32   `json:"data_shards"`
	ParityShards    uint32   `json:"parity_shards"`
	ShardMIDs       []string `json:"shard_mids,omitempty"`
	ShardsAvailable uint32   `json:"shards_available"`
	Degraded        bool     `json:"degraded"`
}

// NodePeerInfo describes a connected peer.
type NodePeerInfo struct {
	PeerID   string
	Addrs    []string
	IsAnchor bool
}

// GCInfo mirrors the GCResponse proto.
type GCInfo struct {
	BytesFreed uint64
	BlocksKept uint64
}

// DeleteResult mirrors the DeleteResponse proto.
type DeleteResult struct {
	BlocksDeleted uint64
	BytesFreed    uint64
}

// AnchorInfo mirrors the AnchorStatusResponse proto.
type AnchorInfo struct {
	PeerID     string
	UptimeSecs int64
	BlocksHeld int64
	Anchors    int32
	Backlog    int32
	Synced     int64
}

// Build is the daemon build identifier reported in PingResponse.
var Build = "dev"

// Server implements the gRPC MembussNode and Node services.
type Server struct {
	membusspb.UnimplementedMembussNodeServer
	membusspb.UnimplementedNodeServer

	Backend Backend
}

// New returns a Server that delegates to b.
func NewServer(b Backend) *Server {
	return &Server{Backend: b}
}

// Register attaches both Node and MembussNode services to the
// gRPC server.
func (s *Server) Register(g *grpc.Server) {
	membusspb.RegisterNodeServer(g, s)
	membusspb.RegisterMembussNodeServer(g, s)
}

// --- Node service ---

// Ping is a connectivity probe.
func (s *Server) Ping(ctx context.Context, req *membusspb.PingRequest) (*membusspb.PingResponse, error) {
	b := Build
	if b == "dev" || b == "" {
		b = version.String()
	}
	return &membusspb.PingResponse{Message: req.GetMessage(), Build: b}, nil
}

// --- MembussNode service ---

// Add ingests a file. The path is resolved on the daemon side.
func (s *Server) Add(ctx context.Context, req *membusspb.AddRequest) (*membusspb.AddResponse, error) {
	if req.GetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "add: path required")
	}
	res, err := s.Backend.Add(ctx, req.GetPath(), req.GetChunker(), req.GetChunkSize(), !req.GetNoSeal(), req.GetName(), req.GetMimeType())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "add: %v", err)
	}
	return &membusspb.AddResponse{
		Mid:    res.MID,
		Size:   res.Size,
		Blocks: res.Blocks,
		Sealed: res.Sealed,
	}, nil
}

// AddStream ingests a file like Add but streams ingest progress
// frames while the daemon reads and chunks it, ending with a
// single done=true frame carrying the result.
//
// Like Get, the ingest runs on its own goroutine and reports
// through a depth-1 drop-on-full channel; this (the only
// sending goroutine) drains it, keeping gRPC's single-sender
// requirement intact while never blocking the ingest on a slow
// client.
func (s *Server) AddStream(req *membusspb.AddRequest, stream membusspb.MembussNode_AddStreamServer) error {
	if req.GetPath() == "" {
		return status.Error(codes.InvalidArgument, "add: path required")
	}
	return streamAdd(stream, func(progressFn func(processed, total uint64)) (AddResult, error) {
		return s.Backend.AddWithProgress(stream.Context(), req.GetPath(), req.GetChunker(), req.GetChunkSize(), !req.GetNoSeal(), req.GetName(), req.GetMimeType(), progressFn)
	})
}

// AddDirStream ingests a local directory as a single MemFS DIR
// tree, streaming the same AddProgress frames as AddStream. The
// daemon walks the directory from its own filesystem, exactly
// as AddStream reads a local file — the client only sends the
// directory path (and optional name), never the file bytes.
func (s *Server) AddDirStream(req *membusspb.AddRequest, stream membusspb.MembussNode_AddDirStreamServer) error {
	if req.GetPath() == "" {
		return status.Error(codes.InvalidArgument, "add: path required")
	}
	return streamAdd(stream, func(progressFn func(processed, total uint64)) (AddResult, error) {
		return s.Backend.AddDirWithProgress(stream.Context(), req.GetPath(), req.GetChunker(), req.GetChunkSize(), !req.GetNoSeal(), req.GetName(), progressFn)
	})
}

// streamAdd runs an ingest that reports byte progress and drives
// the AddProgress stream shared by AddStream and AddDirStream.
//
// The ingest runs on its own goroutine and reports through a
// depth-1 drop-on-full channel; this (the only sending
// goroutine) drains it, keeping gRPC's single-sender requirement
// intact while never blocking the ingest on a slow client.
func streamAdd(stream grpc.ServerStreamingServer[membusspb.AddProgress], ingest func(progressFn func(processed, total uint64)) (AddResult, error)) error {
	type prog struct{ processed, total uint64 }
	progressCh := make(chan prog, 1)
	progressFn := func(processed, total uint64) {
		up := prog{processed: processed, total: total}
		select {
		case progressCh <- up:
		default:
			select {
			case <-progressCh:
			default:
			}
			select {
			case progressCh <- up:
			default:
			}
		}
	}

	type addResult struct {
		res AddResult
		err error
	}
	resCh := make(chan addResult, 1)
	go func() {
		res, err := ingest(progressFn)
		resCh <- addResult{res: res, err: err}
	}()

	var out addResult
	for waiting := true; waiting; {
		select {
		case p := <-progressCh:
			if err := stream.Send(&membusspb.AddProgress{
				BytesProcessed: p.processed,
				TotalBytes:     p.total,
			}); err != nil {
				return err
			}
		case out = <-resCh:
			waiting = false
		case <-stream.Context().Done():
			return status.FromContextError(stream.Context().Err()).Err()
		}
	}

	// Drain any pending progress update that arrived before completion
	for {
		select {
		case p := <-progressCh:
			if err := stream.Send(&membusspb.AddProgress{
				BytesProcessed: p.processed,
				TotalBytes:     p.total,
			}); err != nil {
				return err
			}
		default:
			goto drained
		}
	}
drained:

	if out.err != nil {
		return status.Errorf(codes.Internal, "add: %v", out.err)
	}
	return stream.Send(&membusspb.AddProgress{
		Done:   true,
		Mid:    out.res.MID,
		Size:   out.res.Size,
		Blocks: out.res.Blocks,
		Sealed: out.res.Sealed,
	})
}

// Get streams a MID's content back to the caller in chunks.
//
// The stream is shaped as: zero or more progress_only frames
// (emitted while the object is pulled from the network), then
// exactly one is_header frame carrying the resolved name /
// MIME type / total size, then the payload frames. Older
// clients that ignore the new fields still see the payload
// frames; the progress / header frames carry no data and are
// harmless to skip.
func (s *Server) Get(req *membusspb.GetRequest, stream membusspb.MembussNode_GetServer) error {
	if req.GetMid() == "" {
		return status.Error(codes.InvalidArgument, "get: mid required")
	}

	// progressFn runs on the Memex fetch goroutine; gRPC
	// streams are not safe for concurrent Send from multiple
	// goroutines, so progress updates are funnelled through a
	// channel and drained by this (the only) sending goroutine.
	// A depth-1 channel with drop-on-full keeps the fetch from
	// blocking on a slow client while still surfacing the
	// latest counts.
	progressCh := make(chan memex.ProgressUpdate, 1)
	progressFn := func(update memex.ProgressUpdate) {
		select {
		case progressCh <- update:
		default:
			select {
			case <-progressCh:
			default:
			}
			select {
			case progressCh <- update:
			default:
			}
		}
	}

	type getResult struct {
		rc   io.ReadCloser
		meta ContentMeta
		err  error
	}
	resCh := make(chan getResult, 1)
	go func() {
		rc, meta, err := s.Backend.GetWithProgress(stream.Context(), req.GetMid(), req.GetOffset(), req.GetLimit(), progressFn)
		resCh <- getResult{rc: rc, meta: meta, err: err}
	}()

	// Phase 1: forward progress frames until the fetch resolves.
	var res getResult
	for waiting := true; waiting; {
		select {
		case up := <-progressCh:
			if err := stream.Send(&membusspb.GetChunk{
				ProgressOnly:  true,
				FetchedBlocks: up.BlocksResolved,
				TotalBlocks:   up.BlocksTotal,
				BytesFetched:  up.BytesDelivered,
			}); err != nil {
				return err
			}
		case res = <-resCh:
			waiting = false
		case <-stream.Context().Done():
			return status.FromContextError(stream.Context().Err()).Err()
		}
	}
	if res.err != nil {
		return status.Errorf(codes.Internal, "get: %v", res.err)
	}
	rc := res.rc
	defer rc.Close()

	// Phase 2: header frame with the resolved metadata.
	if err := stream.Send(&membusspb.GetChunk{
		IsHeader:  true,
		Name:      res.meta.Name,
		MimeType:  res.meta.MimeType,
		TotalSize: res.meta.Size,
		Total:     res.meta.Size,
	}); err != nil {
		return err
	}

	// Phase 3: payload frames.
	const frameSize = 64 * 1024
	buf := make([]byte, frameSize)
	var index uint64
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			if err := stream.Send(&membusspb.GetChunk{
				Data:      append([]byte(nil), buf[:n]...),
				Index:     index,
				Total:     res.meta.Size,
				TotalSize: res.meta.Size,
			}); err != nil {
				return err
			}
			index++
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return status.Errorf(codes.Internal, "get: read: %v", err)
		}
	}
}

// Seal pins a MID.
func (s *Server) Seal(ctx context.Context, req *membusspb.SealRequest) (*membusspb.SealResponse, error) {
	if req.GetMid() == "" {
		return nil, status.Error(codes.InvalidArgument, "seal: mid required")
	}
	res, err := s.Backend.Seal(ctx, req.GetMid(), req.GetRecursive())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "seal: %v", err)
	}
	return &membusspb.SealResponse{Pinned: res.Pinned, Already: res.Already}, nil
}

// Unseal removes a pin.
func (s *Server) Unseal(ctx context.Context, req *membusspb.UnsealRequest) (*membusspb.UnsealResponse, error) {
	if req.GetMid() == "" {
		return nil, status.Error(codes.InvalidArgument, "unseal: mid required")
	}
	n, err := s.Backend.Unseal(ctx, req.GetMid())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "unseal: %v", err)
	}
	return &membusspb.UnsealResponse{Removed: n}, nil
}

// Stat describes a MID.
func (s *Server) Stat(ctx context.Context, req *membusspb.StatRequest) (*membusspb.StatResponse, error) {
	if req.GetMid() == "" {
		return nil, status.Error(codes.InvalidArgument, "stat: mid required")
	}
	info, err := s.Backend.Stat(ctx, req.GetMid())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "stat: %v", err)
	}
	resp := &membusspb.StatResponse{
		Present:       info.Present,
		Size:          info.Size,
		Blocks:        info.Blocks,
		Sealed:        info.Sealed,
		Codec:         info.Codec,
		Name:          info.Name,
		MimeType:      info.MimeType,
		Sealers:       int32(info.Sealers),
		AnchorSealers: int32(info.AnchorSealers),
	}
	if info.Erasure != nil {
		resp.Erasure = &membusspb.ErasureInfo{
			DataShards:   info.Erasure.DataShards,
			ParityShards: info.Erasure.ParityShards,
			ShardMids:    info.Erasure.ShardMIDs,
		}
	}
	return resp, nil
}

// Peers returns the local PEX peer table.
func (s *Server) Peers(ctx context.Context, req *membusspb.PeersRequest) (*membusspb.PeersResponse, error) {
	peers, total, err := s.Backend.Peers(req.GetLimit())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "peers: %v", err)
	}
	out := make([]*membusspb.NodePeerInfo, 0, len(peers))
	for _, p := range peers {
		out = append(out, peerInfoToProto(p))
	}
	return &membusspb.PeersResponse{Peers: out, Total: total}, nil
}

// DHTPeek asks the DHT who provides a MID.
func (s *Server) DHTPeek(ctx context.Context, req *membusspb.DHTPeekRequest) (*membusspb.DHTPeekResponse, error) {
	if req.GetMid() == "" {
		return nil, status.Error(codes.InvalidArgument, "dht peek: mid required")
	}
	provs, err := s.Backend.DHTPeek(ctx, req.GetMid(), req.GetLimit())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "dht peek: %v", err)
	}
	out := make([]*membusspb.NodePeerInfo, 0, len(provs))
	for _, p := range provs {
		out = append(out, peerInfoToProto(p))
	}
	return &membusspb.DHTPeekResponse{Providers: out}, nil
}

// GC runs garbage collection on the local store.
func (s *Server) GC(ctx context.Context, req *membusspb.GCRequest) (*membusspb.GCResponse, error) {
	info, err := s.Backend.GC(ctx, req.GetAll())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "gc: %v", err)
	}
	return &membusspb.GCResponse{BytesFreed: info.BytesFreed, BlocksKept: info.BlocksKept}, nil
}

// Delete recursively removes the given MID and its children from the store.
func (s *Server) Delete(ctx context.Context, req *membusspb.DeleteRequest) (*membusspb.DeleteResponse, error) {
	if req.GetMid() == "" {
		return nil, status.Error(codes.InvalidArgument, "delete: mid required")
	}
	res, err := s.Backend.Delete(ctx, req.GetMid())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete: %v", err)
	}
	return &membusspb.DeleteResponse{
		BlocksDeleted: res.BlocksDeleted,
		BytesFreed:    res.BytesFreed,
	}, nil
}

// AnchorStatus returns the anchor engine's stats.
func (s *Server) AnchorStatus(ctx context.Context, req *membusspb.AnchorStatusRequest) (*membusspb.AnchorStatusResponse, error) {
	info := s.Backend.AnchorStatus()
	return &membusspb.AnchorStatusResponse{
		PeerId:        info.PeerID,
		UptimeSeconds: info.UptimeSecs,
		BlocksHeld:    info.BlocksHeld,
		Anchors:       info.Anchors,
		Backlog:       info.Backlog,
		Synced:        info.Synced,
	}, nil
}

func peerInfoToProto(p NodePeerInfo) *membusspb.NodePeerInfo {
	return &membusspb.NodePeerInfo{
		PeerId:   p.PeerID,
		Addrs:    append([]string(nil), p.Addrs...),
		IsAnchor: p.IsAnchor,
	}
}

// Helper used by the daemon Backend: turn a generic error into
// a gRPC status. Kept in this file so Backend implementations
// can import a single package.
func ToStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Internal, err.Error())
}

// ErrNotImplemented is returned by the noop backend for methods
// the daemon has not wired up.
var ErrNotImplemented = errors.New("rpc: not implemented")

// formatBytes is a small helper that callers can use to render
// sizes consistently in CLI output.
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
		return fmt.Sprintf("%d B", n)
	}
}

// FormatBytes is exported for reuse by the CLI command printers.
func FormatBytes(n uint64) string { return formatBytes(n) }
