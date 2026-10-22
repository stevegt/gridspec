# Implementation conformance

[DOT source](conform.dot)

`gridspec conform` compares clean accepted specification IR with an existing Go
implementation without regenerating or rewriting that implementation.
Language-specific mappings live in a strict `gridspec.implementation/v1`
sidecar rather than in graph attributes.

Static bindings resolve stable package objects with Go type information.
Accepted executable scenario contracts are the primary command oracle. The
sidecar maps their artifact names to Go packages; it cannot replace their steps
or expected observations. GridSpec executes them through the same bounded
runner used for generated code and records normalized observations in the
report. Exact sidecar-bound Go tests and optional deterministic
`gridspec.trace/v1` events remain supplemental. The deprecated sidecar command
form is migration-only and is rejected when the same scenario has an accepted
executable contract.

Every active semantic ID has at least one evidence class or an explicit
reviewed exclusion. A checked-in baseline preserves the evidence class
previously established for each ID. Coverage floors and an exclusion limit
prevent new specification surface from silently weakening the comparison.
Baseline emission is explicit and produces a review candidate; a normal run
never blesses regressions.

```specdot
schema: specdot.contract/v1
for: graph.specdot.conform
kind: algorithm
summary: Deterministically compare a vetted specification with existing Go code.
metadata:
  implementation_schema: gridspec.implementation/v1
  report_schema: gridspec.conformance/v1
  trace_schema: gridspec.trace/v1
  baseline_schema: gridspec.conformance-baseline/v1
  command_authority: accepted executable scenario contracts
  shared_runner: generated and existing Go use the same accepted-scenario runner
  sidecar_role: artifact and symbol mappings plus supplemental test and trace evidence
```
