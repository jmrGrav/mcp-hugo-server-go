# Fuzzing

This repository keeps targeted Go fuzz tests close to the highest-risk parsers
and normalizers instead of relying only on example-based unit tests.

## Covered surfaces

- `internal/security`: `PathGuard.SafeJoin` / within-root path handling
- `internal/hugosite`: `SlugFromRel` bundle and multilingual slug parsing
- `internal/taxonomy`: slug/label/dedup/alias normalization
- `internal/tools/write`: front matter update and round-trip validation

## CI strategy

Default CI compiles and runs all fuzz targets through the normal `go test ./...`
path, which catches build breaks and keeps the seed corpus exercised.

Deeper fuzz execution is intentionally separate:

- short smoke: `make fuzz-smoke`
- longer local run: increase `-fuzztime`
- scheduled or manual robustness runs: execute the same commands outside the
  default fast CI path

This keeps the main pipeline predictable while still making fuzz targets part of
the checked-in test surface.

## Seed corpus guidance

When a fuzz run finds a real bug:

1. add the minimized reproducer as a deterministic regression test;
2. add the input as a new seed in the closest fuzz target;
3. document any new invariant if the bug changes the trust boundary.
