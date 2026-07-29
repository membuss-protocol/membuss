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
