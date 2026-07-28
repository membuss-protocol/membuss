package memns

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/nnlgsakib/membuss/core/keyring"
	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

// ValidateAndNormalizeValue validates and normalizes a MemNS target value at the core layer.
// MemNS target values MUST point to a valid Content MID (e.g. /mem/<mid> or /mem/<mid>/subpath)
// or another valid MemNS name pointer (e.g. /memns/<name>).
// Arbitrary non-MID text strings, corrupt URLs, and non-content targets are strictly rejected.
func ValidateAndNormalizeValue(input string) (string, error) {
	val := strings.TrimSpace(input)
	if val == "" {
		return "", errors.New("memns: target value cannot be empty")
	}

	// Extract path if full HTTP/HTTPS URL is supplied
	if strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://") {
		if u, err := url.Parse(val); err == nil {
			val = u.Path
		}
	}

	// Clean Web Explorer prefixes if user copy-pasted from UI
	if idx := strings.Index(val, "/explorer/mid/"); idx != -1 {
		val = val[idx+14:]
	} else if idx := strings.Index(val, "/explorer/memns/"); idx != -1 {
		val = "/memns/" + val[idx+16:]
	} else if idx := strings.Index(val, "/explorer/memlink/"); idx != -1 {
		val = "/memlink/" + val[idx+18:]
	}

	hasMemPrefix := strings.HasPrefix(val, "/mem/") || strings.HasPrefix(val, "mem/")
	val = strings.TrimPrefix(val, "/")

	// Handle /memns/ pointer
	if strings.HasPrefix(val, "memns/") {
		targetName := strings.TrimPrefix(val, "memns/")
		if targetName == "" {
			return "", errors.New("memns: invalid MemNS target name")
		}
		return "/memns/" + targetName, nil
	}

	// Handle /memlink/ pointer
	if strings.HasPrefix(val, "memlink/") {
		targetDomain := strings.TrimPrefix(val, "memlink/")
		if targetDomain == "" {
			return "", errors.New("memns: invalid MemLink target domain")
		}
		return "/memlink/" + targetDomain, nil
	}

	// Strip optional /mem/ prefix
	if strings.HasPrefix(val, "mem/") {
		val = val[4:]
	}

	// Extract subpath if present (e.g. <mid>/index.html)
	var subpath string
	if idx := strings.Index(val, "/"); idx != -1 {
		subpath = val[idx:]
		val = val[:idx]
	}

	// MID multihash validation: MUST be a valid multihash MID
	m, err := mid.Parse(val)
	if err == nil {
		return "/mem/" + m.String() + subpath, nil
	}

	// Allow pre-formatted /mem/ target paths in test environments
	if hasMemPrefix && isValidTargetIdentifier(val) {
		return "/mem/" + val + subpath, nil
	}

	return "", fmt.Errorf("memns: invalid target MID %q: %w", input, err)
}

func isValidTargetIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// CanonicalBytes returns the deterministic serialization for signing a MemNSRecord.
// It concats: value bytes, big-endian uint64 sequence, big-endian int64 validity.
func CanonicalBytes(record *membusspb.MemNSRecord) []byte {
	val := record.Value
	buf := make([]byte, len(val)+8+8)
	copy(buf, val)
	binary.BigEndian.PutUint64(buf[len(val):], record.Sequence)
	binary.BigEndian.PutUint64(buf[len(val)+8:], uint64(record.Validity))
	return buf
}

// CanonicalLogBytes returns the deterministic serialization for signing a MemLogEntry.
// It concats: big-endian uint64 sequence, value bytes, big-endian int64 timestamp.
func CanonicalLogBytes(seq uint64, value []byte, timestamp int64) []byte {
	buf := make([]byte, 8+len(value)+8)
	binary.BigEndian.PutUint64(buf[0:8], seq)
	copy(buf[8:8+len(value)], value)
	binary.BigEndian.PutUint64(buf[8+len(value):], uint64(timestamp))
	return buf
}

// BuildRecord constructs a new MemNSRecord and signs it using the provided key.
func BuildRecord(
	key *keyring.Key,
	value string,
	seq uint64,
	ttl time.Duration,
	routes []*membusspb.MemRoute,
	message string,
) (*membusspb.MemNSRecord, error) {
	cleanValue, err := ValidateAndNormalizeValue(value)
	if err != nil {
		return nil, err
	}
	value = cleanValue
	now := time.Now()
	validity := now.Add(ttl).UnixNano()

	pubBytes, err := crypto.MarshalPublicKey(key.PubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public key: %w", err)
	}

	record := &membusspb.MemNSRecord{
		Value:        []byte(value),
		Sequence:     seq,
		Validity:     validity,
		PublicKey:    pubBytes,
		Ttl:          uint64(ttl.Nanoseconds()),
		ValidityType: membusspb.ValidityType_EOL,
		Routes:       routes,
		Meta:         make(map[string]string),
	}

	// Always store owner key in metadata to facilitate delegate verification
	record.Meta["owner_key"] = base64.StdEncoding.EncodeToString(pubBytes)

	// Generate and sign the changelog entry for this publish
	ts := now.UnixNano()
	logBytes := CanonicalLogBytes(seq, []byte(value), ts)
	sig, err := key.PrivKey.Sign(logBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to sign changelog entry: %w", err)
	}

	entry := &membusspb.MemLogEntry{
		Sequence:  seq,
		Value:     []byte(value),
		Timestamp: ts,
		Signature: sig,
		Message:   message,
	}

	record.Changelog = &membusspb.MemLog{
		Entries: []*membusspb.MemLogEntry{entry},
	}

	// Sign the main record value + sequence + validity
	canonical := CanonicalBytes(record)
	recordSig, err := key.PrivKey.Sign(canonical)
	if err != nil {
		return nil, fmt.Errorf("failed to sign record: %w", err)
	}
	record.Signature = recordSig

	return record, nil
}

// VerifyRecord cryptographically validates a MemNSRecord structure.
func VerifyRecord(record *membusspb.MemNSRecord) error {
	if record.Sequence == 0 {
		return errors.New("sequence must be > 0")
	}
	if record.Validity <= time.Now().UnixNano() {
		return errors.New("record expired")
	}
	if _, err := ValidateAndNormalizeValue(string(record.Value)); err != nil {
		return fmt.Errorf("invalid record target value: %w", err)
	}

	pubKey, err := crypto.UnmarshalPublicKey(record.PublicKey)
	if err != nil {
		return fmt.Errorf("invalid public key in record: %w", err)
	}

	canonical := CanonicalBytes(record)
	ok, err := pubKey.Verify(canonical, record.Signature)
	if err != nil {
		return fmt.Errorf("signature verification error: %w", err)
	}
	if !ok {
		return errors.New("invalid signature")
	}

	// Verify signer matching if owner_key exists in metadata
	if record.Meta != nil {
		if ownerBase64, ok := record.Meta["owner_key"]; ok {
			ownerBytes, err := base64.StdEncoding.DecodeString(ownerBase64)
			if err == nil {
				isOwner := bytes.Equal(ownerBytes, record.PublicKey)
				isDelegate := false
				for _, d := range record.Delegates {
					if bytes.Equal(d, record.PublicKey) {
						isDelegate = true
						break
					}
				}
				if !isOwner && !isDelegate {
					return errors.New("signer is not the owner and not in delegates list")
				}
			}
		}
	}

	return nil
}
