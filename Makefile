.PHONY: build test lint vet fmt check clean

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -X github.com/jmrGrav/mcp-hugo-server-go/internal/server.Version=$(VERSION)
BIN := mcp-hugo-server-go

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/mcp-hugo-server-go

test:
	go test ./internal/...

race:
	go test -race ./internal/...

cover:
	go test -cover -coverprofile=coverage.out ./internal/...
	go tool cover -func=coverage.out | grep total

lint:
	staticcheck ./...

vet:
	go vet ./...

fmt:
	gofmt -l ./internal/ ./cmd/

vuln:
	govulncheck ./...

check: fmt vet lint test

clean:
	rm -f $(BIN) coverage.out
