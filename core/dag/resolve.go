package dag

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"

	membusspb "github.com/nnlgsakib/membuss/proto"
)

// Resolver walks a Merkle DAG stored in a Blockstore and reassembles
// the original content into a sequential io.Reader.
type Resolver struct {
	bs store.Blockstore
}

// NewResolver returns a Resolver that reads from bs.
func NewResolver(bs store.Blockstore) *Resolver {
	return &Resolver{bs: bs}
}

// Resolve returns an io.Reader that yields the bytes of the DAG
// rooted at root, in the order the chunker originally produced
// them.
//
// If root is a leaf, the returned reader yields the leaf's raw
// bytes directly. Otherwise the resolver walks the DAG depth-first
// and concatenates leaves in the order they appear in each
// internal node's Links.
//
// The optional visit hook, if non-nil, is called once for every
// internal node visited. It is provided for instrumentation and
// MUST NOT mutate the Blockstore.
func (r *Resolver) Resolve(root mid.MID, visit func(mid.MID) error) (io.Reader, error) {
	if r.bs == nil {
		return nil, errors.New("dag: nil blockstore")
	}
	if root.IsZero() {
		return nil, errors.New("dag: zero root MID")
	}

	pipeReader, pipeWriter := io.Pipe()
	go r.stream(root, visit, pipeWriter)
	return pipeReader, nil
}

// stream walks the DAG and pushes reassembled bytes into w. Any
// error is delivered to the reader via w.CloseWithError.
//
// Cycle detection is intentionally NOT performed. DAGs are
// immutable and content-addressed, so a cycle can only be
// produced by a malicious or buggy Blockstore, and a defensive
// check would also raise false positives for legitimate content
// that references the same byte sequence at multiple points.
func (r *Resolver) stream(root mid.MID, visit func(mid.MID) error, w *io.PipeWriter) {
	defer w.Close()

	var walk func(m mid.MID) error
	walk = func(m mid.MID) error {
		data, err := r.bs.Get(m)
		if err != nil {
			// Block may not be available yet (streaming
			// assembly). Retry with backoff up to 5s.
			for attempt := 0; attempt < 50; attempt++ {
				time.Sleep(100 * time.Millisecond)
				data, err = r.bs.Get(m)
				if err == nil {
					break
				}
			}
			if err != nil {
				return fmt.Errorf("dag: get %s: %w", m.String(), err)
			}
		}

		// Integrity: verify the fetched bytes actually hash to the
		// claimed MID before trusting their structure or content.
		// The Blockstore verifies on write, but the resolver must
		// not assume the store it reads from is the one it wrote to
		// (Anchor mirrors, restored backups, corrupted disks), so a
		// re-hash here is the last line of defence against silently
		// serving corrupted or substituted blocks. The comparison
		// is on the raw digest, so it is codec-agnostic.
		if err := verifyBlock(m, data); err != nil {
			return err
		}

		// Classify the block. An internal node is the canonical
		// serialization of a DAGNode carrying only Links; a leaf is
		// arbitrary raw content. proto.Unmarshal is lenient, so a
		// raw leaf can decode as a DAGNode with spurious fields.
		// isInternalNode guards against that by requiring the block
		// to round-trip to byte-identical canonical form.
		if links, ok := isInternalNode(data); ok {
			if visit != nil {
				if err := visit(m); err != nil {
					return err
				}
			}
			for _, s := range links {
				child, err := mid.Parse(s)
				if err != nil {
					return fmt.Errorf("dag: parse link %q: %w", s, err)
				}
				if err := walk(child); err != nil {
					return err
				}
			}
			return nil
		}

		// Leaf: emit its raw bytes.
		if _, err := w.Write(data); err != nil {
			return err
		}
		return nil
	}

	if err := walk(root); err != nil {
		_ = w.CloseWithError(err)
	}
}

// verifyBlock reports an error if data does not hash to the digest
// claimed by m. The check compares raw multihash digests, so it is
// independent of the codec tag carried by either side.
func verifyBlock(m mid.MID, data []byte) error {
	want, err := m.DigestBytes()
	if err != nil {
		return fmt.Errorf("dag: claim MID %s has no digest: %w", m.String(), err)
	}
	got, err := mid.FromBytes(data).DigestBytes()
	if err != nil {
		return fmt.Errorf("dag: derive digest for %s: %w", m.String(), err)
	}
	if !bytes.Equal(want, got) {
		return fmt.Errorf("dag: block %s failed integrity check: data does not hash to claimed MID", m.String())
	}
	return nil
}

// isInternalNode reports whether data is the canonical serialization
// of a DAG internal node, and if so returns its links.
//
// reduceLevel builds every internal node as proto.Marshal of a
// DAGNode whose only populated field is Links (Data and Mid unset).
// Recognising that exact shape — rather than "anything that decodes
// with at least one link" — is what makes classification robust:
//
//   - The decoded node must carry at least one link.
//   - It must carry no inline Data (that is a leaf/FILE concern, not
//     an internal DAG node).
//   - Re-marshalling the decoded node must reproduce the input bytes
//     exactly. Protobuf is not canonical for arbitrary inputs, so a
//     raw leaf that merely happens to decode as a DAGNode will almost
//     never re-encode to the identical byte string, and is correctly
//     treated as a leaf.
func isInternalNode(data []byte) (links []string, ok bool) {
	var node membusspb.DAGNode
	if err := proto.Unmarshal(data, &node); err != nil {
		return nil, false
	}
	if len(node.Links) == 0 || len(node.Data) != 0 {
		return nil, false
	}
	// Canonical round-trip: only the Links field participates in an
	// internal node's on-disk form (see reduceLevel), so re-marshal
	// from just the links and require an exact byte match.
	canonical := &membusspb.DAGNode{Links: node.Links}
	remarshaled, err := proto.Marshal(canonical)
	if err != nil {
		return nil, false
	}
	if !bytes.Equal(remarshaled, data) {
		return nil, false
	}
	return node.Links, true
}
