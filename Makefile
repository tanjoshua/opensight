BIN := ./bin/opensight
# Use golangci-lint from PATH if present, otherwise the locally installed binary.
GOLANGCI ?= $(shell command -v golangci-lint 2>/dev/null || echo ./bin/golangci-lint)

.PHONY: build test lint up down dev-stack dev-stack-down dev-stack-reset dev-serve dev-work

build:
	go build -o $(BIN) ./cmd/opensight

test:
	go test ./...

lint:
	$(GOLANGCI) run

up:
	./scripts/dev-up

down: dev-stack-down

dev-stack:
	docker compose -f compose.dev.yml up

dev-stack-down:
	docker compose -f compose.dev.yml down

dev-stack-reset:
	docker compose -f compose.dev.yml down -v

dev-serve:
	go tool air -c .air.serve.toml

dev-work:
	go tool air -c .air.work.toml
