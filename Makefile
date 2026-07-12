.PHONY: build test lint vet fmt check clean check-agent-ready smoke-agent-interop check-changelog check-readme-release check-release fuzz-smoke soak-local

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
RELEASE_VERSION ?=
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

fuzz-smoke:
	go test ./internal/security -run=^$$ -fuzz=FuzzPathGuardSafeJoin -fuzztime=3s
	go test ./internal/hugosite -run=^$$ -fuzz=FuzzSlugFromRel -fuzztime=3s
	go test ./internal/taxonomy -run=^$$ -fuzz=FuzzTaxonomyNormalization -fuzztime=3s
	go test ./internal/taxonomy -run=^$$ -fuzz=FuzzNormalizeAliasMap -fuzztime=3s
	go test ./internal/tools/write -run=^$$ -fuzz=FuzzApplyPageUpdatesRoundTrip -fuzztime=3s
	go test ./internal/tools/write -run=^$$ -fuzz=FuzzValidateFrontmatterRoundTrip -fuzztime=3s

check: fmt vet lint test

check-agent-ready:
	./scripts/check-agent-ready.sh

smoke-agent-interop:
	./scripts/smoke-agent-interop.sh

soak-local:
	./scripts/soak-local.sh

check-changelog:
	@test -n "$(RELEASE_VERSION)" || (echo "RELEASE_VERSION is required, e.g. make check-changelog RELEASE_VERSION=v1.2.11" >&2; exit 2)
	go run ./cmd/check-changelog -version "$(RELEASE_VERSION)"

check-readme-release:
	go run ./cmd/check-readme-release

check-release: check-changelog check-readme-release check-agent-ready smoke-agent-interop

clean:
	rm -f $(BIN) coverage.out
