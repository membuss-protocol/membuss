---
id: node-control-api
title: Local Node Control REST API Specification (/api/v1)
sidebar_label: Node Control REST API
---

# Node Control REST API Specification (`/api/v1`)

The node daemon exposes a REST control API on `http://127.0.0.1:5001/api/v1`.

---

## Endpoint Specification

| Method | Endpoint | Query / Body Params | Response JSON |
|---|---|---|---|
| `GET` | `/api/v1/node/info` | None | `{ "peer_id": "...", "addrs": [...], "version": "1.0.0" }` |
| `POST` | `/api/v1/add` | Multipart file payload | `{ "mid": "membafz...", "size": 1048576 }` |
| `GET` | `/api/v1/peers` | None | `{ "peers": [{ "peer_id": "...", "addr": "..." }] }` |
| `POST` | `/api/v1/seal/{mid}` | URL Path Param `mid` | `{ "status": "sealed", "mid": "membafz..." }` |
| `POST` | `/api/v1/gc` | `{ "min_age": 3600 }` | `{ "freed_bytes": 263700 }` |

---

## MemVPN Endpoints

| Method | Endpoint | Query / Body Params | Response JSON |
|---|---|---|---|
| `GET` | `/api/v1/vpn/status` | None | Full `MeshStatus` with stats, peers, exit nodes, services |
| `GET` | `/api/v1/vpn/wg/devices` | None | `[{ "name": "...", "virtual_ip": "...", "connected": true, ... }]` |
| `POST` | `/api/v1/vpn/wg/device` | `{ "name": "iPhone" }` | `{ "device_name": "...", "config_text": "...", ... }` |
| `DELETE` | `/api/v1/vpn/wg/device` | `?id=<name>` | `{ "success": true }` |
| `GET` | `/api/v1/vpn/wg/profile` | `?device=<name>` | `WGProfile` with config text, keys, endpoint |
| `GET` | `/api/v1/vpn/wg/config` | `?device=<name>` | WireGuard `.conf` file download |
| `POST` | `/api/v1/vpn/exit/select` | `{ "peer_id": "auto" \| "<id>" \| "" }` | `{ "selected_exit": "..." }` |
| `POST` | `/api/v1/vpn/exit/toggle` | `{ "enabled": true, "allow_all": true }` | `{ "success": true }` |
| `POST` | `/api/v1/vpn/services/expose` | `{ "name": "...", "target_addr": "...", "description": "..." }` | `{ "success": true }` |
| `DELETE` | `/api/v1/vpn/services/expose` | `?name=<name>` | `{ "success": true }` |
| `POST` | `/api/v1/vpn/services/forward` | `{ "local_addr": "...", "remote_peer_id": "...", "remote_service": "..." }` | `{ "success": true }` |
| `DELETE` | `/api/v1/vpn/services/forward` | `?local_addr=<addr>` | `{ "success": true }` |
