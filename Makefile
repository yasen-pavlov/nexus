.PHONY: test test-unit test-integration lint lint-sql coverage build build-cli swagger dev dev-db up down logs rag-eval help

help:
	@echo "Nexus — common targets:"
	@echo "  make up           Start full stack (app + deps) in Docker"
	@echo "  make down         Stop everything"
	@echo "  make dev          Run backend locally against containerized deps (db/opensearch/tika)"
	@echo "  make dev-db       Start just the deps (db/opensearch/tika)"
	@echo "  make test         Run all tests (unit + integration)"
	@echo "  make lint         Run golangci-lint"
	@echo "  make lint-sql     Run sqlfluff on every migration"
	@echo "  make rag-eval     Offline RAG quality eval → rag-eval-report.md"
	@echo "  make coverage     Run integration tests with coverage report"
	@echo "  make build        Build server binary to bin/nexus"
	@echo "  make build-cli    Build CLI/MCP client binary to bin/nexus-cli"

# Run all tests (unit + integration).
test: test-unit test-integration

# Unit tests only (no database required).
test-unit:
	go test ./internal/...

# Integration tests — containers start automatically via testcontainers-go.
# To reuse an already-running dev cluster (faster iteration), export:
#   NEXUS_TEST_DATABASE_URL=postgres://nexus:nexus@localhost:5432/nexus?sslmode=disable
#   NEXUS_TEST_OPENSEARCH_URL=http://localhost:9200
test-integration:
	go test -tags integration ./internal/...

lint:
	golangci-lint run ./...

# Run sqlfluff across every migration file (postgres dialect, config in
# `.sqlfluff`). Enforces cosmetic + structural rules; CI runs the same
# command so local and CI verdicts match exactly.
#
# Requires sqlfluff — install via `pipx install sqlfluff`. Auto-fixable
# findings can be resolved with `sqlfluff fix migrations/`.
lint-sql:
	sqlfluff lint migrations/

# Total-coverage floor. Override on the command line with `make coverage
# COVERAGE_FLOOR=95` to demand more.
COVERAGE_FLOOR ?= 90

# Coverage report (excludes testutil). Fails if total coverage drops below
# the floor — mirrors the check enforced in CI (.github/workflows/ci.yml).
coverage:
	go test -tags integration $$(go list ./internal/... | grep -v testutil) -coverprofile=coverage.out
	@total=$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
		echo "total: $${total}%"; \
		awk -v t="$${total}" -v f="$(COVERAGE_FLOOR)" 'BEGIN { exit (t + 0 < f + 0) ? 1 : 0 }' \
		  || { echo "Coverage $${total}% is below the $(COVERAGE_FLOOR)% floor."; exit 1; }
	@echo "Run 'go tool cover -html=coverage.out' for detailed report"

# Generate swagger docs (requires: go install github.com/swaggo/swag/cmd/swag@v1.8.12)
swagger:
	swag init -g cmd/nexus/main.go -o docs --parseDependency --parseInternal

# Build the server binary.
build: swagger
	go build -o bin/nexus ./cmd/nexus

# Build the CLI/MCP client binary. No swagger/frontend prerequisites — it only
# depends on the shared internal packages.
build-cli:
	go build -o bin/nexus-cli ./cmd/nexus-cli

# --- Containers ---------------------------------------------------------------

# Start deps only (Postgres, OpenSearch, Tika). Used by `make dev`.
dev-db:
	docker compose up -d db opensearch tika

# Start the full stack (app + deps). Requires .env — copy from .env.example.
up:
	@test -f .env || { echo "Missing .env — run: cp .env.example .env && edit it"; exit 1; }
	docker compose --profile app up -d

# Stop everything across all profiles.
down:
	docker compose --profile app --profile ollama down

logs:
	docker compose --profile app logs -f

# --- Local dev ----------------------------------------------------------------

# Run the backend locally against containerized deps. Loads NEXUS_ENCRYPTION_KEY /
# NEXUS_JWT_SECRET from .env so sessions + encrypted connector configs stay valid
# across `make dev` and `docker compose --profile app up`.
dev: dev-db
	@test -f .env || { echo "Missing .env — run: cp .env.example .env && edit it"; exit 1; }
	set -a && . ./.env && set +a && \
		NEXUS_DATABASE_URL=postgres://nexus:nexus@localhost:5432/nexus?sslmode=disable \
		NEXUS_OPENSEARCH_URL=http://localhost:9200 \
		NEXUS_TIKA_URL=http://localhost:9998 \
		NEXUS_FS_ROOT_PATH=./testdata \
		go run ./cmd/nexus

# Offline RAG quality eval against your live index. Same deps + env as the
# server (LLM keys come from .env). Runs the golden set through the
# orchestrator, judges with an LLM, and writes rag-eval-report.md diffed
# against the previous baseline. Pass extra flags via ARGS, e.g.
#   make rag-eval ARGS="-user muty -judge-model anthropic:claude-sonnet-4-6"
rag-eval: dev-db
	@test -f .env || { echo "Missing .env — run: cp .env.example .env && edit it"; exit 1; }
	set -a && . ./.env && set +a && \
		NEXUS_DATABASE_URL=postgres://nexus:nexus@localhost:5432/nexus?sslmode=disable \
		NEXUS_OPENSEARCH_URL=http://localhost:9200 \
		NEXUS_TIKA_URL=http://localhost:9998 \
		go run ./cmd/rag-eval $(ARGS)
