package manifest_rpc

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	libp2pquic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/erasure"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

func newTestHost(t *testing.T) host.Host {
	t.Helper()
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tcpAddr, _ := multiaddr.NewMultiaddr("/ip4/127.0.0.1/tcp/0")
	quicAddr, _ := multiaddr.NewMultiaddr("/ip4/127.0.0.1/udp/0/quic-v1")
	h, err := libp2p.New(
		libp2p.Identity(priv),
		libp2p.ListenAddrs(tcpAddr, quicAddr),
		libp2p.Transport(tcp.NewTCPTransport),
		libp2p.Transport(libp2pquic.NewTransport),
		libp2p.Security(noise.ID, noise.New),
	)
	if err != nil {
		t.Fatalf("libp2p.New: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// encodeFixture erasure-encodes sample data into bs (shards + manifest) and
// returns the manifest plus the original MID.
func encodeFixture(t *testing.T, bs store.Blockstore) (*membusspb.ErasureManifest, mid.MID) {
	t.Helper()

	cfg, err := erasure.NewConfig(4, 2)
	if err != nil {
		t.Fatalf("erasure config: %v", err)
	}
	enc, err := erasure.NewEncoder(cfg)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}

	data := bytes.Repeat([]byte("membuss-manifest-rpc"), 512)
	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	for _, sh := range encoded.Shards {
		if err := bs.Put(sh.ShardMID, sh.Data); err != nil {
			t.Fatalf("put shard: %v", err)
		}
	}
	if err := erasure.SetManifest(bs, encoded.OriginalMID, encoded.Manifest); err != nil {
		t.Fatalf("set manifest: %v", err)
	}
	return encoded.Manifest, encoded.OriginalMID
}

func connect(t *testing.T, a, b host.Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Connect(ctx, peer.AddrInfo{ID: b.ID(), Addrs: b.Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}
}

func mustMID(t *testing.T) mid.MID {
	t.Helper()
	data := bytes.Repeat([]byte("no-manifest"), 64)
	sum := sha256.Sum256(data)
	return mid.FromBytes(sum[:])
}

func TestManifestRoundtrip(t *testing.T) {
	server := newTestHost(t)
	client := newTestHost(t)

	bs := store.NewMemstore()
	want, origMID := encodeFixture(t, bs)

	svc := NewService(server, bs)
	defer svc.Close()

	connect(t, client, server)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := FetchManifest(ctx, client, server.ID(), origMID)
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	if got.Manifest == nil {
		t.Fatal("expected manifest, got nil")
	}
	if !proto.Equal(got.Manifest, want) {
		t.Fatal("fetched manifest differs from stored manifest")
	}
	if got.RootMid != "" {
		t.Fatalf("expected empty root hint without a linkage row, got %q", got.RootMid)
	}
	if got.ServedBy != server.ID() {
		t.Fatalf("ServedBy = %s, want %s", got.ServedBy, server.ID())
	}
}

// TestManifestRootHintRoundtrip verifies the serving peer attaches its
// local erasure_root linkage row to the response so the client can
// discover shard holders via the DHT.
func TestManifestRootHintRoundtrip(t *testing.T) {
	server := newTestHost(t)
	client := newTestHost(t)

	bs := store.NewMemstore()
	want, origMID := encodeFixture(t, bs)
	root := mid.FromBytes([]byte("shardset-link-root"))
	if err := erasure.SetManifestRoot(bs, origMID, root); err != nil {
		t.Fatalf("set root link: %v", err)
	}

	svc := NewService(server, bs)
	defer svc.Close()

	connect(t, client, server)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := FetchManifest(ctx, client, server.ID(), origMID)
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	if got.Manifest == nil || !proto.Equal(got.Manifest, want) {
		t.Fatal("fetched manifest differs from stored manifest")
	}
	if got.RootMid != root.String() {
		t.Fatalf("RootMid = %q, want %q", got.RootMid, root.String())
	}
}

func TestManifestNotFound(t *testing.T) {
	server := newTestHost(t)
	client := newTestHost(t)

	svc := NewService(server, store.NewMemstore())
	defer svc.Close()

	connect(t, client, server)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := FetchManifest(ctx, client, server.ID(), mustMID(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Manifest != nil {
		t.Fatalf("expected nil manifest on not-found, got %+v", got.Manifest)
	}
	if got.RootMid != "" || got.ServedBy != "" {
		t.Fatalf("expected zero result, got %+v", got)
	}
}

func TestFetchFromPeersSkipsUnreachable(t *testing.T) {
	server := newTestHost(t)
	client := newTestHost(t)

	bs := store.NewMemstore()
	want, origMID := encodeFixture(t, bs)
	svc := NewService(server, bs)
	defer svc.Close()

	connect(t, client, server)

	// A syntactically valid peer ID with no addresses in the client's
	// peerstore: the dial fails immediately and gets skipped.
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	deadID, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatalf("peer id: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got := FetchManifestFromPeers(ctx, client, []peer.ID{deadID, server.ID()}, origMID, 500*time.Millisecond)
	if got.Manifest == nil {
		t.Fatal("expected manifest from reachable peer")
	}
	if got.ServedBy != server.ID() {
		t.Fatalf("ServedBy = %s, want %s", got.ServedBy, server.ID())
	}
	if !proto.Equal(got.Manifest, want) {
		t.Fatal("fetched manifest differs from stored manifest")
	}
}
