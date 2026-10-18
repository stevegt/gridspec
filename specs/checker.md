# Finite checker

[DOT source](checker.dot)

The native checker canonicalizes configurations and explores them breadth first
within the selected finite bounds. Safety violations retain a shortest witness.
After reachable-state exploration, strongly connected components evaluate
liveness claims. Unsupported history semantics or exhausted bounds are
inconclusive rather than silently successful.

Every run is classified as `passed`, `violated`, `inconclusive`, `assumed`, or
`unchecked`. A profile with required finite checking converts inconclusive or
unchecked required work into a vet error.

```specdot
schema: specdot.contract/v1
for: graph.specdot.checker
kind: algorithm
summary: Bounded safety and liveness analysis over canonical machine configurations.
```
