## Summary

- what changed
- why it changed

## Linked issues

- closes #
- relates to #

## Root cause

Describe the actual failing path, invariant, or contract mismatch.

## Changes

- files touched
- behavior changed
- compatibility kept or intentionally changed

## Validation

List the commands you actually ran:

```bash
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
govulncheck ./...
```

Add any extra proof:

- smoke output
- failing test now green
- live verification
- screenshots
- log excerpts

## Risks

- remaining risk
- migration concern
- operator impact

## AI assistance

If AI was used, describe it briefly here.

Required standard:

- AI-generated code was reviewed by the author before submission
- AI-generated review comments or requested changes must be backed by proof
- no unresolved AI claim should remain without file/line, test, log, or contract evidence

## Review checklist

- [ ] Scope is small and coherent
- [ ] Tests or checks prove the change
- [ ] Docs updated if public behavior changed
- [ ] No secrets, tokens, or private config leaked
- [ ] Issue is not closed without proof
