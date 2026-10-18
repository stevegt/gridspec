# Side effects

[DOT source](effects.dot)

Source reads and remote probes do not grant Specdot authority over their
content. Report publication is controlled by the current process. IR emission
writes a temporary file, synchronizes it, closes it, and renames it over the
target; failure or uncertainty retains the previously published target.

Conformance additionally reads a trusted implementation sidecar, invokes Go
builds, tests, and configured binaries, and may explicitly publish a baseline
candidate. Execution is never implied by `vet`; it occurs only under the
`conform` command. Baseline replacement uses the same atomic publication and
failure-retention policy as canonical IR emission.

```specdot
schema: specdot.contract/v1
for: graph.specdot.effects
kind: decision
summary: Filesystem, network, report, and canonical-IR effects of one vet invocation.
```
