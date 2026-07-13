---
name: General issue
about: Fallback issue template for proven defects or scoped improvements
title: ""
labels: ""
---

## Type

- [ ] Bug
- [ ] Enhancement
- [ ] Documentation

## Summary

Describe the problem or improvement request concisely.

## Affected area

Tool, endpoint, workflow, runtime component, documentation area, or release surface.

## Evidence

Provide at least one:

- failing test
- reproducible request/response
- log excerpt
- file/line mismatch
- contract mismatch
- live behavior proof

Do not include secrets, bearer tokens, cookies, OAuth codes, or private config values.

## Reproduction

Exact steps, command, or request.

## Observed behavior

What happens now.

## Expected behavior

What should happen instead.

## Impact

Why this matters for users, operators, agents, security, or release quality.

## Suggested priority

- P0
- P1
- P2

## Checklist

- [ ] I searched existing open and closed issues
- [ ] I confirmed this is still present on current `main` or the current deployment
- [ ] I removed secrets and private credentials from the report
