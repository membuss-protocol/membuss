---
id: web-explorer
title: Built-in Web Explorer UI Architecture
sidebar_label: Web Explorer UI
---

# Built-in Web Explorer UI Architecture

Membuss runs an embedded **Web Explorer** (`gateway/explorer`) compiled directly into the daemon binary via Go `embed`.

---

## Dashboard Components

- **DAG Visualizer**: Interactive Merkle block graph tree viewer.
- **Search Bar**: Instant resolution of MIDs, directory sub-paths, and MemNS names.
- **Peer Swarm Dashboard**: Live multiaddress list, peer latency measurements, and active Memex sessions.
- **Upload Form**: Drag-and-drop file and directory tree uploader.
