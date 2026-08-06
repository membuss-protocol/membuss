package erasure

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"sync"

	"github.com/klauspost/reedsolomon"
	"github.com/multiformats/go-multihash"

	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

// DefaultDataShards is the default number of data shards.
const DefaultDataShards = 10

// DefaultParityShards is the default number of parity shards.
const DefaultParityShards = 4

// MinShards is the smallest data-shard count accepted by NewEncoder.
const MinShards = 2

// MaxShards is the largest data+parity count accepted by NewEncoder.
const MaxShards = 256

// Config describes an erasure-coding configuration.
type Config struct {
	DataShards   int
	ParityShards int
}

// DefaultConfig returns a Config with the default configuration.
func DefaultConfig() Config {
	return Config{DataShards: DefaultDataShards, ParityShards: DefaultParityShards}
}

// AdaptiveConfig returns an optimized shard configuration based on content size to minimize overhead.
func AdaptiveConfig(size int64) Config {
	if size <= 0 {
		return DefaultConfig()
	}
	if size < 64*1024 { // < 64KB
		return Config{DataShards: 2, ParityShards: 1}
	}
	if size < 1024*1024 { // < 1MB
		return Config{DataShards: 4, ParityShards: 2}
	}
	if size < 10*1024*1024 { // < 10MB
		return Config{DataShards: 8, ParityShards: 3}
	}
	return DefaultConfig()
}

// NewConfig returns a validated Config or an error.
func NewConfig(data, parity int) (Config, error) {
	if data < MinShards {
		return Config{}, fmt.Errorf("erasure: data shards %d below minimum %d", data, MinShards)
	}
	if parity < 0 {
		return Config{}, fmt.Errorf("erasure: parity shards must be non-negative")
	}
	if data+parity > MaxShards {
		return Config{}, fmt.Errorf("erasure: total shards %d above maximum %d", data+parity, MaxShards)
	}
	return Config{DataShards: data, ParityShards: parity}, nil
}

// Shard is one erasure-coded shard of a block.
type Shard struct {
	Index    int
	Data     []byte
	ShardMID mid.MID
	Original mid.MID
}

// Encoded is the result of encoding a block.
type Encoded struct {
	OriginalMID  mid.MID
	OriginalSize int
	Shards       []Shard
	Manifest     *membusspb.ErasureManifest
}

// Encoder applies a Config using reedsolomon.
type Encoder struct {
	cfg Config
	enc reedsolomon.Encoder
}

// NewEncoder returns an Encoder that uses the given config.
func NewEncoder(cfg Config) (*Encoder, error) {
	if _, err := NewConfig(cfg.DataShards, cfg.ParityShards); err != nil {
		return nil, err
	}
	enc, err := reedsolomon.New(cfg.DataShards, cfg.ParityShards)
	if err != nil {
		return nil, fmt.Errorf("erasure: build encoder: %w", err)
	}
	return &Encoder{cfg: cfg, enc: enc}, nil
}

// bufferPool recycles byte slices to avoid intermediate allocations and reduce GC pressure.
var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 256*1024) // 256KB buffers
	},
}

// streamHasher wraps a multihash hasher matching a specific algorithm.
type streamHasher struct {
	alg uint64
	h   hash.Hash
}

func newStreamHasher(alg uint64) (*streamHasher, error) {
	if alg == 0 {
		alg = mid.GetDefaultHash()
	}
	h, err := multihash.GetHasher(alg)
	if err != nil {
		alg = multihash.SHA2_256
		h = sha256.New()
	}
	return &streamHasher{alg: alg, h: h}, nil
}

func (s *streamHasher) Write(p []byte) (n int, err error) {
	return s.h.Write(p)
}

func (s *streamHasher) SumMID(codec uint64) (mid.MID, error) {
	sum := s.h.Sum(nil)
	mh, err := multihash.Encode(sum, s.alg)
	if err != nil {
		return mid.MID{}, err
	}
	return mid.FromCodecAndHash(1, codec, mh), nil
}

// Encode splits data into shards and produces the parity shards.
func (e *Encoder) Encode(data []byte) (*Encoded, error) {
	if len(data) == 0 {
		return nil, errors.New("erasure: cannot encode empty block")
	}
	original := mid.FromBytes(data)

	shards, err := e.enc.Split(data)
	if err != nil {
		return nil, fmt.Errorf("erasure: split: %w", err)
	}
	if err := e.enc.Encode(shards); err != nil {
		return nil, fmt.Errorf("erasure: encode: %w", err)
	}

	total := e.cfg.DataShards + e.cfg.ParityShards
	out := &Encoded{
		OriginalMID:  original,
		OriginalSize: len(data),
		Shards:       make([]Shard, total),
		Manifest: &membusspb.ErasureManifest{
			OriginalMid:  original.String(),
			DataShards:   uint32(e.cfg.DataShards),
			ParityShards: uint32(e.cfg.ParityShards),
			OriginalSize: uint64(len(data)),
		},
	}
	for i, raw := range shards {
		s := Shard{
			Index:    i,
			Data:     raw,
			Original: original,
		}
		s.ShardMID = mid.FromBytes(raw)
		out.Shards[i] = s
		out.Manifest.ShardMids = append(out.Manifest.ShardMids, s.ShardMID.String())
	}
	return out, nil
}

// Decode reconstructs the original block from a (possibly incomplete) shard set.
// Before reconstructing, it validates each shard against its expected MID in the manifest
// to prevent corrupted shards from producing garbage output silently.
func (e *Encoder) Decode(shards [][]byte, manifest *membusspb.ErasureManifest) ([]byte, error) {
	if manifest == nil {
		return nil, errors.New("erasure: nil manifest")
	}
	if int(manifest.DataShards) != e.cfg.DataShards {
		return nil, fmt.Errorf("erasure: manifest data shards %d, encoder %d", manifest.DataShards, e.cfg.DataShards)
	}
	if int(manifest.ParityShards) != e.cfg.ParityShards {
		return nil, fmt.Errorf("erasure: manifest parity shards %d, encoder %d", manifest.ParityShards, e.cfg.ParityShards)
	}
	total := e.cfg.DataShards + e.cfg.ParityShards
	if len(shards) != total {
		return nil, fmt.Errorf("erasure: got %d shards, want %d", len(shards), total)
	}

	// Verify each present shard's integrity before passing to reedsolomon
	for i, s := range shards {
		if s != nil {
			if i >= len(manifest.ShardMids) {
				return nil, fmt.Errorf("erasure: shard index %d exceeds manifest MIDs", i)
			}
			if !VerifyShard(s, manifest.ShardMids[i]) {
				// Mark corrupted shard as nil so it gets reconstructed safely
				shards[i] = nil
			}
		}
	}

	if err := e.enc.Reconstruct(shards); err != nil {
		return nil, fmt.Errorf("erasure: reconstruct: %w", err)
	}

	var buf bytes.Buffer
	if err := e.enc.Join(&buf, shards, len(shards[0])*e.cfg.DataShards); err != nil {
		return nil, fmt.Errorf("erasure: join: %w", err)
	}
	if buf.Len() < int(manifest.OriginalSize) {
		return nil, errors.New("erasure: reconstructed buffer smaller than original size")
	}
	trimmed := buf.Bytes()[:manifest.OriginalSize]

	want, err := mid.Parse(manifest.OriginalMid)
	if err != nil {
		return nil, fmt.Errorf("erasure: parse manifest original MID: %w", err)
	}
	got := mid.FromBytes(trimmed)
	if !got.Equal(want) {
		return nil, errors.New("erasure: recovered bytes do not match manifest original MID")
	}
	return trimmed, nil
}

// Verify checks that a complete shard set is internally consistent.
func (e *Encoder) Verify(shards [][]byte) (bool, error) {
	return e.enc.Verify(shards)
}

// VerifyPartial verifies the consistency of an incomplete shard set.
// It requires at least DataShards to be present.
func (e *Encoder) VerifyPartial(shards [][]byte) (bool, error) {
	total := e.cfg.DataShards + e.cfg.ParityShards
	if len(shards) != total {
		return false, fmt.Errorf("erasure: got %d shards, want %d", len(shards), total)
	}

	present := 0
	shardsCopy := make([][]byte, total)
	for i, s := range shards {
		if s != nil {
			present++
			shardsCopy[i] = append([]byte(nil), s...)
		}
	}

	if present < e.cfg.DataShards {
		return false, fmt.Errorf("erasure: insufficient shards to verify (%d present, need %d)", present, e.cfg.DataShards)
	}

	if err := e.enc.Reconstruct(shardsCopy); err != nil {
		return false, fmt.Errorf("erasure: reconstruct for verify: %w", err)
	}

	return e.enc.Verify(shardsCopy)
}

// VerifyShard checks the integrity of a single shard against its expected MID.
func VerifyShard(shardData []byte, expectedMIDStr string) bool {
	expectedMID, err := mid.Parse(expectedMIDStr)
	if err != nil {
		return false
	}
	gotMID := mid.FromBytes(shardData)
	return gotMID.Equal(expectedMID)
}

// InlineValidator verifies shards as they are transferred/received one by one.
type InlineValidator struct {
	manifest *membusspb.ErasureManifest
}

// NewInlineValidator returns a new validator for a transfer session.
func NewInlineValidator(manifest *membusspb.ErasureManifest) *InlineValidator {
	return &InlineValidator{manifest: manifest}
}

// Verify checks a single incoming shard inline.
func (v *InlineValidator) Verify(index int, data []byte) bool {
	if v.manifest == nil || index < 0 || index >= len(v.manifest.ShardMids) {
		return false
	}
	return VerifyShard(data, v.manifest.ShardMids[index])
}

// EncodeStream reads from r and writes erasure-coded shards to the output writers in chunks.
// This supports streaming blocks of arbitrary size (e.g. 1GB+) without high memory overhead.
func (e *Encoder) EncodeStream(r io.Reader, writers []io.Writer) (*membusspb.ErasureManifest, error) {
	total := e.cfg.DataShards + e.cfg.ParityShards
	if len(writers) != total {
		return nil, fmt.Errorf("erasure: got %d writers, want %d", len(writers), total)
	}

	// We stream chunk by chunk. To keep it compatible with reedsolomon,
	// we read a block chunk, run reedsolomon split/encode, and write to each writer.
	// We'll use 64KB chunks for data shards.
	chunkSize := 64 * 1024
	buf := make([]byte, chunkSize)
	
	// Create multi-hash for the original stream to compute original MID
	defaultAlg := mid.GetDefaultHash()
	hasher, err := newStreamHasher(defaultAlg)
	if err != nil {
		return nil, fmt.Errorf("erasure: stream hasher: %w", err)
	}
	var originalSize uint64
	var shardMids []string

	// Initialize individual shard hashers to compute shard MIDs over the entire stream
	shardHashers := make([]*streamHasher, total)
	for i := range shardHashers {
		sHasher, shErr := newStreamHasher(defaultAlg)
		if shErr != nil {
			return nil, fmt.Errorf("erasure: shard hasher %d: %w", i, shErr)
		}
		shardHashers[i] = sHasher
	}

	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			originalSize += uint64(n)
			_, _ = hasher.Write(buf[:n])

			// Pad chunk if it's the last one and incomplete
			chunkData := buf[:n]
			if n < chunkSize {
				// zero pad to chunkSize so shard sizes match
				padding := make([]byte, chunkSize-n)
				chunkData = append(chunkData, padding...)
			}

			shards, encErr := e.enc.Split(chunkData)
			if encErr != nil {
				return nil, fmt.Errorf("erasure: stream split: %w", encErr)
			}
			if encErr := e.enc.Encode(shards); encErr != nil {
				return nil, fmt.Errorf("erasure: stream encode: %w", encErr)
			}

			// Write each chunk shard to its corresponding writer and update shard hashers
			for i, shard := range shards {
				if _, werr := writers[i].Write(shard); werr != nil {
					return nil, fmt.Errorf("erasure: stream write shard %d: %w", i, werr)
				}
				_, _ = shardHashers[i].Write(shard)
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, err
		}
	}

	originalMID, err := hasher.SumMID(mid.CodecRaw)
	if err != nil {
		return nil, fmt.Errorf("erasure: stream sum original: %w", err)
	}

	for i := range shardHashers {
		sMID, err := shardHashers[i].SumMID(mid.CodecRaw)
		if err != nil {
			return nil, fmt.Errorf("erasure: stream sum shard %d: %w", i, err)
		}
		shardMids = append(shardMids, sMID.String())
	}

	return &membusspb.ErasureManifest{
		OriginalMid:  originalMID.String(),
		DataShards:   uint32(e.cfg.DataShards),
		ParityShards: uint32(e.cfg.ParityShards),
		OriginalSize: originalSize,
		ShardMids:    shardMids,
	}, nil
}

// DecodeStream reconstructs the original stream from the incomplete shard readers.
// Reads chunk by chunk to maintain O(1) memory overhead.
func (e *Encoder) DecodeStream(readers []io.Reader, w io.Writer, manifest *membusspb.ErasureManifest) error {
	if manifest == nil {
		return errors.New("erasure: nil manifest")
	}
	total := e.cfg.DataShards + e.cfg.ParityShards
	if len(readers) != total {
		return fmt.Errorf("erasure: got %d readers, want %d", len(readers), total)
	}

	chunkSize := 64 * 1024
	shardSize := chunkSize / e.cfg.DataShards
	shards := make([][]byte, total)
	for i := range shards {
		shards[i] = make([]byte, shardSize)
	}

	wantMID, err := mid.Parse(manifest.OriginalMid)
	if err != nil {
		return fmt.Errorf("erasure: parse expected original MID: %w", err)
	}

	decodedMh, err := multihash.Decode(wantMID.Hash)
	if err != nil {
		return fmt.Errorf("erasure: decode manifest multihash: %w", err)
	}

	hasher, err := newStreamHasher(decodedMh.Code)
	if err != nil {
		return fmt.Errorf("erasure: stream hasher: %w", err)
	}

	var remainingBytes = manifest.OriginalSize

	for remainingBytes > 0 {
		// Read one chunk's worth of shards
		presentShards := make([][]byte, total)
		for i, r := range readers {
			if r != nil {
				n, err := io.ReadFull(r, shards[i])
				if err == nil && n == shardSize {
					presentShards[i] = shards[i]
				} else {
					presentShards[i] = nil
				}
			} else {
				presentShards[i] = nil
			}
		}

		if err := e.enc.Reconstruct(presentShards); err != nil {
			return fmt.Errorf("erasure: stream reconstruct: %w", err)
		}

		var buf bytes.Buffer
		if err := e.enc.Join(&buf, presentShards, chunkSize); err != nil {
			return fmt.Errorf("erasure: stream join: %w", err)
		}

		chunkBytes := buf.Bytes()
		toWrite := len(chunkBytes)
		if uint64(toWrite) > remainingBytes {
			toWrite = int(remainingBytes)
		}

		dataChunk := chunkBytes[:toWrite]
		if _, werr := w.Write(dataChunk); werr != nil {
			return fmt.Errorf("erasure: stream write: %w", werr)
		}
		_, _ = hasher.Write(dataChunk)
		remainingBytes -= uint64(toWrite)
	}

	// Verify the final reconstructed stream matches manifest original MID
	gotMID, err := hasher.SumMID(mid.CodecRaw)
	if err != nil {
		return fmt.Errorf("erasure: stream sum original: %w", err)
	}
	if !gotMID.Equal(wantMID) {
		return errors.New("erasure: stream recovered bytes do not match expected MID")
	}

	return nil
}
