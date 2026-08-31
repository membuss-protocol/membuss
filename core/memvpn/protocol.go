package memvpn

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	FrameHandshake       byte = 0x01
	FrameHandshakeAck    byte = 0x02
	FrameServiceAnnounce byte = 0x03
	FrameDialRequest     byte = 0x04
	FrameDialResponse    byte = 0x05
	FramePing            byte = 0x06
	FramePong            byte = 0x07
	FrameExitConnect     byte = 0x10
	FrameExitAck         byte = 0x11
)

// Frame represents a binary framed message over libp2p streams.
type Frame struct {
	Type    byte
	Payload []byte
}

// WriteFrame writes a framed packet: [1 byte Type][4 bytes Length][Payload].
func WriteFrame(w io.Writer, f *Frame) error {
	header := make([]byte, 5)
	header[0] = f.Type
	binary.BigEndian.PutUint32(header[1:5], uint32(len(f.Payload)))

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if len(f.Payload) > 0 {
		if _, err := w.Write(f.Payload); err != nil {
			return fmt.Errorf("write frame payload: %w", err)
		}
	}
	return nil
}

// ReadFrame reads a framed packet.
func ReadFrame(r io.Reader) (*Frame, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	frameType := header[0]
	length := binary.BigEndian.Uint32(header[1:5])

	if length > 16<<20 { // 16 MiB safety limit
		return nil, errors.New("frame payload exceeds maximum size")
	}

	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, fmt.Errorf("read frame payload: %w", err)
		}
	}

	return &Frame{Type: frameType, Payload: payload}, nil
}

// WriteJSON writes a JSON object wrapped in a Frame.
func WriteJSON(w io.Writer, frameType byte, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	return WriteFrame(w, &Frame{Type: frameType, Payload: data})
}

// ReadJSON unmarshals a frame payload into a Go struct.
func ReadJSON(f *Frame, v any) error {
	return json.Unmarshal(f.Payload, v)
}

// HandshakePayload exchanged during initial P2P mesh connection.
type HandshakePayload struct {
	MeshID     string    `json:"mesh_id"`
	NodeName   string    `json:"node_name"`
	VirtualIP  string    `json:"virtual_ip"`
	AuthToken  string    `json:"auth_token"`
	Services   []string  `json:"services"`
	IsExitNode bool      `json:"is_exit_node"`
	Timestamp  time.Time `json:"timestamp"`
}

// HandshakeAckPayload returned upon successful authentication.
type HandshakeAckPayload struct {
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
	VirtualIP  string    `json:"virtual_ip"`
	IsExitNode bool      `json:"is_exit_node"`
	Timestamp  time.Time `json:"timestamp"`
}

// ServiceAnnouncePayload broadcasts exposed services to peers.
type ServiceAnnouncePayload struct {
	Services []string `json:"services"`
}

// DialRequestPayload requests access to a named service on a remote peer.
type DialRequestPayload struct {
	ServiceName string `json:"service_name"`
}

// DialResponsePayload indicates whether dialing the service succeeded.
type DialResponsePayload struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// ExitConnectPayload requests outbound TCP connection through an Exit Node.
type ExitConnectPayload struct {
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
}

// ExitAckPayload confirms connection establishment by the Exit Node.
type ExitAckPayload struct {
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	BoundAddr string `json:"bound_addr,omitempty"`
}
