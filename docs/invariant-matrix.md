# Invariant Matrix

This page maps critical runtime guarantees to automated proofs already present
in the repository. It is intended to make release review and hardening work
possible without reverse-engineering the entire test suite.

## How to read this matrix

- **Invariant**: the guarantee the project relies on.
- **Scope**: source content, public output, derived DB, or runtime behavior.
- **Automated proof**: the concrete tests that currently exercise the guarantee.
- **Notes**: what is intentionally out of scope or only partially covered.

## Core invariants

| Invariant | Scope | Automated proof | Notes |
| --- | --- | --- | --- |
| Source and in-memory source index stay aligned after create/update/delete | source / write | `internal/tools/write/property_test.go:TestWriteToolLifecycleProperty`, `internal/tools/write/tools_test.go:TestUpdatePageSuccess`, `internal/tools/write/tools_test.go:TestDeletePageSuccess` | Property test covers long random sequences; focused tests cover direct semantics. |
| Disk-reloaded source index matches the live source index after successful mutations | source / write | `internal/tools/write/property_test.go:TestWriteToolLifecycleProperty`, `internal/soak/soak_test.go:TestMutationBuildSoak` | Soak test rechecks invariants repeatedly under mixed operations. |
| No zombie public content remains after successful delete + build | public / write / build | `internal/tools/write/tools_test.go:TestDeletePageRemovesPublicArtifactsAndSiteDB`, `internal/soak/soak_test.go:TestMutationBuildSoak` | Delete path and repeated mutation/build loops both assert cleanup. |
| Published/public index agrees with built output after successful build | public / site index | `internal/soak/soak_test.go:TestMutationBuildSoak`, `internal/site/index_test.go:TestNewIndexEmpty` | Soak proves repeated rebuild agreement; unit tests cover index construction edges. |
| Draft source content never appears in anonymous source fallback | anonymous / source boundary | `internal/tools/anonymous/tools_test.go:TestGetPageDraftBlockedEvenWithSourceFallback` | Future/expired pages are covered by dedicated anonymous tests in other files when added. |
| External build commands respect timeout/cancellation | process execution / admin | `internal/tools/admin/build_test.go:TestBuildSiteTimeout`, `internal/tools/admin/preview_test.go:TestPreviewBuildTimeout` | Covers timeout classification and cancellation surface. |
| Build and preview reject concurrent mutation/build overlap | locking / runtime | `internal/tools/admin/build_test.go:TestBuildSiteConcurrentReject`, `internal/tools/admin/preview_test.go:TestPreviewBuildConcurrentReject` | Validates operator-visible `build_in_progress` behavior. |
| Derived DB remains coherent after public/source sync operations | derived DB | `internal/db/db_test.go:TestSyncPublicPage`, `internal/db/db_test.go:TestSyncSourcePage`, `internal/db/db_test.go:TestStartupSync`, `internal/db/db_test.go:TestDeletePage` | Does not prove crash consistency; only logical coherence of successful calls. |
| Path joins stay within configured roots | filesystem / security | `internal/security/pathguard_test.go`, `internal/security/pathguard_fuzz_test.go:FuzzPathGuardSafeJoin` | Fuzz smoke extends coverage across hostile path forms. |
| Slug resolution remains stable for branch bundles and multilingual files | slug / source index | `internal/hugosite/source_index_test.go`, `internal/hugosite/source_index_fuzz_test.go:FuzzSlugFromRel` | Fuzzing is canonicalization-focused, not product-contract validation. |
| Taxonomy normalization remains consistent across tools | taxonomy / read paths | `internal/taxonomy/taxonomy_test.go`, `internal/tools/read/cross_tool_taxonomy_test.go` | Cross-tool test is the contract proof; unit tests cover normalization mechanics. |

## Performance evidence

The following benchmarks exist to make regressions in hot paths visible:

- `internal/site/benchmark_test.go`
  - `BenchmarkIndexSearch`
  - `BenchmarkIndexGetBySlug`
  - `BenchmarkIndexSitemap`
  - `BenchmarkIndexGetFeed`
- `internal/tools/anonymous/benchmark_test.go`
  - `BenchmarkListPages`
  - `BenchmarkGetPage`
- `internal/db/benchmark_test.go`
  - `BenchmarkSyncSourcePage`
  - `BenchmarkStartupSync`
  - `BenchmarkSearch`
- `internal/tools/admin/build_benchmark_test.go`
  - `BenchmarkBuildOutputSummary`

Run them with:

```bash
make bench-core
```

## What this matrix does not prove yet

- crash consistency after `SIGKILL`
- ENOSPC / read-only filesystem rollback behavior
- long soak memory growth beyond the local harness duration
- external webhook retry/backoff guarantees

Those areas should stay explicitly tracked as hardening work instead of being
implicitly assumed.
