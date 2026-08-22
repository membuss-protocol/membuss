// Package wiretest provides in-memory test doubles for membuss
// wire-format helpers. Test-support code: never import from
// production paths.
package wiretest

import (
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// Stream is an inert network.Stream backed by a caller-supplied
// io.Reader and optional io.Writer. Only Read/Write carry behavior;
// every other method is a no-op — exactly the surface framing
// helpers (readMsg/readFrame) exercise.
//
// A nil Writer is replaced with io.Discard so writes never panic.
type Stream struct {
	io.Reader
	io.Writer
}

// NewStream wraps r as a network.Stream whose reads drain r.
func NewStream(r io.Reader) *Stream {
	return &Stream{Reader: r, Writer: io.Discard}
}

func (s *Stream) ID() string                                   { return "wiretest" }
func (s *Stream) Protocol() protocol.ID                        { return "/membuss/wiretest/1.0.0" }
func (s *Stream) SetProtocol(protocol.ID) error                { return nil }
func (s *Stream) Stat() network.Stats                          { return network.Stats{} }
func (s *Stream) Conn() network.Conn                           { return nil }
func (s *Stream) Scope() network.StreamScope                   { return nil }
func (s *Stream) Close() error                                 { return nil }
func (s *Stream) CloseRead() error                             { return nil }
func (s *Stream) CloseWrite() error                            { return nil }
func (s *Stream) Reset() error                                 { return nil }
func (s *Stream) ResetWithError(network.StreamErrorCode) error { return nil }
func (s *Stream) SetDeadline(time.Time) error                  { return nil }
func (s *Stream) SetReadDeadline(time.Time) error              { return nil }
func (s *Stream) SetWriteDeadline(time.Time) error             { return nil }
