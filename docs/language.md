# GridSpec language reference

GridSpec Version 1 is a finite transaction language. A program begins with:

```text
gridspec 1
```

It can replace module/profile metadata, put or delete an exact `(kind, id)`
entity, and assert preconditions:

```text
module "example.com/system"
profile "build"
put graph graph.worker { kind = "algorithm", status = "active" }
assert exists graph graph.worker
assert absent node worker.retired
assert equals graph graph.worker status "active"
delete node worker.retired
```

Values are quoted strings, signed 64-bit integers, booleans, null, arrays, and
maps. Kinds, IDs, and assertion fields are identifiers beginning with a letter
or underscore; subsequent bytes may also include digits, `.`, `-`, `:`, and
`/`. Programs are UTF-8 with LF line endings. Duplicate keys, unknown
statements, trailing input, CR line endings, byte-order marks, and integers
outside signed 64-bit range are rejected.

All statements execute atomically against an immutable base state. A failed
assertion or operation produces no result state. The language has no loops,
recursion, functions, imports, I/O, clocks, randomness, network access, or
process execution, so execution terminates in steps bounded by program size.

## State entities

Version 1 recognizes these checker-facing entity kinds:

- `graph`, with a human-readable title, optional summary, kind/status, URL,
  attributes, and source location;
- `node` and `edge`, with a human-readable title, optional summary, a
  `graph_id`, semantic kind/status, attributes, and edge endpoints;
- `contract`, carrying typed agent, promise, machine, data, channel, effect,
  scenario, claim, waiver, latitude, and metadata fields;
- `document`, carrying exact UTF-8 Markdown content, its lowercase SHA-256,
  canonical path, and heading/link indexes that must rederive exactly from the
  content.

The state layer is extensible: unknown entity kinds are preserved by replay and
views even when the current semantic checker has no rules for them. Protocol
documents under [`protocols/`](../protocols/) freeze the exact Grid-CBOR slots,
pCIDs, CID profiles, and signing projection.

In build-ready profiles, every active graph, node, and edge has a title. Titles
are trimmed, single-line text of at most 40 characters. Summaries, when
present, are trimmed, single-line text of at most 140 characters. These fields
are compiler input: an LLM may propose them, but they change authority only
through the normal immutable transaction and signed-acceptance path.

An executable scenario contract is a compiler-owned behavioral oracle. It
names an artifact, semantic IDs it covers, a repeat count, inline fixture files,
and one or more ordered command steps. Every step declares exact exit, stdout,
stderr, and file expectations. Paths must be safe relative slash paths; step
IDs and covered IDs are unique; repeats are 1–10; a scenario has at most 64
steps; fixture content is limited to 8 MiB; streams and expected files are
limited to 1 MiB; timeouts default to 5 seconds and cannot exceed 60 seconds.
These limits make the observation finite. They do not make arbitrary Go
equivalence decidable or provide an operating-system sandbox.

The same accepted contract qualifies both compiler output and an existing Go
implementation. Code generation and an implementation sidecar may map the
contract's artifact name to different Go packages, but neither may redefine
the steps or expected observations. Existing-code conformance builds the mapped
package without rewriting it and records the normalized scenario observations
in its report.

## Events and acceptance

An event records previous event, base state, language pCID, exact program CID,
result state, and required links. Unsigned and authored events use different
pCIDs. Deterministic replay must reproduce the recorded result CID.

Events are proposals. Candidate state is replayed and vetted before proposal
publication, then independently reloaded and vetted before acceptance. A
version becomes authoritative only when a signed Grid Commit v3 selects it in
the complete timeline-head map. See
[`architecture.md`](architecture.md) for trust, Git transport, LLM, and
decidability boundaries.

The previous DOT/Markdown grammar is retained only in
[`legacy-specdot-language.md`](legacy-specdot-language.md).
