---
id: cli-reference
title: Complete CLI Command Reference
sidebar_label: CLI Command Reference
---

# Complete CLI Command Reference

The unified `membuss` binary provides CLI subcommands for node management, data ingestion, DAG inspection, and network status.

---

## 1. Content Management Subcommands

```bash
# Add file or directory to network
membuss add ./path/to/file [-r] [--chunker fixed|rabin|fastcdc]

# Fetch content by MID
membuss get <mid> -o ./destination

# Inspect DAG node descriptor
membuss dag <mid>

# Seal content (pin recursively)
membuss seal <mid>

# Unseal content
membuss unseal <mid>

# Remove content locally
membuss rm <mid>
```

---

## 2. Daemon & Network Subcommands

```bash
# Start local node daemon
membuss daemon start --config ./membuss.yaml

# Check daemon status
membuss daemon status

# List connected libp2p peers
membuss peers

# Trigger local garbage collection
membuss gc [--min-age 1h]
```

---

## 3. MemVPN Subcommands

```bash
# Start daemon with VPN mesh enabled (configured via YAML)
membuss daemon --config ./membuss.yaml --datadir ~/.memdata

# Check VPN status (via REST API)
curl http://127.0.0.1:5001/api/v1/vpn/status

# List registered WireGuard client devices
curl http://127.0.0.1:5001/api/v1/vpn/wg/devices

# Register a new client device
curl -X POST http://127.0.0.1:5001/api/v1/vpn/wg/device \
  -d '{"name": "My iPhone"}'

# Get WireGuard config profile for a device
curl "http://127.0.0.1:5001/api/v1/vpn/wg/profile?device=My%20iPhone"

# Download WireGuard .conf file
curl -o myphone.conf "http://127.0.0.1:5001/api/v1/vpn/wg/config?device=My%20iPhone"

# Delete a client device
curl -X DELETE "http://127.0.0.1:5001/api/v1/vpn/wg/device?id=My%20iPhone"

# Enable Auto Exit Swarm (route traffic through mesh exit nodes)
curl -X POST http://127.0.0.1:5001/api/v1/vpn/exit/select \
  -d '{"peer_id": "auto"}'

# Disable Exit Swarm (direct local egress)
curl -X POST http://127.0.0.1:5001/api/v1/vpn/exit/select \
  -d '{"peer_id": ""}'

# Act as Exit Node Provider (share internet with mesh)
curl -X POST http://127.0.0.1:5001/api/v1/vpn/exit/toggle \
  -d '{"enabled": true, "allow_all": true}'

# Expose a local service to the mesh
curl -X POST http://127.0.0.1:5001/api/v1/vpn/services/expose \
  -d '{"name": "webapp", "target_addr": "127.0.0.1:3000", "description": "My app"}'

# Forward a remote peer's service to a local port
curl -X POST http://127.0.0.1:5001/api/v1/vpn/services/forward \
  -d '{"local_addr": "127.0.0.1:8888", "remote_peer_id": "12D3KooW...", "remote_service": "webapp"}'

# Stop port forwarding
curl -X DELETE "http://127.0.0.1:5001/api/v1/vpn/services/forward?local_addr=127.0.0.1:8888"
```
