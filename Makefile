BIN := pgopt
PKG := ./cmd/pgopt
PGOPT_DB ?= postgres://pgopt:pgopt@localhost:55432/pgopt?sslmode=disable

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.Date=$(DATE)

.PHONY: all build install test vet lint clean run fixtures \
	docker-up docker-down docker-logs docker-psql demo

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

install:
	go install -ldflags "$(LDFLAGS)" $(PKG)

test:
	go test ./...

vet:
	go vet ./...

# Requires staticcheck; optional
lint:
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "(staticcheck not installed, skipping)"

clean:
	rm -f $(BIN)
	go clean ./...

# Demo: run the tool against every fixture
fixtures: build
	@for f in testdata/bad/*.sql; do \
		echo "━━━━━━ $$f ━━━━━━"; \
		./$(BIN) --verbose $$f || true; \
		echo ""; \
	done

# Quick demo on stdin
demo: build
	@echo "SELECT * FROM users WHERE lower(email) = 'a@b.com' LIMIT 10 OFFSET 100000;" | ./$(BIN) --verbose -

# ────────────────────────────────────────────────────────────────
# Docker: stack con PostgreSQL + seed, usado para demos y tests de
# integración contra una DB real.
# ────────────────────────────────────────────────────────────────

docker-up:
	docker compose up -d
	@echo "Waiting for PostgreSQL to become ready..."
	@for i in $$(seq 1 30); do \
		docker compose exec -T postgres pg_isready -U pgopt -d pgopt >/dev/null 2>&1 && break || sleep 1; \
	done
	@echo "✓ PostgreSQL ready on localhost:55432"
	@echo "  export PGOPT_DB='$(PGOPT_DB)'"

docker-down:
	docker compose down -v

docker-logs:
	docker compose logs -f postgres

docker-psql:
	docker compose exec postgres psql -U pgopt -d pgopt

# End-to-end demo: levanta docker, ejecuta pgopt contra ese DB
# y tira el stack al terminar. Idempotente.
e2e: build
	@$(MAKE) docker-up
	@echo ""
	@echo "━━━━━━━ pgopt --db (schema-aware only) ━━━━━━━"
	./$(BIN) --db "$(PGOPT_DB)" --verbose testdata/demo/missing_idx.sql || true
	@echo ""
	@echo "━━━━━━━ pgopt --db --explain (plan-based) ━━━━━━━"
	./$(BIN) --db "$(PGOPT_DB)" --explain --verbose testdata/demo/bad_query_1.sql || true
	@echo ""
	@echo "━━━━━━━ partition_key_unused ━━━━━━━"
	./$(BIN) --db "$(PGOPT_DB)" --verbose testdata/demo/partition_unused.sql || true
	@echo ""
	@echo "Leaving docker up — run 'make docker-down' to tear down."
