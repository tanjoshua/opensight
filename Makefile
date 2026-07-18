BIN := ./bin/opensight
# Use golangci-lint from PATH if present, otherwise the locally installed binary.
GOLANGCI ?= $(shell command -v golangci-lint 2>/dev/null || echo ./bin/golangci-lint)

.PHONY: build test lint

build:
	go build -o $(BIN) ./cmd/opensight

test:
	go test ./...

lint:
	$(GOLANGCI) run
