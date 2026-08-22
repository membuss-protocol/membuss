# Membuss deploy bundle (XC-009)

Bundled Prometheus alert rules and Grafana dashboards for the
metrics the daemon already exposes.

## Contents

- `grafana/membuss-node.json` — node overview: store size, peers,
  DHT provide success, Memex block flow, ingest + transfer latency,
  erasure repair activity.
- `grafana/membuss-gateway.json` — Mem-Gate v2: request rate,
  5xx ratio, LRU cache hit ratio, rate-limit drops, SSE streams.
- `prometheus/rules.yml` — 6 alerts (DHT failure ratio, isolated
  node, unrecoverable MIDs, gateway 5xx ratio, cache hit ratio,
  rate-limit drops).

## Prometheus

The node API serves `/metrics` on the API address (default
`127.0.0.1:5001`) when `metrics_enabled: true`. Mem-Gate serves its
own registry on the gateway metrics port (token-gated or
localhost-only). Example scrape config:

```yaml
scrape_configs:
  - job_name: membuss-node
    static_configs:
      - targets: ["node1:5001"]

  - job_name: membuss-gateway
    # gateway metrics are token-gated when metrics_token is set;
    # otherwise localhost-only.
    authorization:
      credentials: "<metrics_token>"
    static_configs:
      - targets: ["node1:8080"]
```

## Alert rules

```yaml
rule_files:
  - deploy/prometheus/rules.yml
```

## Grafana

Import both dashboards via **Dashboards > Import** and pick your
Prometheus data source in the `DS_PROMETHEUS` selector.

## Note on repair metrics

`membuss_erasure_repair_*` panels stay at zero until the repair
worker is started by the daemon — see finding.txt CORE-F2 /
XC-009 note in finding.txt AUDIT ADDENDUM.
