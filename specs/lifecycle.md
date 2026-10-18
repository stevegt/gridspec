# Invocation lifecycle

[DOT source](lifecycle.dot)

One process accepts a `Start` event. `Advance` abstracts completion and the
filesystem, configuration, validation, and reporting outcomes observed at each
stage; the implementation remains deterministic for a fixed environment and
request. Each invocation terminates cleanly, with validation findings, or with
a usage/internal failure.

```specdot
schema: specdot.contract/v1
for: machine.specdot.vet
kind: machine
summary: Finite lifecycle of one specdot vet process.
machine:
  agent: agent.specdot
  semantics: run-to-completion
  events:
    - name: Start
      payload: type.vet.request
      policy: handled
    - name: Advance
      policy: handled
claims:
  - id: claim.vet.clean.reachable
    kind: reachable
    state: state.vet.clean
    required: true
  - id: claim.vet.findings.reachable
    kind: reachable
    state: state.vet.findings
    required: true
  - id: claim.vet.failure.reachable
    kind: reachable
    state: state.vet.failed
    required: true
  - id: claim.vet.transition.coverage
    kind: transition_coverage
    required: true
```
