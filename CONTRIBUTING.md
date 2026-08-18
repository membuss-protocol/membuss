# Contributing to Membuss

Thank you for your interest in contributing to **Membuss**! We welcome contributions from developers, researchers, and technical writers worldwide.

Membuss is a decentralized, content-addressed storage, networking, and serverless edge compute protocol built in Go. Whether you are fixing bugs, proposing new protocol extensions, optimizing erasure coding math, or improving documentation, your help is appreciated.

---

## 🧭 Monorepo Structure

Before you start writing code, take a look at the codebase layout:

```
membuss/
├── cmd/
│   └── membuss/           # Unified CLI, daemon, and gateway executable
├── core/
│   ├── chunk/             # Adaptive procedural chunking (256 KiB - 4 MiB)
│   ├── dag/               # BLAKE3 Merkle DAG builder & multihash formatting (MID)
│   ├── erasure/           # Reed-Solomon 10+4 Galois Field erasure coding & repair
│   ├── memfs/             # Virtual UnixFS-style content-addressed filesystem
│   ├── memedge/           # Serverless edge engine (Wazero WASI & Goja JavaScript)
│   ├── memns/             # Ed25519 cryptographic mutable name pointers
│   └── store/             # Pebble LSM SSTable store & Counting Bloom Filters
├── net/
│   ├── dht/               # Mem-DHT (Kademlia with Protobuf record validation)
│   ├── memex_v2/          # Multiplexed block exchange over libp2p streams
│   └── pex/               # Peer Exchange (PEX) gossip protocol
├── gateway/
│   ├── memgate_v2/        # Public HTTP CDN gateway with RFC 7233 byte-range streaming
│   └── explorer/          # SvelteKit-powered Web Explorer & SSE telemetry
├── desktop/               # Cross-platform desktop application (Wails v2)
├── proto/                 # Protocol Buffer contracts and generated Go structs
└── docs/                  # Docusaurus documentation website
```

---

## 🛠️ Development Environment Setup

### Prerequisites

- **Go**: Version `1.25` or higher ([Download Go](https://go.dev/dl/))
- **Node.js & npm**: Version `18+` or `20+` (required only for desktop frontend and web explorer)
- **Git**: For version control
- **Protocol Buffers Compiler (`protoc`)**: Optional, required only when updating `.proto` definitions

### 1. Clone the Repository

```bash
git clone https://github.com/nnlgsakib/membuss.git
cd membuss
```

### 2. Build the Project

```bash
# Build the unified membuss binary
go build -o membuss ./cmd/membuss

# Verify the build
./membuss version
```

### 3. Run Tests

Ensure all unit tests and integration suites pass locally:

```bash
# Run core test suites
go test -v ./...

# Run specific protocol tests
go test -v ./core/erasure
go test -v ./net/memex_v2
go test -v ./core/memedge
```

---

## 🔄 Development Workflow

We follow a standard Git branching and Conventional Commits workflow:

### 1. Create a Topic Branch

Always create a dedicated branch for your work branching off `master` (or the active development branch):

```bash
git checkout -b feat/your-feature-name
# or
git checkout -b fix/your-bug-fix
```

### 2. Commit Message Guidelines

Use **Conventional Commits** formatting to ensure clear changelog generation:

- `feat(scope)`: A new feature or capability (e.g. `feat(memedge): add support for custom WASI environment variables`)
- `fix(scope)`: A bug fix (e.g. `fix(dht): fix protobuf validator for relay records`)
- `perf(scope)`: A code change that improves performance (e.g. `perf(chunk): use sync.Pool for buffer allocation`)
- `docs(scope)`: Documentation changes (e.g. `docs(api): update gRPC endpoint specs`)
- `test(scope)`: Adding or correcting tests (e.g. `test(memex): add multi-peer race condition test`)
- `refactor(scope)`: Code refactoring without behavioral changes

### 3. Code Standards & Modularity

- **Keep It Modular**: Prefer creating focused, dedicated files and sub-packages over adding complexity to monolithic files.
- **Pure Go First**: Maintain zero CGo dependencies across core modules to preserve cross-compilation capability (`windows-amd64`, `linux-amd64`, `linux-arm64`, `darwin-arm64`).
- **Idiomatic Go**: Format your code with `gofmt` or `goimports`. Run `go vet ./...` before submitting.
- **Thread Safety**: Ensure all shared data structures are protected with appropriate synchronization primitives (`sync.RWMutex`, atomic counters).

---

## 📜 Updating Protocol Buffers

If you modify any `.proto` files in `proto/`, recompile the Go bindings using `protoc`:

```bash
protoc --go_out=. --go_opt=paths=source_relative proto/*.proto
```

---

## 🧪 Testing Guidelines

Every new feature or bug fix should include corresponding automated tests:

- **Unit Tests**: Place tests alongside code in `*_test.go` files.
- **Race Condition Testing**: Run tests with the race detector enabled:
  ```bash
  go test -race ./net/... ./core/...
  ```
- **Benchmarks**: For performance-critical code (chunking, erasure coding, DAG generation), include benchmarks:
  ```bash
  go test -bench=. -benchmem ./core/erasure
  ```

---

## 📬 Submitting a Pull Request (PR)

1. Ensure your branch is rebased on the latest `master`.
2. Ensure all tests pass (`go test ./...`).
3. Push your branch to your fork:
   ```bash
   git push origin feat/your-feature-name
   ```
4. Open a Pull Request on GitHub with a clear description of:
   - **What changed** and **why**.
   - Any relevant issue numbers (e.g., `Fixes #42`).
   - Steps taken to test and verify the change.

---

## 🔒 Security Disclosures

If you discover a potential security vulnerability in Membuss, please do **not** open a public issue. Instead, report it privately to the maintainers or via email at `security@membuss.network` so it can be addressed responsibly.

---

## 📄 License

By contributing to Membuss, you agree that your contributions will be licensed under the project's [Apache 2.0 License](LICENSE).
