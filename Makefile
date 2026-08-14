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
export CGO_ENABLED=0
PKG           := ./...

# Race detector toggle. The project has no cgo dependency, so it
# builds and tests fine with CGO_ENABLED=0 everywhere. The one thing
# that needs cgo is `go test -race`, which requires a working C
# toolchain. Default is OFF so the suite runs on any machine; opt in
# with `make test RACE=1` where a C compiler is available.
RACE          ?= 0
ifeq ($(RACE),1)
    TEST_FLAGS   := -race -count=1
    TEST_CGO     := 1
else
    TEST_FLAGS   := -count=1
    TEST_CGO     := 0
endif
BUILD_DIR     := bin
# Single unified binary: membuss is both the node daemon and the
# operator CLI (run the node with `membuss daemon start`).
MEMBUSS_BIN   := $(BUILD_DIR)/membuss$(BIN_EXT)
CONFIG_FILE   ?= membuss.yaml
FRONTEND_DIR  := explorer-web
NPM           ?= npm

# Docker knobs. Override on the command line, e.g.
#   make docker-push IMAGE=ghcr.io/nnlgsakib/membuss:latest
DOCKER        ?= docker
REGISTRY      ?= ghcr.io/nnlgsakib
IMAGE         ?= ghcr.io/nnlgsakib/membuss:latest
CONTAINER     ?= membuss
COMPOSE       ?= docker compose

.PHONY: build frontend frontend-dev proto test lint run-daemon tidy clean \
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
	CGO_ENABLED=$(TEST_CGO) $(GO) test $(PKG) $(TEST_FLAGS)

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
