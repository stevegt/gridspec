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

- `graph`, with graph kind/status, URL, attributes, and source location;
- `node` and `edge`, with a `graph_id`, semantic kind/status, attributes, and
  edge endpoints;
- `contract`, carrying typed agent, promise, machine, data, channel, effect,
  claim, waiver, latitude, and metadata fields;
- `document`, carrying exact UTF-8 Markdown content, its lowercase SHA-256,
  canonical path, and heading/link indexes that must rederive exactly from the
  content.

The state layer is extensible: unknown entity kinds are preserved by replay and
views even when the current semantic checker has no rules for them. Protocol
documents under [`protocols/`](../protocols/) freeze the exact Grid-CBOR slots,
pCIDs, CID profiles, and signing projection.

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
