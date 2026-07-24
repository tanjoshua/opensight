BIN := ./bin/opensight
# Use golangci-lint from PATH if present, otherwise the locally installed binary.
GOLANGCI ?= $(shell command -v golangci-lint 2>/dev/null || echo ./bin/golangci-lint)

.PHONY: build test lint up down dev-stack dev-stack-down dev-stack-reset dev-serve dev-work seed-dev clear-db

build:
	go build -o $(BIN) ./cmd/opensight

# Skip Go source that npm packages ship inside web/node_modules.
test:
	go test $$(go list ./... | grep -v /node_modules/)

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

seed-dev:
	go run ./cmd/opensight seed dev

# Drops and recreates the opensight app database, then reapplies migrations.
# Leaves containers running and doesn't touch Temporal's databases.
clear-db:
	docker compose -f compose.dev.yml exec -T postgres psql -U opensight -d postgres \
		-c "DROP DATABASE IF EXISTS opensight WITH (FORCE);" \
		-c "CREATE DATABASE opensight OWNER opensight;"
	go run ./cmd/opensight migrate
