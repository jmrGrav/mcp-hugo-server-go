.PHONY: build test test-contracts race race-critical lint vet fmt check clean check-agent-ready smoke-agent-interop check-changelog check-readme-release check-release fuzz-smoke soak-local bench-core gosec source-loc source-loc-badge check-loc-badge install-scc check-deps-fresh check-npm-version-sync

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
RELEASE_VERSION ?=
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo)
BUILD_CHANNEL ?= $(if $(RELEASE_VERSION),release,$(firstword $(subst -, ,$(VERSION))))
LDFLAGS := -X github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo.Version=$(VERSION) -X github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo.ReleaseVersion=$(RELEASE_VERSION) -X github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo.Commit=$(COMMIT) -X github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo.BuildChannel=$(BUILD_CHANNEL)
BIN := mcp-hugo-server-go
SCC_VERSION := v3.7.0
SCC_BIN := $(shell go env GOPATH)/bin/scc
RACE_CRITICAL_TIMEOUT ?= 90s
RACE_CRITICAL_TESTS := ^(TestConcurrentUpdatePageSamePageDeterministicOutcome|TestCreatePageIdempotencyKeyRaceOnConcurrentRetries|TestChunkedUploadStoreOverlappingRetryDoesNotDoubleAppend|TestBuildSiteConcurrentReject|TestStoreGetBySessionConcurrentWithEstablishNoRace|TestStoreSessionActivationDoesNotRace|TestRateLimiterConcurrentAccessIsRaceFreeAndBucketAccurate|TestRefreshTokenGrantIsSingleUseUnderConcurrentRace|TestGuardForeignChangeSetConcurrentWithCreatePageIsRaceFree|TestConcurrentApplyBundlePlanByDistinctPrincipalsIsolatesMutationStatus|TestConcurrentCreatePageByDistinctPrincipalsRacesRateLimitBucketsCorrectly|TestRecoveryJournalSurvivesReopenAndAdvancesStateInPlace)$$

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/mcp-hugo-server-go

test:
	go test ./internal/...

test-contracts:
	go test ./internal/... -run '^(TestContract|TestCrossTool)'

race:
	go test -race ./internal/...

# Fast, deterministic concurrency feedback for the shared-state paths most
# exposed by mutation/build refactors. This is intentionally a focused subset,
# not a replacement for `make race` or the full test suite.
race-critical:
	go test -race -count=1 -timeout=$(RACE_CRITICAL_TIMEOUT) \
		-run '$(RACE_CRITICAL_TESTS)' \
		./internal/tools/write ./internal/tools/admin ./internal/previewstore \
		./internal/oauth ./internal/server ./internal/db

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

check-deps-fresh:
	@./scripts/check-deps-fresh.sh

check-npm-version-sync:
	@./scripts/check-npm-version-sync.sh

gosec:
	go install github.com/securego/gosec/v2/cmd/gosec@v2.22.9
	"$(shell go env GOPATH)/bin/gosec" -tests=false ./cmd/... ./internal/...

install-scc:
	go install github.com/boyter/scc/v3@$(SCC_VERSION)

source-loc: install-scc
	@SCC_BIN="$(SCC_BIN)" ./scripts/source-loc.sh --print

source-loc-badge: install-scc
	@test -n "$(BADGE_JSON)" || (echo "BADGE_JSON is required" >&2; exit 2)
	@SCC_BIN="$(SCC_BIN)" ./scripts/source-loc.sh --badge-json "$(BADGE_JSON)"

check-loc-badge: install-scc
	@SCC_BIN="$(SCC_BIN)" ./scripts/test-source-loc.sh

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

bench-core:
	go test ./internal/site -run '^$$' -bench 'BenchmarkIndex(Search|GetBySlug|Sitemap|GetFeed)' -benchmem
	go test ./internal/tools/anonymous -run '^$$' -bench 'Benchmark(ListPages|GetPage)' -benchmem
	go test ./internal/tools/read -run '^$$' -bench 'BenchmarkSearchContent' -benchmem
	go test ./internal/db -run '^$$' -bench 'Benchmark(SyncSourcePage|StartupSync|Search)' -benchmem
	go test ./internal/tools/admin -run '^$$' -bench 'BenchmarkBuildOutputSummary' -benchmem

clean:
	rm -f $(BIN) coverage.out
