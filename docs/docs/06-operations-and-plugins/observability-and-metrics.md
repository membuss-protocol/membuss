---
id: observability-and-metrics
title: Observability, Prometheus Metrics & Telemetry
sidebar_label: Observability & Metrics
---

# Observability, Prometheus Metrics & Telemetry

Membuss provides built-in Prometheus metrics and telemetry (`gateway/memgate_v2/metrics.go`).

---

## 1. Prometheus Endpoint (`/metrics`)

When `metrics_enabled: true` is configured, nodes expose a Prometheus endpoint at `/metrics`.

### Key Metrics

| Metric Name | Type | Description |
|---|---|---|
| `membuss_gateway_requests_total` | Counter | Total HTTP gateway requests by status code |
| `membuss_gateway_bytes_sent_total` | Counter | Total bytes served over gateway |
| `membuss_blockstore_blocks_total` | Gauge | Total stored content blocks |
| `membuss_anchor_discovery_backlog` | Gauge | Active backlog of discovered MIDs to mirror |
| `membuss_peer_connections_active` | Gauge | Connected libp2p swarm peers |
