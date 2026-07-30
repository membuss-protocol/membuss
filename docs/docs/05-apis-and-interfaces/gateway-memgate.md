---
id: gateway-memgate
title: Mem-Gate HTTP CDN Gateway Specification
sidebar_label: Mem-Gate CDN Gateway
---

# Mem-Gate HTTP CDN Gateway Specification

**Mem-Gate** (`gateway/memgate_v2`) is a public HTTP gateway and CDN router built on `go-chi/chi/v5` listening on port `8080`.

---

## 1. Gateway Feature Engine

- **RFC 7233 Byte Range Streaming**: Streamable audio and video playback (`Range: bytes=0-1048575`).
- **Dynamic Content Sniffing**: MIME type detection via `gabriel-vasile/mimetype` with override maps (`.wasm`, `.svg`, `.css`, `.js`).
- **Edge Cache Headers**: Returns `Cache-Control: public, max-age=31536000, immutable` and `ETag` headers.
- **IP Rate Limiting & Referer Tracking**: Token bucket IP rate limiter and referer tracker to prevent DDoS attacks.

---

## 2. Gateway Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/mem/{mid}` | Serve resolved MID content |
| `HEAD` | `/mem/{mid}` | Metadata header existence check |
| `GET` | `/mem/{mid}?format=dag-json` | Output raw DAG protobuf as JSON |
| `GET` | `/mem/{mid}/{path}` | Resolve sub-path within a MemFS directory |
