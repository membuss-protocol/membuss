// Package mid implements the Membuss content identifier (MID).
//
// A MID is a content-addressed identifier that follows the
// IPFS CIDv1 shape: it is a multibase-encoded (base32lower)
// CIDv1 wrapping a multihash envelope. The public string
// form is the literal "mem" prefix followed by the multibase
// letter and the lower-case base32 alphabet:
//
//	mem + "b" + base32lower(CIDv1 bytes)
//
// The CIDv1 byte layout is:
//
//	<version=0x01> <varint(codec)> <multihash>
//
// The multihash envelope is:
//
//	<hash-fn-code=0x12 (sha2-256)> <length=0x20> <32-byte-digest>
//
// so a raw block (codec 0x55) is:
//
//	01 55 12 20 <32 bytes>          (total 36 bytes)
//
// which encodes to a 58-character base32 string. The
// "mem" prefix + the multibase 'b' prefix + the 58
// characters produces the canonical ~61-character public
// MID (matching the IPFS CIDv1 + base32 length).
package mid

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
)

// Prefix is the literal string that begins every public MID.
const Prefix = "mem"

// CIDv1 version byte.
const cidVersion1 = 1

// CodecRaw identifies a block whose data is the raw content
// payload. It mirrors the IPFS "raw" codec (0x55) and is the
// codec used for every leaf chunk emitted by core/chunk.
const CodecRaw uint64 = 0x55

// CodecDAGPB identifies a DAG internal node. It mirrors the
// IPFS dag-pb codec (0x70) and is the codec used for every
// non-leaf DAGNode emitted by core/dag.
const CodecDAGPB uint64 = 0x70

// CodecMemFS identifies a MemFS typed node (FILE, DIR, SYMLINK,
// METADATA). It is the codec used by every MemFSNode emitted
// by core/memfs. Codec 0x72 is a custom multicodec tag
// registered for Membuss's UnixFS-equivalent layer (Phase 17).
const CodecMemFS uint64 = 0x72

// Supported multihash algorithm constants.
const (
	HashBLAKE3   = multihash.BLAKE3
	HashSHA256   = multihash.SHA2_256
	HashSHA512   = multihash.SHA2_512
)

// DefaultHash is the default multihash code used to hash content (BLAKE3).
const DefaultHash = multihash.BLAKE3

var (
	defaultHashMu  sync.RWMutex
	defaultHashAlg uint64 = multihash.BLAKE3
)

// GetDefaultHash returns the current default multihash algorithm code.
func GetDefaultHash() uint64 {
	defaultHashMu.RLock()
	defer defaultHashMu.RUnlock()
	return defaultHashAlg
}

// SetDefaultHash sets the default multihash algorithm code (e.g. multihash.BLAKE3, multihash.SHA2_256).
func SetDefaultHash(code uint64) error {
	if !isSupportedHash(code) {
		return fmt.Errorf("mid: unsupported hash algorithm code %#x", code)
	}
	defaultHashMu.Lock()
	defaultHashAlg = code
	defaultHashMu.Unlock()
	return nil
}

// SetDefaultHashByName sets the default multihash algorithm by string name ("blake3", "sha256", "sha512").
func SetDefaultHashByName(name string) error {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "blake3", "blake3-256", "blake":
		return SetDefaultHash(multihash.BLAKE3)
	case "sha256", "sha2-256":
		return SetDefaultHash(multihash.SHA2_256)
	case "sha512", "sha2-512":
		return SetDefaultHash(multihash.SHA2_512)
	default:
		return fmt.Errorf("mid: unknown hash algorithm name %q", name)
	}
}

func isSupportedHash(code uint64) bool {
	switch code {
	case multihash.BLAKE3, multihash.SHA2_256, multihash.SHA2_512, multihash.KECCAK_256, multihash.SHAKE_256:
		return true
	default:
		return false
	}
}

// MID is a content identifier: a CIDv1-encoded codec +
// multihash digest.
//
// The zero value is invalid; use FromBytes, FromMultihash,
// or Parse to construct one. The String form is the public,
// network-facing identifier and is what appears on the wire
// and in the gateway.
type MID struct {
	// Version is the CIDv1 version byte. Always 1 for
	// freshly built MIDs. Parsed MIDs report whatever the
	// input carried; only Version==1 is accepted by Parse.
	Version uint8
	// Codec is the multicodec tag for the body. 0x55 for
	// raw blocks, 0x70 for dag-pb, etc.
	codec uint64
	// Hash is the raw multihash envelope, including the
	// hash-fn code byte, the length byte, and the digest.
	Hash []byte
	// cid is the parsed go-cid form. Cached so callers that
	// want the rich cid.Cid API do not have to re-parse.
	cid cid.Cid
	// str is the cached public string form ("mem" + base32).
	str string
}

// FromBytes returns the MID for the given content bytes using the configured default multihash algorithm (BLAKE3 by default).
func FromBytes(data []byte) MID {
	alg := GetDefaultHash()
	m, err := FromBytesWithCodecAndHash(data, CodecRaw, alg)
	if err != nil {
		sum := sha256.Sum256(data)
		mh, _ := multihash.Encode(sum[:], multihash.SHA2_256)
		return FromCodecAndHash(cidVersion1, CodecRaw, mh)
	}
	return m
}

// FromBytesWithCodec returns the MID for the given content bytes tagged with the supplied codec using the configured default multihash algorithm.
func FromBytesWithCodec(data []byte, codec uint64) MID {
	alg := GetDefaultHash()
	m, err := FromBytesWithCodecAndHash(data, codec, alg)
	if err != nil {
		sum := sha256.Sum256(data)
		mh, _ := multihash.Encode(sum[:], multihash.SHA2_256)
		return FromCodecAndHash(cidVersion1, codec, mh)
	}
	return m
}

// FromBytesWithHash returns the MID for the given content bytes using the specified hash algorithm.
// Supported algorithms include multihash.BLAKE3, multihash.SHA2_256, multihash.BLAKE2B_256, and multihash.SHA2_512.
func FromBytesWithHash(data []byte, hashAlg uint64) (MID, error) {
	return FromBytesWithCodecAndHash(data, CodecRaw, hashAlg)
}

// FromBytesWithCodecAndHash returns the MID for the given content bytes tagged with the supplied codec and hash algorithm.
func FromBytesWithCodecAndHash(data []byte, codec uint64, hashAlg uint64) (MID, error) {
	mh, err := multihash.Sum(data, hashAlg, -1)
	if err != nil {
		return MID{}, fmt.Errorf("mid: sum multihash algorithm %#x: %w", hashAlg, err)
	}
	return fromCodecAndHashErr(cidVersion1, codec, mh)
}

// FromMultihash wraps a pre-built multihash envelope with the
// given codec. The caller retains ownership of mh; this
// function copies it.
func FromMultihash(codec uint64, mh []byte) (MID, error) {
	return fromCodecAndHashErr(cidVersion1, codec, mh)
}

// fromCodecAndHashErr is the same as FromCodecAndHash but
// surfaces validation errors instead of panicking.
func fromCodecAndHashErr(version uint8, codec uint64, mh []byte) (MID, error) {
	if len(mh) == 0 {
		return MID{}, errors.New("mid: empty multihash")
	}
	if _, err := multihash.Decode(mh); err != nil {
		return MID{}, fmt.Errorf("mid: decode multihash: %w", err)
	}
	if err := validateEnvelope(mh); err != nil {
		return MID{}, fmt.Errorf("mid: validate multihash: %w", err)
	}
	out := make([]byte, len(mh))
	copy(out, mh)
	return build(version, codec, out)
}

// FromCodecAndHash constructs a MID from a version, codec, and
// multihash envelope. It panics if the multihash is invalid;
// the FromBytes hot path uses this and SHA-256 is always
// encodable, so the panic is unreachable there.
func FromCodecAndHash(version uint8, codec uint64, mh []byte) MID {
	m, err := fromCodecAndHashErr(version, codec, mh)
	if err != nil {
		panic(err.Error())
	}
	return m
}

// build is the shared constructor used by FromCodecAndHash and
// the parser. It copies the multihash and parses the go-cid.
func build(version uint8, codec uint64, mh []byte) (MID, error) {
	if version != cidVersion1 {
		return MID{}, fmt.Errorf("mid: unsupported CID version %d", version)
	}
	c := cid.NewCidV1(codec, mh)
	str := Prefix + c.String()
	return MID{
		Version: version,
		codec:   codec,
		Hash:    mh,
		cid:     c,
		str:     str,
	}, nil
}

// Parse parses a public MID string ("mem" + multibase +
// CIDv1 bytes) and returns the corresponding MID. It rejects
// anything that is not a CIDv1 wrapping a sha2-256
// multihash.
func Parse(s string) (MID, error) {
	if !strings.HasPrefix(s, Prefix) {
		return MID{}, fmt.Errorf("mid: missing %q prefix", Prefix)
	}
	encoded := strings.TrimPrefix(s, Prefix)
	if encoded == "" {
		return MID{}, errors.New("mid: empty encoded body")
	}
	parsed, err := cid.Decode(encoded)
	if err != nil {
		return MID{}, errors.New("mid: invalid encoded multihash")
	}
	if parsed.Version() != cidVersion1 {
		return MID{}, fmt.Errorf("mid: unsupported version %d", parsed.Version())
	}
	mh := parsed.Hash()
	if err := validateEnvelope(mh); err != nil {
		return MID{}, fmt.Errorf("mid: invalid multihash envelope: %w", err)
	}
	str := s
	formatted := Prefix + parsed.String()
	if str != formatted {
		str = formatted
	}
	return MID{
		Version: uint8(parsed.Version()),
		codec:   parsed.Prefix().Codec,
		Hash:    append([]byte(nil), mh...),
		cid:     parsed,
		str:     str,
	}, nil
}

// MustParse is the panicking form of Parse; it is intended
// for constants and test fixtures only.
func MustParse(s string) MID {
	m, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return m
}

// validateEnvelope sanity-checks a multihash decoded from
// bytes. It rejects unknown hash codes and length
// mismatches, which the multihash library can otherwise
// silently accept.
func validateEnvelope(mh []byte) error {
	if len(mh) == 0 {
		return errors.New("empty multihash")
	}
	d, err := multihash.Decode(mh)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if !isSupportedHash(d.Code) {
		return fmt.Errorf("unsupported hash code %#x", d.Code)
	}
	if len(d.Digest) != d.Length {
		return fmt.Errorf("length mismatch: header says %d, body has %d", d.Length, len(d.Digest))
	}
	return nil
}

// Codec returns the codec tag associated with this MID.
func (m MID) Codec() uint64 { return m.codec }

// HashBytes returns a copy of the multihash envelope.
func (m MID) HashBytes() []byte {
	out := make([]byte, len(m.Hash))
	copy(out, m.Hash)
	return out
}

// CopyBytes returns a fresh defensive copy of the multihash envelope.
func (m MID) CopyBytes() []byte {
	out := make([]byte, len(m.Hash))
	copy(out, m.Hash)
	return out
}

// RawBytes returns the underlying multihash envelope slice without allocating a copy.
// Callers MUST treat the returned slice as read-only.
func (m MID) RawBytes() []byte { return m.Hash }

// DigestBytes returns the raw hash digest (decoded from the
// multihash envelope).
func (m MID) DigestBytes() ([]byte, error) {
	d, err := multihash.Decode(m.Hash)
	if err != nil {
		return nil, fmt.Errorf("mid: decode multihash: %w", err)
	}
	return d.Digest, nil
}

// Bytes returns the multihash envelope without allocating a copy.
// It is the form used in protobuf message bodies and in Pebble DB keys.
func (m MID) Bytes() []byte { return m.Hash }

// CID returns the underlying go-cid Cid. The returned value
// is suitable for use with the ipfs/go-cid APIs. It shares
// memory with the receiver; do not mutate.
func (m MID) CID() cid.Cid { return m.cid }

// String returns the public, network-facing form of this MID:
// the "mem" prefix followed by the multibase (base32lower)
// encoding of the CIDv1 bytes.
func (m MID) String() string {
	if m.str != "" {
		return m.str
	}
	if len(m.Hash) == 0 {
		return ""
	}
	c := cid.NewCidV1(m.codec, m.Hash)
	return Prefix + c.String()
}

// Equal reports whether m and other refer to the same
// content. Two MIDs are equal iff their codec and
// multihash envelope match. The CIDv1 version is always 1
// for freshly built MIDs, so it does not need to be
// checked separately.
func (m MID) Equal(other MID) bool {
	if m.codec != other.codec {
		return false
	}
	return bytes.Equal(m.Hash, other.Hash)
}

// IsZero reports whether m is the zero value, which is not
// a valid MID.
func (m MID) IsZero() bool { return len(m.Hash) == 0 }
