# Implementation conformance

[DOT source](conform.dot)

`specdot conform` compares a clean canonical specification IR with an existing
Go implementation. It does not generate code. Language-specific bindings live
in a strict `specdot.implementation/v1` sidecar rather than in DOT attributes.

Static bindings resolve stable package objects with Go type information.
Behavioral evidence comes from isolated command scenarios, exact Go tests, and
optional deterministic `specdot.trace/v1` events. Every active semantic ID has
at least one evidence class or an explicit reviewed exclusion.

A checked-in baseline preserves the evidence class previously established for
each ID. Coverage floors and an exclusion limit prevent new specification
surface from silently weakening the comparison. Baseline emission is explicit
and produces a review candidate; a normal run never blesses regressions.

```specdot
schema: specdot.contract/v1
for: graph.specdot.conform
kind: algorithm
summary: Deterministically compare a vetted specification with existing Go code.
metadata:
  implementation_schema: specdot.implementation/v1
  report_schema: specdot.conformance/v1
  trace_schema: specdot.trace/v1
  baseline_schema: specdot.conformance-baseline/v1
```
