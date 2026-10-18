# Bootstrap and future generation

[DOT source](architecture.dot)

The current Go implementation and independent unit/regression tests remain the
release bootstrap authority. This self-specification is normative design input
and a continuously checked consistency gate, but validating it with the same
implementation does not prove that implementation correct.

`specdot conform` adds deterministic symbol, scenario, and semantic-trace
evidence without claiming that self-validation is an independent proof. The
sealed `specdot.ir/v1` artifact remains the only intended input to a future
`specdot build`; generation and independently bootstrapped verification remain
deferred to that later milestone.

```specdot
schema: specdot.contract/v1
for: decision.specdot.bootstrap
kind: decision
summary: Separate present dogfooding guarantees from future self-hosted generation.
metadata:
  current_bootstrap: Go implementation plus independent tests
  self_spec_guarantee: internal consistency and reviewed behavioral intent
  current_conformance: Go symbol, command, test, trace, coverage, and baseline evidence
  future_generation_input: specdot.ir/v1
```
