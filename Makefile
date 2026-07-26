BIN := ./bin/opensight
BUF := ./web/node_modules/.bin/buf
# Use golangci-lint from PATH if present, otherwise the locally installed binary.
GOLANGCI ?= $(shell command -v golangci-lint 2>/dev/null || echo ./bin/golangci-lint)

# Database the opt-in DB-backed integration tests run against. Kept separate
# from the dev database so a test run can't disturb local data.
TEST_DATABASE_URL ?= postgres://opensight:opensight@localhost:5432/opensight_test?sslmode=disable

.PHONY: build test test-integration lint proto up down dev-stack dev-stack-down dev-stack-reset dev-serve dev-work seed-dev clear-db test-db

build:
	go build -o $(BIN) ./cmd/opensight

# Skip Go source that npm packages ship inside web/node_modules.
test:
	go test $$(go list ./... | grep -v /node_modules/)

# Migrates $(TEST_DATABASE_URL) and runs the DB-backed integration tests.
# Locally, run `make test-db` first to create the database.
test-integration:
	DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/opensight migrate
	OPENSIGHT_STORE_TEST_DATABASE_URL="$(TEST_DATABASE_URL)" \
		go test -count=1 ./internal/store/... ./internal/workflows/... ./internal/metrics/...

# Drops and recreates the integration-test database in the dev Postgres container.
test-db:
	docker compose -f compose.dev.yml exec -T postgres psql -U opensight -d postgres \
		-c "DROP DATABASE IF EXISTS opensight_test WITH (FORCE);" \
		-c "CREATE DATABASE opensight_test OWNER opensight;"

lint:
	$(GOLANGCI) run

proto:
	$(BUF) format -w
	$(BUF) lint
	$(BUF) generate

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
