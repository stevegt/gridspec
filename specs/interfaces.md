# Interfaces

[DOT source](interfaces.dot)

An operator autonomously offers one validation request. Specdot separately
accepts that request, evaluates the complete configured module, and offers one
result. The result includes the process outcome and any diagnostics; accepting
the result does not imply that the operator agrees with its findings.

```specdot
schema: specdot.contract/v1
for: graph.specdot.interfaces
kind: promise
summary: Operator-facing request and result promises for one vet invocation.
```

The same operator may independently offer an implementation-conformance
request and accept its result. Conformance acceptance authorizes execution of
the trusted repository's configured build and test commands; it does not grant
the implementation authority to edit specifications or accept a baseline.

## Specdot agent

The Specdot process controls its generated IR and report outputs. Reading a
module or probing a remote contract is a capability, not authority over the
source or remote content.

```specdot
schema: specdot.contract/v1
for: agent.specdot
kind: agent
summary: One autonomous Specdot validation process.
agent:
  controls:
    - resource.ir.output
    - resource.report.stream
  capabilities:
    - promise.specdot.request.accept
    - promise.specdot.result.offer
    - promise.specdot.conform.request.accept
    - promise.specdot.conform.result.offer
    - effect.specdot.read.module
    - effect.specdot.read.implementation
    - effect.specdot.execute.implementation
    - effect.specdot.probe.remote
    - effect.specdot.replace.ir
    - effect.specdot.replace.baseline
    - effect.specdot.write.report
  machines:
    - machine.specdot.vet
```
