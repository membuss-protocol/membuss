package dag

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/chunk"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"

	membusspb "github.com/nnlgsakib/membuss/proto"
)

// referenceBuild is the original level-complete batch algorithm,
// kept here as an independent oracle. The streaming Build in
// dag.go must produce a byte-identical root for every input.
func referenceBuild(t *testing.T, bs store.Blockstore, c chunk.Chunker) mid.MID {
	t.Helper()
	var leaves []mid.MID
	for {
		blk, err := c.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reference chunk: %v", err)
		}
		lm := blk.MID()
		if err := bs.Put(lm, blk.Data()); err != nil {
			t.Fatalf("reference put leaf: %v", err)
		}
		leaves = append(leaves, lm)
	}
	if len(leaves) == 0 {
		t.Fatal("reference: empty input")
	}
	if len(leaves) == 1 {
		return leaves[0]
	}
	current := leaves
	for len(current) > 1 {
		var next []mid.MID
		for start := 0; start < len(current); start += Fanout {
			end := start + Fanout
			if end > len(current) {
				end = len(current)
			}
			links := make([]string, end-start)
			for i, c := range current[start:end] {
				links[i] = c.String()
			}
			raw, err := proto.Marshal(&membusspb.DAGNode{Links: links})
			if err != nil {
				t.Fatalf("reference marshal: %v", err)
			}
			nm := mid.FromBytes(raw)
			if err := bs.Put(nm, raw); err != nil {
				t.Fatalf("reference put internal: %v", err)
			}
			next = append(next, nm)
		}
		current = next
	}
	return current[0]
}

// TestStreamingBuildMatchesReference is the load-bearing test for the
// streaming rewrite: it must produce the exact same root MID as the
// batch algorithm across a wide range of leaf counts, including the
// Fanout boundaries where grouping behaviour changes. A mismatch here
// means a content-addressing break in production.
func TestStreamingBuildMatchesReference(t *testing.T) {
	// Chunk size chosen so leaf count == payload/blk. Cover 1 leaf,
	// sub-fanout, exact fanout, fanout+1, multi-level, and a large
	// multi-level tree.
	const blk = 1024
	leafCounts := []int{1, 2, Fanout - 1, Fanout, Fanout + 1, 2 * Fanout, Fanout*Fanout + 3, 40000}
	for _, n := range leafCounts {
		payload := make([]byte, n*blk)
		if _, err := rand.Read(payload); err != nil {
			t.Fatalf("rand: %v", err)
		}

		refBS := store.NewMemstore()
		refChunker, err := chunk.NewFixed(blk)(bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("chunker: %v", err)
		}
		want := referenceBuild(t, refBS, refChunker)

		gotBS := store.NewMemstore()
		gotChunker, err := chunk.NewFixed(blk)(bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("chunker: %v", err)
		}
		got, err := NewBuilder(gotBS).Build(gotChunker)
		if err != nil {
			t.Fatalf("streaming Build (n=%d): %v", n, err)
		}

		if !got.Equal(want) {
			t.Fatalf("n=%d: streaming root %s != reference root %s", n, got, want)
		}

		// And the streamed content must round-trip.
		rd, err := NewResolver(gotBS).Resolve(got, nil)
		if err != nil {
			t.Fatalf("Resolve (n=%d): %v", n, err)
		}
		out, err := io.ReadAll(rd)
		if err != nil {
			t.Fatalf("ReadAll (n=%d): %v", n, err)
		}
		if !bytes.Equal(out, payload) {
			t.Fatalf("n=%d: round-trip mismatch", n)
		}
	}
}

// corruptStore wraps a Blockstore and returns substituted bytes for
// one targeted MID, simulating a corrupted or malicious store. It
// bypasses the verify-on-write guard that a real Memstore enforces.
type corruptStore struct {
	store.Blockstore
	target string
	fake   []byte
}

func (c *corruptStore) Get(m mid.MID) ([]byte, error) {
	if m.String() == c.target {
		return append([]byte(nil), c.fake...), nil
	}
	return c.Blockstore.Get(m)
}

// TestResolveDetectsCorruptedLeaf verifies that the resolver rejects a
// block whose bytes no longer hash to the claimed MID (fix #4).
func TestResolveDetectsCorruptedLeaf(t *testing.T) {
	payload := bytes.Repeat([]byte{0x11}, 4*64*1024) // multi-leaf
	bs := store.NewMemstore()
	c, err := chunk.NewFixed(64 * 1024)(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("chunker: %v", err)
	}
	root, err := NewBuilder(bs).Build(c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Find a leaf MID to corrupt by walking the real DAG.
	var leaf mid.MID
	err = store.Walk(bs, root, func(m mid.MID, isLeaf bool) error {
		if isLeaf && leaf.IsZero() {
			leaf = m
		}
		return nil
	})
	if err != nil || leaf.IsZero() {
		t.Fatalf("locate leaf: %v", err)
	}

	cs := &corruptStore{Blockstore: bs, target: leaf.String(), fake: []byte("tampered bytes not matching the MID")}
	rd, err := NewResolver(cs).Resolve(root, nil)
	if err != nil {
		t.Fatalf("Resolve constructor: %v", err)
	}
	if _, err := io.ReadAll(rd); err == nil {
		t.Fatal("expected integrity error reading a corrupted leaf, got nil")
	}
}

// TestClassificationRawLeafNotMisreadAsInternal builds a raw leaf whose
// bytes are themselves a valid serialized DAGNode-with-links, then
// confirms the resolver treats it as a leaf, not an internal node
// (fix #5). A leaf is emitted verbatim; an internal node would be
// walked and its "links" parsed.
func TestClassificationRawLeafNotMisreadAsInternal(t *testing.T) {
	// A DAGNode carrying a Data field. reduceLevel never sets Data,
	// so canonical round-trip must reject this as internal.
	withData, err := proto.Marshal(&membusspb.DAGNode{
		Links: []string{mid.FromBytes([]byte("child")).String()},
		Data:  []byte("i am actually leaf content"),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, ok := isInternalNode(withData); ok {
		t.Fatal("DAGNode with Data set must not classify as internal")
	}

	// A canonical links-only node must classify as internal.
	linksOnly, err := proto.Marshal(&membusspb.DAGNode{
		Links: []string{mid.FromBytes([]byte("child")).String()},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, ok := isInternalNode(linksOnly); !ok {
		t.Fatal("canonical links-only DAGNode must classify as internal")
	}

	// Arbitrary raw bytes are a leaf.
	if _, ok := isInternalNode([]byte("just some raw content")); ok {
		t.Fatal("arbitrary raw bytes must not classify as internal")
	}
}

// TestStatShapeSingleLeaf checks stats for a one-chunk DAG.
func TestStatShapeSingleLeaf(t *testing.T) {
	bs := store.NewMemstore()
	payload := []byte("one small chunk")
	c, err := chunk.NewFixed(chunk.DefaultBlockSize)(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("chunker: %v", err)
	}
	root, err := NewBuilder(bs).Build(c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	st, err := Stat(bs, root)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Nodes != 1 || st.Leaves != 1 || st.Internal != 0 {
		t.Fatalf("single leaf: got %+v", st)
	}
	if st.Depth != 1 || st.Width != 0 {
		t.Fatalf("single leaf depth/width: got depth=%d width=%d", st.Depth, st.Width)
	}
	if st.Bytes != uint64(len(payload)) {
		t.Fatalf("single leaf bytes: got %d want %d", st.Bytes, len(payload))
	}
}

// TestStatShapeMultiLevel checks stats for a tree tall enough to have
// more than one internal level.
func TestStatShapeMultiLevel(t *testing.T) {
	const blk = 1024
	const n = Fanout + 5 // two internal-node groups at level 1, one root
	payload := make([]byte, n*blk)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}
	bs := store.NewMemstore()
	c, err := chunk.NewFixed(blk)(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("chunker: %v", err)
	}
	root, err := NewBuilder(bs).Build(c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	st, err := Stat(bs, root)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Leaves != n {
		t.Fatalf("leaves: got %d want %d", st.Leaves, n)
	}
	// n leaves -> ceil(n/Fanout)=2 level-1 nodes -> 1 root. So 3 internal.
	if st.Internal != 3 {
		t.Fatalf("internal: got %d want 3", st.Internal)
	}
	if st.Nodes != n+3 {
		t.Fatalf("nodes: got %d want %d", st.Nodes, n+3)
	}
	if st.Depth != 3 {
		t.Fatalf("depth: got %d want 3", st.Depth)
	}
	// Widest node is a full level-1 group of Fanout children.
	if st.Width != uint64(Fanout) {
		t.Fatalf("width: got %d want %d", st.Width, Fanout)
	}
}
