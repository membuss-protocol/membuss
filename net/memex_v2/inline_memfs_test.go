package memex_v2

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/ingest"
	"github.com/nnlgsakib/membuss/core/memfs"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

func TestSession_InlineMemFSResolution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Provider node with inline edge function file
	provHost := newTestHost(t)
	defer provHost.Close()
	provEng, provStore := newTestEngine(t, provHost)
	defer provEng.Stop()

	codeData := []byte("export default function handler(req) { return { status: 200, body: 'ok' }; }")
	ingestRes, err := ingest.IngestFile(ctx, provStore, bytes.NewReader(codeData), ingest.Options{
		Name: "api/test.js",
		Seal: true,
	})
	if err != nil {
		t.Fatalf("ingest code: %v", err)
	}

	rootMID := ingestRes.MID
	if rootMID.Codec() != mid.CodecMemFS {
		t.Fatalf("expected CodecMemFS, got %x", rootMID.Codec())
	}

	// 2. Fetcher node (empty store)
	fetchHost := newTestHost(t)
	defer fetchHost.Close()
	fetchEng, fetchStore := newTestEngine(t, fetchHost)
	defer fetchEng.Stop()

	// Connect hosts
	fetchHost.Peerstore().AddAddrs(provHost.ID(), provHost.Addrs(), time.Hour)
	provHost.Peerstore().AddAddrs(fetchHost.ID(), fetchHost.Addrs(), time.Hour)
	if err := fetchHost.Connect(ctx, peer.AddrInfo{ID: provHost.ID(), Addrs: provHost.Addrs()}); err != nil {
		t.Fatalf("connect hosts: %v", err)
	}

	sess, err := NewSession(SessionConfig{
		Engine:    fetchEng,
		Root:      rootMID,
		Providers: []peer.AddrInfo{{ID: provHost.ID(), Addrs: provHost.Addrs()}},
		Timeout:   3 * time.Second,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	dataReader, err := sess.Fetch(ctx)
	if err != nil {
		t.Fatalf("session fetch: %v", err)
	}
	if dataReader == nil {
		t.Fatalf("session fetch returned nil reader")
	}

	// Verify root MemFSNode is in fetcher store
	hasRoot, err := fetchStore.Has(rootMID)
	if err != nil || !hasRoot {
		t.Fatalf("fetchStore missing root MID %s", rootMID)
	}

	// Verify store.Walk succeeds on the fetcher store (DAG is complete)
	walkVisited := 0
	err = store.Walk(fetchStore, rootMID, func(m mid.MID, leaf bool) error {
		walkVisited++
		return nil
	})
	if err != nil {
		t.Fatalf("store.Walk failed: %v", err)
	}
	if walkVisited == 0 {
		t.Fatalf("store.Walk visited 0 nodes")
	}

	// Verify MemFS reader can read the file data on the fetcher
	r, err := memfs.NewResolver(fetchStore).Open(ctx, rootMID)
	if err != nil {
		t.Fatalf("memfs.NewResolver.Open on fetched node: %v", err)
	}
	defer r.Close()

	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read from memfs reader: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), codeData) {
		t.Fatalf("content mismatch: got %q, want %q", buf.String(), string(codeData))
	}
}
