# Membuss Makefile
#
# Targets:
#   make build          build frontend, then compile the unified membuss binary into bin/
#   make frontend       build the explorer web UI (Vite)
#   make frontend-dev   run the explorer web UI in dev mode (Vite)
#   make proto          regenerate protobuf Go bindings into proto/
#   make test           run `go test ./... -count=1` (CGO off; no race)
#                       (set RACE=1 to enable the race detector where a
#                        C toolchain is available, e.g. `make test RACE=1`)
#   make lint           run golangci-lint (skipped if not installed)
#   make fuzz           replay fuzz seed corpora + saved counterexamples
#   make fuzz-all       fuzz every target FUZZTIME each (default 30s)
#   make fuzz-one       fuzz one target: PKG=./net/pex/ FUZZ=FuzzPEXReadMsg
#   make run-daemon     run the daemon with ./membuss.yaml
#   make tidy           go mod tidy
#   make clean          remove bin/, proto outputs, and frontend build artifacts
#
#   make docker-build   build the container image (tag: membuss:local)
#   make docker-run     run a one-off container with the named volume
#   make docker-stop    stop and remove the one-off container
#   make docker-logs    tail the container log
#   make docker-push    push the local image to a configurable registry
#   make docker-compose-up     docker compose up -d
#   make docker-compose-down   docker compose down -v
#   make docker-compose-logs   docker compose logs -f

# Detect OS for binary extension
ifeq ($(OS),Windows_NT)
    BIN_EXT := .exe
else
    BIN_EXT :=
endif

GO            ?= go
# Exported to child processes; RACE=1 flips it to 1 below (race
# detector needs cgo).
export CGO_ENABLED=0
PKG           := ./...

# Race detector toggle. Default OFF so the suite runs on any machine;
# opt in with `make test RACE=1` where a C compiler is available.
RACE          ?= 0
ifeq ($(RACE),1)
    TEST_FLAGS   := -race -count=1
    CGO_ENABLED  := 1
else
    TEST_FLAGS   := -count=1
endif

BUILD_DIR     := bin
# Single unified binary: membuss is both the node daemon and the
# operator CLI (run the node with `membuss daemon start`).
MEMBUSS_BIN   := $(BUILD_DIR)/membuss$(BIN_EXT)
CONFIG_FILE   ?= membuss.yaml
FRONTEND_DIR  := explorer-web
NPM           ?= npm

# Docker knobs. Override on the command line, e.g.
#   make docker-push IMAGE=ghcr.io/membuss-protocol/membuss:latest
DOCKER        ?= docker
REGISTRY      ?= ghcr.io/membuss-protocol
IMAGE         ?= ghcr.io/membuss-protocol/membuss:latest
CONTAINER     ?= membuss
COMPOSE       ?= docker compose

.PHONY: build frontend frontend-dev proto test lint fuzz fuzz-all fuzz-one run-daemon tidy clean \
        docker-build docker-run docker-stop docker-logs docker-push \
        docker-compose-up docker-compose-down docker-compose-logs

# Dynamic versioning linker flags
ifeq ($(OS),Windows_NT)
    GIT_COMMIT := $(shell git rev-parse HEAD 2>NUL || echo unknown)
    BUILD_TIME := $(shell powershell -Command "[DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')" 2>NUL || echo unknown)
else
    GIT_COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
    BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || echo unknown)
endif
LDFLAGS    := -ldflags "-X github.com/nnlgsakib/membuss/core/version.GitCommit=$(GIT_COMMIT) -X github.com/nnlgsakib/membuss/core/version.BuildTime=$(BUILD_TIME)"

build: frontend
	$(GO) build $(LDFLAGS) -o $(MEMBUSS_BIN)  ./cmd/membuss

frontend:
	cd $(FRONTEND_DIR) && $(NPM) install && $(NPM) run build

frontend-dev:
	cd $(FRONTEND_DIR) && $(NPM) run dev

proto:
ifeq ($(OS),Windows_NT)
	powershell -ExecutionPolicy Bypass -File scripts/gen-proto.ps1
else
	bash scripts/gen-proto.sh
endif

test:
	$(GO) test $(PKG) $(TEST_FLAGS)

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run $(PKG); \
	else \
		echo "golangci-lint not installed; skipping"; \
	fi

run-daemon: build
	$(MEMBUSS_BIN) daemon start -config $(CONFIG_FILE)

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BUILD_DIR) proto/*.pb.go
	rm -rf gateway/explorer/web/dist

# ---------------------------------------------------------------------------
# Docker
# ---------------------------------------------------------------------------

# docker-build produces a single image tagged $(IMAGE) using the
# multi-stage Dockerfile in the repo root. The build-arg BUILDDATE
# is propagated to the image label so CI builds can be traced.
docker-build:
	$(DOCKER) build -t $(IMAGE) \
		--build-arg BUILDDATE="$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		--build-arg GIT_COMMIT="$(GIT_COMMIT)" \
		--build-arg BUILD_TIME="$(BUILD_TIME)" .

# docker-run brings up a one-off container with the same port and
# volume layout that docker-compose.yml describes, but without
# the compose tool in the loop. Handy for local debugging.
docker-run: docker-build
	$(DOCKER) run -d --name $(CONTAINER) \
		-p 4001:4001/tcp \
		-p 4001:4001/udp \
		-p 5001:5001/tcp \
		-p 8080:8080/tcp \
		-p 50051:50051/tcp \
		-v membuss-data:/var/lib/membuss \
		$(IMAGE)

docker-stop:
	-$(DOCKER) rm -f $(CONTAINER)

docker-logs:
	$(DOCKER) logs -f $(CONTAINER)

# docker-push tags and pushes the local image to the configured container registry.
docker-push: docker-build
	$(DOCKER) push $(IMAGE)

docker-compose-up:
	$(COMPOSE) up -d --build

docker-compose-down:
	$(COMPOSE) down -v

docker-compose-logs:
	$(COMPOSE) logs -f

# ---------------------------------------------------------------------------
# Fuzzing (finding.txt XC-001)
# ---------------------------------------------------------------------------
#
# Go runs ONE fuzz target per command, so fuzz-all chains one rule
# per target. Targets:
#
#   make fuzz                       replay seed corpus + saved
#                                   counterexamples (fast, no mutation)
#   make fuzz-all                   actually fuzz every target for
#                                   FUZZTIME each (default 30s)
#   make fuzz-one PKG=... FUZZ=...  fuzz a single target
#
# A crash writes a counterexample to <pkg>/testdata/fuzz/<Name>/<hash>;
# `make fuzz` then replays it forever after as a regression test.

FUZZTIME ?= 30s

.PHONY: fuzz fuzz-all fuzz-one \
        fuzz-parse-range fuzz-pex fuzz-memex-frame fuzz-descriptor fuzz-dht-ns fuzz-keyring

fuzz:
	$(GO) test -run 'Fuzz' ./gateway/memgate_v2/ ./net/pex/ ./net/memex_v2/ ./core/descriptor/ ./net/dht/ ./core/keyring/

# One rule per target (Go fuzzes one function per process). Plain
# single-line recipes so they run identically under sh and cmd.exe
# (PowerShell-spawned make has no sh).
fuzz-all: fuzz-parse-range fuzz-pex fuzz-memex-frame fuzz-descriptor fuzz-dht-ns fuzz-keyring

fuzz-parse-range:
	@echo == FuzzParseRange gateway/memgate_v2 $(FUZZTIME) ==
	$(GO) test -run '^FuzzParseRange$$' -fuzz '^FuzzParseRange$$' -fuzztime $(FUZZTIME) ./gateway/memgate_v2/

fuzz-pex:
	@echo == FuzzPEXReadMsg net/pex $(FUZZTIME) ==
	$(GO) test -run '^FuzzPEXReadMsg$$' -fuzz '^FuzzPEXReadMsg$$' -fuzztime $(FUZZTIME) ./net/pex/

fuzz-memex-frame:
	@echo == FuzzMemexReadFrame net/memex_v2 $(FUZZTIME) ==
	$(GO) test -run '^FuzzMemexReadFrame$$' -fuzz '^FuzzMemexReadFrame$$' -fuzztime $(FUZZTIME) ./net/memex_v2/

fuzz-descriptor:
	@echo == FuzzDescriptorParse core/descriptor $(FUZZTIME) ==
	$(GO) test -run '^FuzzDescriptorParse$$' -fuzz '^FuzzDescriptorParse$$' -fuzztime $(FUZZTIME) ./core/descriptor/

fuzz-dht-ns:
	@echo == FuzzMemNSValidate net/dht $(FUZZTIME) ==
	$(GO) test -run '^FuzzMemNSValidate$$' -fuzz '^FuzzMemNSValidate$$' -fuzztime $(FUZZTIME) ./net/dht/

fuzz-keyring:
	@echo == FuzzKeyImportParse core/keyring $(FUZZTIME) ==
	$(GO) test -run '^FuzzKeyImportParse$$' -fuzz '^FuzzKeyImportParse$$' -fuzztime $(FUZZTIME) ./core/keyring/

# Single target, e.g.: make fuzz-one PKG=./net/pex/ FUZZ=FuzzPEXReadMsg
fuzz-one:
	$(GO) test -run '^$(FUZZ)$$' -fuzz '^$(FUZZ)$$' -fuzztime $(FUZZTIME) $(PKG)
