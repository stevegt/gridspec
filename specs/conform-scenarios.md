# Conformance scenarios

## Clean conformance

[DOT source](scenario-conform-clean.dot)

A clean conformance run vets the specification, resolves all declared Go
objects, observes both command and Go-test behavior, verifies required trace
events, classifies every active ID, meets its coverage floors, and preserves
the checked-in evidence baseline. It exits with status 0.

```specdot
schema: specdot.contract/v1
for: scenario.specdot.conform.clean
kind: scenario
summary: Reviewed implementation evidence agrees with the canonical specification.
```
