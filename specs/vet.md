# Vet pipeline

[DOT source](vet.dot)

Specdot resolves or discovers the manifest, loads a strict configuration,
selects a profile, and discovers the complete module before parsing any
requested entry point. Syntax diagnostics do not abort compilation: they join
semantic and finite-check diagnostics in the sealed result.

Configuration, discovery, sealing, emission, and reporting failures terminate
with process status 2. A successfully rendered report terminates with status 1
when errors remain and status 0 otherwise. IR emission occurs before reporting,
and selecting `--emit-ir -` moves the report to stderr.

```specdot
schema: specdot.contract/v1
for: graph.specdot.vet
kind: algorithm
summary: Complete orchestration and process outcomes for specdot vet.
```
