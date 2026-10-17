# Worker machine

Source: [worker.dot](worker.dot).

```specdot
schema: specdot.contract/v1
for: machine.worker
kind: machine
summary: A finite worker lifecycle.
machine:
  agent: agent.worker
  semantics: run-to-completion
  events:
    - name: Start
      policy: handled
    - name: Finish
      policy: handled
claims:
  - id: claim.worker.done_reachable
    kind: reachable
    state: state.worker.done
    required: true
  - id: claim.worker.no_deadlock
    kind: deadlock_free
    required: true
  - id: claim.worker.transition_coverage
    kind: transition_coverage
    required: true
```
