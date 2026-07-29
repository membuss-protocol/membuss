---
id: deployment
title: Production Deployment & Containerization
sidebar_label: Deployment & Docker
---

# Production Deployment & Containerization Guide

This document details deploying **Membuss** in production environments using Docker, multi-node clusters, reverse proxies, and systemd daemons.

---

## 1. Single-Node Docker Deployment

Membuss provides a multi-stage Docker build producing a distroless container image executing as non-root user `membuss:10001`.

```yaml
# docker-compose.yml
version: '3.8'

services:
  membuss-node:
    image: nnlgsakib/membuss:latest
    container_name: membuss-node
    restart: unless-stopped
    ports:
      - "4001:4001/tcp"
      - "4001:4001/udp"
      - "8080:8080"
      - "5001:5001"
      - "50051:50051"
    volumes:
      - ./data:/app/data
      - ./membuss.yaml:/app/membuss.yaml:ro
    environment:
      - MEMBUSS_LOG_LEVEL=info
```

Run container:

```bash
docker compose up -d
```

---

## 2. Multi-Node Anchor Cluster Setup

For high-availability infrastructure, deploy a cluster with Anchor Nodes enabled:

```yaml
# docker-compose.multi.yml
version: '3.8'

services:
  node1-anchor:
    image: nnlgsakib/membuss:latest
    container_name: membuss-anchor1
    environment:
      - MEMBUSS_ANCHOR_MODE=true
    volumes:
      - ./anchor1-data:/app/data

  node2-worker:
    image: nnlgsakib/membuss:latest
    container_name: membuss-worker2
    volumes:
      - ./worker2-data:/app/data

  node3-gateway:
    image: nnlgsakib/membuss:latest
    container_name: membuss-gateway3
    ports:
      - "80:8080"
    volumes:
      - ./gateway3-data:/app/data
```

---

## 3. Reverse Proxy Nginx Configuration (TLS & Gateway)

To front the **Mem-Gate** HTTP CDN layer with TLS/SSL:

```nginx
server {
    listen 443 ssl http2;
    server_name gateway.membuss.io;

    ssl_certificate /etc/letsencrypt/live/gateway.membuss.io/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/gateway.membuss.io/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Disable proxy buffering for streaming byte responses
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}
```
