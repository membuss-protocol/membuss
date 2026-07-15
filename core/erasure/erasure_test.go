package erasure

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	"github.com/nnlgsakib/membuss/core/mid"
)

func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		data    int
		parity  int
		wantErr bool
	}{
		{"defaults", 10, 4, false},
		{"min", MinShards, 0, false},
		{"too few data", 1, 4, true},
		{"negative parity", 10, -1, true},
		{"too many", MaxShards, 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewConfig(c.data, c.parity)
			if (err != nil) != c.wantErr {
				t.Fatalf("NewConfig(%d,%d) err=%v wantErr=%v", c.data, c.parity, err, c.wantErr)
			}
		})
	}
}

func TestEncodeDefaultsDeterministic(t *testing.T) {
	enc, err := NewEncoder(DefaultConfig())
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	data := []byte("hello, membuss!")
	a, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("Encode a: %v", err)
	}
	b, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("Encode b: %v", err)
	}
	if !a.OriginalMID.Equal(b.OriginalMID) {
		t.Fatal("OriginalMID must be deterministic for the same input")
	}
	if len(a.Shards) != 14 {
		t.Fatalf("Shards len = %d, want 14", len(a.Shards))
	}
	if a.Manifest.DataShards != 10 || a.Manifest.ParityShards != 4 {
		t.Fatalf("manifest shards = %d+%d, want 10+4", a.Manifest.DataShards, a.Manifest.ParityShards)
	}
	if len(a.Manifest.ShardMids) != 14 {
		t.Fatalf("manifest shard_mids len = %d, want 14", len(a.Manifest.ShardMids))
	}
}

func TestEncodeRejectsEmpty(t *testing.T) {
	enc, _ := NewEncoder(DefaultConfig())
	if _, err := enc.Encode(nil); err == nil {
		t.Fatal("Encode of nil must fail")
	}
}

// TestEncodeDifferentDataDifferentMIDs uses inputs that fill
// every data shard with distinct bytes, so no two encodings
// produce identical shards. (Short inputs leave some data
// shards as pure zero bytes, which legitimately hash the same.)
func TestEncodeDifferentDataDifferentMIDs(t *testing.T) {
	enc, _ := NewEncoder(DefaultConfig())
	a := bytes.Repeat([]byte{0xAA}, 10*1024) // 10 KiB, fills all 10 data shards
	b := bytes.Repeat([]byte{0xBB}, 10*1024)
	outA, err := enc.Encode(a)
	if err != nil {
		t.Fatalf("Encode a: %v", err)
	}
	outB, err := enc.Encode(b)
	if err != nil {
		t.Fatalf("Encode b: %v", err)
	}
	if outA.OriginalMID.Equal(outB.OriginalMID) {
		t.Fatal("distinct content must yield distinct original MIDs")
	}
	for i := range outA.Shards {
		if outA.Shards[i].ShardMID.Equal(outB.Shards[i].ShardMID) {
			t.Fatalf("shard %d MIDs collide for distinct content", i)
		}
	}
}

func TestShardSizesUniform(t *testing.T) {
	enc, _ := NewEncoder(DefaultConfig())
	data := make([]byte, 1024)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand: %v", err)
	}
	out, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := len(out.Shards[0].Data)
	for i, s := range out.Shards {
		if len(s.Data) != want {
			t.Fatalf("shard %d size = %d, want %d (uniform)", i, len(s.Data), want)
		}
	}
}

// TestEncodeDecodeRoundTrip is the headline integration test:
// encode 1 MiB, drop 4 shards, decode, verify the recovered
// bytes match the original.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	enc, err := NewEncoder(DefaultConfig())
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	const total = 1 * 1024 * 1024
	data := make([]byte, total)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand: %v", err)
	}

	out, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	shards := make([][]byte, len(out.Shards))
	for i, s := range out.Shards {
		shards[i] = s.Data
	}
	shards[10] = nil
	shards[11] = nil
	shards[12] = nil
	shards[13] = nil

	missing := 0
	for _, s := range shards {
		if s == nil {
			missing++
		}
	}
	if missing != 4 {
		t.Fatalf("missing shards = %d, want 4", missing)
	}

	got, err := enc.Decode(shards, out.Manifest)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("decoded bytes do not match original")
	}
	if len(got) != total {
		t.Fatalf("decoded len = %d, want %d", len(got), total)
	}
}

// TestDecodeExceedsBudgetFails: with RS(10,4) we can lose 4
// shards; 5 missing must fail.
func TestDecodeExceedsBudgetFails(t *testing.T) {
	enc, _ := NewEncoder(DefaultConfig())
	data := make([]byte, 1024)
	out, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	shards := make([][]byte, len(out.Shards))
	for i, s := range out.Shards {
		shards[i] = s.Data
	}
	shards[9] = nil
	shards[10] = nil
	shards[11] = nil
	shards[12] = nil
	shards[13] = nil
	if _, err := enc.Decode(shards, out.Manifest); err == nil {
		t.Fatal("Decode with 5 of 14 shards missing must fail")
	}
}

func TestVerifyAcceptsCompleteSet(t *testing.T) {
	enc, _ := NewEncoder(DefaultConfig())
	data := make([]byte, 1024)
	out, _ := enc.Encode(data)
	shards := make([][]byte, len(out.Shards))
	for i, s := range out.Shards {
		shards[i] = s.Data
	}
	ok, err := enc.Verify(shards)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("Verify of complete shard set must be true")
	}
}

func TestVerifyRejectsTamperedData(t *testing.T) {
	enc, _ := NewEncoder(DefaultConfig())
	data := make([]byte, 1024)
	out, _ := enc.Encode(data)
	shards := make([][]byte, len(out.Shards))
	for i, s := range out.Shards {
		shards[i] = s.Data
	}
	shards[0][0] ^= 0xFF
	ok, err := enc.Verify(shards)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Fatal("Verify of tampered shard set must be false")
	}
}

func TestShardMIDMatchesData(t *testing.T) {
	enc, _ := NewEncoder(DefaultConfig())
	data := make([]byte, 256)
	out, _ := enc.Encode(data)
	for i, s := range out.Shards {
		if s.ShardMID.IsZero() {
			t.Fatalf("shard %d has zero MID", i)
		}
		want := mid.FromBytes(s.Data)
		if !s.ShardMID.Equal(want) {
			t.Fatalf("shard %d MID = %s, want %s", i, s.ShardMID, want)
		}
	}
}

func TestManifestRoundTrip(t *testing.T) {
	enc, _ := NewEncoder(DefaultConfig())
	data := make([]byte, 512)
	out, _ := enc.Encode(data)
	if got := out.Manifest.OriginalSize; got != uint64(len(data)) {
		t.Fatalf("manifest OriginalSize = %d, want %d", got, len(data))
	}
	if len(out.Manifest.ShardMids) != 14 {
		t.Fatalf("manifest ShardMids len = %d, want 14", len(out.Manifest.ShardMids))
	}
	for i, smid := range out.Manifest.ShardMids {
		if mid.MustParse(smid).String() != out.Shards[i].ShardMID.String() {
			t.Fatalf("shard %d manifest MID %s != shards[%d].ShardMID %s", i, smid, i, out.Shards[i].ShardMID)
		}
	}
}

func TestAdaptiveConfig(t *testing.T) {
	cases := []struct {
		size   int64
		data   int
		parity int
	}{
		{0, DefaultDataShards, DefaultParityShards},
		{-10, DefaultDataShards, DefaultParityShards},
		{10 * 1024, 2, 1},
		{500 * 1024, 4, 2},
		{5 * 1024 * 1024, 8, 3},
		{50 * 1024 * 1024, DefaultDataShards, DefaultParityShards},
	}
	for _, c := range cases {
		cfg := AdaptiveConfig(c.size)
		if cfg.DataShards != c.data || cfg.ParityShards != c.parity {
			t.Errorf("AdaptiveConfig(%d) = (%d, %d), want (%d, %d)", c.size, cfg.DataShards, cfg.ParityShards, c.data, c.parity)
		}
	}
}

func TestVerifyPartial(t *testing.T) {
	enc, _ := NewEncoder(DefaultConfig())
	data := make([]byte, 1024)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand: %v", err)
	}
	out, _ := enc.Encode(data)

	// Case 1: Complete set
	shards := make([][]byte, len(out.Shards))
	for i, s := range out.Shards {
		shards[i] = s.Data
	}
	ok, err := enc.VerifyPartial(shards)
	if err != nil || !ok {
		t.Fatalf("VerifyPartial complete set failed: %v (ok=%v)", err, ok)
	}

	// Case 2: Incomplete but sufficient (drop 3 shards)
	shards[1] = nil
	shards[5] = nil
	shards[12] = nil
	ok, err = enc.VerifyPartial(shards)
	if err != nil || !ok {
		t.Fatalf("VerifyPartial with 3 missing failed: %v (ok=%v)", err, ok)
	}

	// Case 3: Too few shards (drop 5 shards)
	shards[2] = nil
	shards[9] = nil
	ok, err = enc.VerifyPartial(shards)
	if err == nil {
		t.Fatal("VerifyPartial with 5 missing should fail")
	}
}

func TestVerifyShardAndInlineValidator(t *testing.T) {
	enc, _ := NewEncoder(DefaultConfig())
	data := []byte("inline validation test data")
	out, _ := enc.Encode(data)

	validator := NewInlineValidator(out.Manifest)

	// Check correct shard
	if !validator.Verify(0, out.Shards[0].Data) {
		t.Error("Verify correct shard failed")
	}

	// Check tampered shard
	tampered := append([]byte(nil), out.Shards[0].Data...)
	tampered[0] ^= 0xFF
	if validator.Verify(0, tampered) {
		t.Error("Verify accepted tampered shard data")
	}

	// Check out of range
	if validator.Verify(-1, out.Shards[0].Data) {
		t.Error("Verify accepted out of range index")
	}
}

func TestEncodeDecodeStreamRoundTrip(t *testing.T) {
	cfg := Config{DataShards: 4, ParityShards: 2}
	enc, err := NewEncoder(cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	// 500 KB random block
	const size = 500 * 1024
	originalData := make([]byte, size)
	if _, err := rand.Read(originalData); err != nil {
		t.Fatalf("rand: %v", err)
	}

	// Writers for 6 output streams (shards)
	var shardBuffers [6]bytes.Buffer
	writers := make([]io.Writer, 6)
	for i := range shardBuffers {
		writers[i] = &shardBuffers[i]
	}

	// Stream Encode
	manifest, err := enc.EncodeStream(bytes.NewReader(originalData), writers)
	if err != nil {
		t.Fatalf("EncodeStream: %v", err)
	}

	if manifest.OriginalSize != size {
		t.Fatalf("manifest OriginalSize = %d, want %d", manifest.OriginalSize, size)
	}

	// Setup Readers
	readers := make([]io.Reader, 6)
	for i := range shardBuffers {
		readers[i] = bytes.NewReader(shardBuffers[i].Bytes())
	}

	// Drop 2 shards (parity shards)
	readers[4] = nil
	readers[5] = nil

	// Stream Decode
	var decodedBuf bytes.Buffer
	err = enc.DecodeStream(readers, &decodedBuf, manifest)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}

	if !bytes.Equal(decodedBuf.Bytes(), originalData) {
		t.Fatal("decoded stream does not match original stream data")
	}
}
