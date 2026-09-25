# NOTE: This Makefile uses POSIX shell utilities (git, date, etc.) and is
# intended to be run on Linux or macOS. Windows developers must use WSL,
# Git Bash, or MSYS2 to invoke make targets locally.

COMMIT ?= $(shell git rev-parse --short HEAD)
DATE   ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Image tag. Defaults to a branch+commit tag for local dev. CI overrides it
# with the semantic-release version once image publishing lands (Phase 1) —
# e.g. `make docker-build-controlplane TAG=1.4.0` — so this target doesn't
# change between local and CI use, only the tag passed to it does.
TAG ?= $(shell git rev-parse --abbrev-ref HEAD | tr '/' '-')-$(COMMIT)

# Target platforms for docker-buildx-*/publish.
PLATFORMS ?= linux/amd64,linux/arm64

.PHONY: build build-controlplane build-worker build-cli build-mcp \
	docker-build-controlplane docker-build-worker docker-build-mcp docker-build-frontend \
	docker-buildx-controlplane docker-buildx-worker docker-buildx-mcp docker-buildx-frontend \
	publish test test-cover fmt fmt-check vet lint generate generate-check \
	setup pre-commit-hooks-update ci e2e

# ── build-* : compile a Go binary (go build, no Docker) ─────────────────────
# Output goes to bin/. Fast inner dev loop: compiling, running a binary
# directly, or a quick "does this still build" check.

build:
	go build ./...

build-controlplane:
	go build -o bin/controlplane ./controlplane/cmd/server

build-worker:
	go build -o bin/worker ./worker/cmd/worker

build-cli:
	go build -o bin/idpctl ./cli/cmd/idpctl

build-mcp:
	go build -o bin/mcp-server ./mcp/cmd/mcp-server

# ── docker-build-* : single-arch, --load'ed for local use ───────────────────

docker-build-controlplane:
	docker buildx build --load -t agentic-idp-controlplane:$(TAG) -f controlplane/docker/Dockerfile .

docker-build-worker:
	docker buildx build --load -t agentic-idp-worker:$(TAG) -f worker/docker/Dockerfile .

docker-build-mcp:
	docker buildx build --load -t agentic-idp-mcp:$(TAG) -f mcp/docker/Dockerfile .

docker-build-frontend:
	docker buildx build --load -t agentic-idp-frontend:$(TAG) -f frontend/docker/Dockerfile .

# ── docker-buildx-* : multi-platform, CI/publish only (no --load) ───────────

docker-buildx-controlplane:
	docker buildx build --platform $(PLATFORMS) -t agentic-idp-controlplane:$(TAG) -f controlplane/docker/Dockerfile .

docker-buildx-worker:
	docker buildx build --platform $(PLATFORMS) -t agentic-idp-worker:$(TAG) -f worker/docker/Dockerfile .

docker-buildx-mcp:
	docker buildx build --platform $(PLATFORMS) -t agentic-idp-mcp:$(TAG) -f mcp/docker/Dockerfile .

docker-buildx-frontend:
	docker buildx build --platform $(PLATFORMS) -t agentic-idp-frontend:$(TAG) -f frontend/docker/Dockerfile .

# publish pushes docker-buildx-* images to the registry. CI-only — see CLAUDE.md.
publish:
	@echo "publish is wired up in CI only — see .github/workflows/"

# ── Test ───────────────────────────────────────────────────────────────────────

test:
	go test ./...

test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

# ── Code quality ───────────────────────────────────────────────────────────────

fmt:
	gofmt -w -s .
	goimports -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needs to be run on:"; gofmt -l .; exit 1)

vet:
	go vet ./...

lint:
	golangci-lint run

# ── Codegen (sqlc) ───────────────────────────────────────────────────────────
# Query files under internal/<domain>/queries/*.sql are the source of truth;
# internal/<domain>/sqlcgen/ is generated and must never be hand-edited.

generate:
	sqlc generate

# Fails if the committed sqlcgen/ output doesn't match what queries/*.sql and
# the schema would generate — catches a query change committed without
# re-running `make generate`.
generate-check:
	sqlc diff

# ── Developer setup ────────────────────────────────────────────────────────────

# Install development tooling. Run once after cloning.
setup:
	GOTOOLCHAIN=local go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	@if ! command -v sqlc >/dev/null 2>&1; then \
		echo "sqlc not found — install via 'brew install sqlc' (see https://docs.sqlc.dev/en/latest/overview/install.html), then re-run 'make setup'"; \
	fi
	@if command -v pre-commit >/dev/null 2>&1; then \
		pre-commit install; \
		pre-commit install --hook-type commit-msg; \
	else \
		echo "pre-commit not found — install via 'pip install pre-commit' or 'brew install pre-commit', then re-run 'make setup'"; \
	fi

pre-commit-hooks-update:
	pre-commit clean
	pre-commit install-hooks

# ── CI ─────────────────────────────────────────────────────────────────────────

# Full local CI check — mirrors what the CI workflow runs on PRs. Native
# build only; the docker-build-* targets are exercised by CI's separate
# image-publishing job (from Phase 1 on), not this composite.
ci: fmt-check vet lint generate-check test build

# End-to-end proof of the Phase 0 milestone against the real images: docker
# compose up, worker self-registers, assumes a real (emulated) tier role.
# Same command locally and in CI. Requires a running Docker daemon.
e2e:
	./scripts/e2e.sh
