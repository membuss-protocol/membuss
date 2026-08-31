package memvpn

import (
	"time"
)

const (
	// MeshProtocolID is the libp2p protocol identifier for P2P mesh stream multiplexing.
	MeshProtocolID = "/membuss/memvpn/1.0.0"

	// ExitProtocolID is the protocol identifier for full-internet exit node proxy streaming.
	ExitProtocolID = "/membuss/memvpn/exit/1.0.0"

	// DefaultVirtualSubnet is the default virtual IP range (10.42.0.0/24).
	DefaultVirtualSubnet = "10.42.0."

	// DefaultWGPort is the default WireGuard UDP listen port.
	DefaultWGPort = 51820

	// DefaultServerVirtualIP is the default virtual IP of the local WireGuard server interface.
	DefaultServerVirtualIP = "10.42.0.1"
)

// MeshConfig holds configuration for the MemVPN subsystem.
type MeshConfig struct {
	MeshID           string        `yaml:"mesh_id" json:"mesh_id"`
	NodeName         string        `yaml:"node_name" json:"node_name"`
	PreSharedKey     string        `yaml:"preshared_key" json:"preshared_key"`
	VirtualIP        string        `yaml:"virtual_ip" json:"virtual_ip"`
	AllowAllPeers    bool          `yaml:"allow_all_peers" json:"allow_all_peers"`
	AllowedPeers     []string      `yaml:"allowed_peers" json:"allowed_peers"`
	WGListenPort     int           `yaml:"wg_listen_port" json:"wg_listen_port"`
	IsExitNode       bool          `yaml:"is_exit_node" json:"is_exit_node"`
	SelectedExit     string        `yaml:"selected_exit" json:"selected_exit"`
	ExitAllowAll     bool          `yaml:"exit_allow_all" json:"exit_allow_all"`
	ExitAllowedPeers []string      `yaml:"exit_allowed_peers" json:"exit_allowed_peers"`
	ConnectTimeout   time.Duration `yaml:"connect_timeout" json:"connect_timeout"`
	DataDir          string        `yaml:"data_dir" json:"data_dir"`
}

// WGDevice represents a client device (e.g. "iPhone", "MacBook") connected via WireGuard.
type WGDevice struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	PublicKey         string    `json:"public_key"`
	PrivateKey        string    `json:"private_key,omitempty"`
	PresharedKey      string    `json:"preshared_key,omitempty"`
	VirtualIP         string    `json:"virtual_ip"`
	AllowedIPs        string    `json:"allowed_ips"`
	CreatedAt         time.Time `json:"created_at"`
	LastHandshakeTime time.Time `json:"last_handshake_time,omitempty"`
	BytesSent         int64     `json:"bytes_sent"`
	BytesRecv         int64     `json:"bytes_recv"`
	Endpoint          string    `json:"endpoint,omitempty"`
	Connected         bool      `json:"connected"`
}

// WGProfile represents a generated WireGuard configuration ready for import or QR encoding.
type WGProfile struct {
	DeviceName     string `json:"device_name"`
	VirtualIP      string `json:"virtual_ip"`
	ClientPrivKey  string `json:"client_private_key"`
	ClientPubKey   string `json:"client_public_key"`
	ServerPubKey   string `json:"server_public_key"`
	ServerEndpoint string `json:"server_endpoint"`
	DNS            string `json:"dns"`
	AllowedIPs     string `json:"allowed_ips"`
	ConfigText     string `json:"config_text"`
	DownloadURL    string `json:"download_url"`
}

// ExitNodeInfo contains telemetry for an internet exit gateway in the swarm.
type ExitNodeInfo struct {
	PeerID      string `json:"peer_id"`
	NodeName    string `json:"node_name"`
	VirtualIP   string `json:"virtual_ip"`
	Country     string `json:"country,omitempty"`
	City        string `json:"city,omitempty"`
	LatencyMs   int64  `json:"latency_ms"`
	ActiveConns int64  `json:"active_conns"`
	Available   bool   `json:"available"`
	Selected    bool   `json:"selected"`
}

// ExitPolicy defines firewall and access rules for an exit node.
type ExitPolicy struct {
	AllowAll        bool     `json:"allow_all"`
	AllowedPeers    []string `json:"allowed_peers"`
	BlockPrivateIPs bool     `json:"block_private_ips"`
}

// ExposedService represents a local port exposed to the P2P mesh network.
type ExposedService struct {
	Name         string   `json:"name"`
	TargetAddr   string   `json:"target_addr"`
	Description  string   `json:"description"`
	Status       string   `json:"status"`
	BytesSent    int64    `json:"bytes_sent"`
	BytesRecv    int64    `json:"bytes_recv"`
	ActiveConns  int64    `json:"active_conns"`
	AllowedPeers []string `json:"allowed_peers,omitempty"`
}

// PortForward represents a local listening port bound to a remote peer's service.
type PortForward struct {
	LocalAddr     string `json:"local_addr"`
	RemotePeerID  string `json:"remote_peer_id"`
	RemoteService string `json:"remote_service"`
	Status        string `json:"status"`
	BytesSent     int64  `json:"bytes_sent"`
	BytesRecv     int64  `json:"bytes_recv"`
	ActiveConns   int64  `json:"active_conns"`
}

// PeerInfo contains status information about a connected mesh peer.
type PeerInfo struct {
	PeerID      string   `json:"peer_id"`
	NodeName    string   `json:"node_name"`
	VirtualIP   string   `json:"virtual_ip"`
	Connected   bool     `json:"connected"`
	LatencyMs   int64    `json:"latency_ms"`
	BytesSent   int64    `json:"bytes_sent"`
	BytesRecv   int64    `json:"bytes_recv"`
	Services    []string `json:"services"`
	IsExitNode  bool     `json:"is_exit_node"`
	DirectRoute bool     `json:"direct_route"`
}

// SpeedSample represents a point-in-time bandwidth speed measurement.
type SpeedSample struct {
	Timestamp   time.Time `json:"timestamp"`
	UploadBps   int64     `json:"upload_bps"`
	DownloadBps int64     `json:"download_bps"`
}

// TrafficStats tracks aggregate VPN throughput and network contribution.
type TrafficStats struct {
	// Aggregate Traffic
	BytesSent     int64 `json:"bytes_sent"`
	BytesRecv     int64 `json:"bytes_recv"`
	ActiveStreams int64 `json:"active_streams"`
	TotalStreams  int64 `json:"total_streams"`

	// Swarm Contribution (Egress / Relay served to network)
	ContributedBytesSent int64 `json:"contributed_bytes_sent"`
	ContributedBytesRecv int64 `json:"contributed_bytes_recv"`
	ContributedConns     int64 `json:"contributed_conns"`

	// Client Devices (Traffic consumed by local connected devices)
	ClientBytesSent int64 `json:"client_bytes_sent"`
	ClientBytesRecv int64 `json:"client_bytes_recv"`
	ClientConns     int64 `json:"client_conns"`

	// Protocol Breakdown
	DNSQueriesCount int64 `json:"dns_queries_count"`
	TCPConnsCount   int64 `json:"tcp_conns_count"`
	UDPFlowsCount   int64 `json:"udp_flows_count"`

	// Live Speeds & Rate History
	CurrentUploadBps   int64         `json:"current_upload_bps"`
	CurrentDownloadBps int64         `json:"current_download_bps"`
	ContributionRatio  float64       `json:"contribution_ratio"`
	SpeedHistory       []SpeedSample `json:"speed_history,omitempty"`
}

// MeshStatus is the top-level status report of the MemVPN service.
type MeshStatus struct {
	Enabled          bool             `json:"enabled"`
	MeshID           string           `json:"mesh_id"`
	NodeName         string           `json:"node_name"`
	VirtualIP        string           `json:"virtual_ip"`
	WGServerPort     int              `json:"wg_server_port"`
	WGServerPubKey   string           `json:"wg_server_public_key"`
	WGServerEndpoint string           `json:"wg_server_endpoint"`
	WGDevicesCount   int              `json:"wg_devices_count"`
	IsExitNode       bool             `json:"is_exit_node"`
	SelectedExitNode string           `json:"selected_exit_node"`
	RoutingEnabled   bool             `json:"routing_enabled"`
	PeerCount        int              `json:"peer_count"`
	Peers            []PeerInfo       `json:"peers"`
	ExitNodes        []ExitNodeInfo   `json:"exit_nodes"`
	Services         []ExposedService `json:"services"`
	Forwards         []PortForward    `json:"forwards"`
	Stats            TrafficStats     `json:"stats"`
	UptimeSeconds    int64            `json:"uptime_seconds"`
}
