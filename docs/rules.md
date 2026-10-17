# Rule reference

Rule IDs are stable machine-readable API. Text may become clearer without
changing the condition represented by an ID. JSON and SARIF output preserve the
same IDs.

| Range | Area | What is checked |
| --- | --- | --- |
| `SDV000`–`SDV025` | DOT syntax | A non-empty graph corpus, parseability, semantic graph metadata, directed executable graphs, explicit nodes, one declaration per DOT node, IDs on every node/edge, and unambiguous edge segments. |
| `SDV050`–`SDV055` | Markdown contracts | CommonMark traversal, valid strict YAML, safe YAML features, supported schema, and a `for` target. |
| `SDV100`–`SDV120` | Linking | Valid globally unique IDs, case collisions, one extension per target, existing contract targets and claims, status/dependency consistency, typed/accounted boundaries, visual-vs-semantic ambiguity, and misspelled semantic attributes. |
| `SDV130`–`SDV133` | Waivers | Waivability, required risk fields, date format, and expiry. |
| `SDV140`–`SDV149` | Documentation links | URL syntax and policy, online accessibility, supported schemes, local files, anchors, backlinks, and orphan contract documents. |
| `SDV180`–`SDV196` | Agents and observations | Agent-local control, capabilities, machine ownership, superagent membership, signals/impositions distinct from promises, and observer-local assessments with evidence. |
| `SDV200`–`SDV211` | Promise Theory | Promise edges, autonomous parties, polarity, nominal body types, CEL conditions, prerequisites, evidence vs authority, compatible offer/accept bindings, and acyclic prerequisites. |
| `SDV300`–`SDV308` | Algorithms/scenarios | Entries, exits, reachability, co-reachability, terminal flow, and fully named decision outcomes. |
| `SDV320`–`SDV356` | Machines | Finite variables, run-to-completion semantics, hierarchy/regions, events, guards/actions/timers, conflict resolution, reachability, event accounting, eventless cycles, entry/exit actions, pseudostates, claim validity, agent ownership, liveness assumptions, fork/join structure, and channel send/receive ownership and completeness. |
| `SDV400`–`SDV428` | Data | Type kinds and references, records/enums/maps/unions/collections, fields/cardinality, mutable identity, exclusive authority, evidence-vs-authority, logical/physical identity, replicas, encoding, migrations, authority agents, typed relationships, ownership, and acyclic containment. |
| `SDV500`–`SDV521` | Channels/effects | Typed agent endpoints, complete and valid delivery semantics, exactly-once mechanisms, controlling agent/resource, outcome coverage, destructive recovery contracts, retention contradictions, and exclusive multi-publisher conflicts. |
| `SDV600`–`SDV607` | Build readiness | Effective contracts, machine ownership, scenarios, constrained latitude, secrets, trust boundaries, and graph-level documentation. |
| `SDV700`–`SDV701` | Finite verification | Counterexamples and inconclusive bounded exploration. |

## Critical examples

- `SDV101`: one semantic ID is declared more than once.
- `SDV114`: an input/output is not accounted for in another DOT file.
- `SDV145` / `SDV147`: a linked Markdown file or anchor is missing.
- `SDV209` / `SDV210`: a promise offer or acceptance has no compatible
  autonomous counterpart.
- `SDV211`: promise prerequisites form a cycle.
- `SDV337`: an accepted event has no handled/deferred/refused/ignored outcome
  in a state.
- `SDV411`: a physical identity type is conflated with logical identity.
- `SDV520`: a destructive effect deletes an artifact on an outcome where the
  data contract requires retention.
- `SDV521`: multiple agents publish one exclusive resource without a selection
  policy.
- `SDV700`: finite exploration produced a counterexample trace.
- `SDV701`: a proof could not finish within its model or configured bounds.

The safety-critical non-waivable set is defined in
[`internal/rules/rules.go`](../internal/rules/rules.go). Profile severity
overrides can disable advisory rules, but they should be reviewed like compiler
flag changes.
