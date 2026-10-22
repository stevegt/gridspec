# GridSpec architecture

## Authority model

GridSpec treats specifications as source code. The authoritative state is an
immutable Grid-CBOR snapshot reached by replaying finite GridSpec transactions.
DOT and prose are generated views; legacy DOT and Markdown are migration inputs.
Go is compiler output or an implementation being checked against the specs.

A transaction event binds the exact program bytes, base state, result state,
required links, and previous event. An event can be an unsigned tool/LLM
proposal or a signed independently authored transaction. Neither is accepted
merely because it exists. Accepted state is the `main` head selected by a
cryptographically signed Grid Commit v3 containing the complete timeline-head
map. This separation lets tools propose aggressively without granting them
authority.

## CAS materialization boundary

The content-addressed state is part of compilation, not an optional archive
written after compilation. GridSpec transaction text describes a change to a
base state; by itself it is not the complete specification. The compiler first
parses and replays the transaction, materializes the complete result closure in
CAS, and verifies the resulting state CID. Every downstream operation consumes
that CID:

```text
transaction bytes + base state CID
              -> deterministic replay
              -> materialized result state CID in CAS
              -> vet
              -> proposal or signed acceptance
              -> text/DOT views, conformance, documentation, and Go generation
```

There is no raw-transaction-to-LLM generation path. A generator reloads and
rehashes the selected state closure from CAS before rendering its model input.
Accepted state is the default compiler input. An operator may explicitly build
or inspect a current vetted proposal, but the proposal is still a materialized
state CID and remains distinct from signed acceptance.

The model is similar to Dockerfiles and image storage:

| Docker concept | GridSpec concept |
| --- | --- |
| Dockerfile or build instruction | GridSpec transaction program |
| base image digest | base state CID |
| layer transition and resulting image | transaction event and result state CID |
| local content store or registry | Git-backed Grid CAS |
| signed release manifest or digest-pinned tag | signed Grid Commit selecting timeline heads |
| consumer using `FROM ...@sha256` | view, checker, or generator using a state CID |

This is an analogy, not a wire-format claim. OCI layers are filesystem deltas;
GridSpec transactions are semantic transitions and the result is an
independently verified semantic snapshot. Git commits carry the blocks, but the
Grid CIDs—not Git object IDs or mutable refs—identify compiler inputs.

## Compiler and disassembler

The forward path is:

```text
accepted or explicitly selected proposal state CID -> verify CAS closure
               -> derived semantic IR -> LLM translation -> candidate Go
               -> deterministic shape checks -> gofmt -> staged module
               -> generated Go tests -> accepted command scenarios
               -> qualified output and CAS evidence -> review
```

The reverse path is:

```text
accepted state CID + existing Go -> deterministic source bundle
            -> LLM reconstruction
            -> parsed GridSpec transaction -> replay -> vet -> proposal
```

An LLM is therefore part of both the compiler and disassembler. Its output is
not merely commentary, but it is also not accepted on trust. The deterministic
shell checks syntax, paths, ownership, content hashes, state replay, semantic
rules, bounded models, and executable tests. A human or separately authorized
agent accepts the resulting transaction or code change.

## Shared executable qualification

Accepted specifications contain language-neutral `contract.scenario` command
oracles. Each names an artifact, covered semantic IDs, inline fixture files, a
repeat count, and ordered process steps with exact exit, stream, and file
expectations. Generated-file responses and implementation sidecars separately
map those artifact names to Go main packages. A package mapping selects code;
it cannot redefine accepted behavior. Model-authored Go tests and sidecar-bound
Go tests remain supplemental evidence. Generated artifact mappings may name
only `.` or a `./`-relative package inside the staged module.

`build` writes a temporary module, runs its Go tests, builds the mapped
artifacts, and executes every accepted scenario. Each repeat receives a fresh
fixture directory; steps within a repeat are separate processes sharing that
directory. This exposes persisted state and restart behavior. GridSpec rejects
timeouts, oversized output, mismatched observations, and filesystem changes a
step did not declare. Only a passing stage is renamed into the requested output
tree.

`update` uses the same gate. Even a dry run reconstructs the existing Go bundle
in a temporary module, applies the proposed owned-file changes there, and
qualifies that result without touching the source tree. Write mode rechecks the
source hashes after qualification and applies only the already-tested bytes,
with rollback protection for write failures.

`conform` applies the same accepted-scenario runner to hand-written Go without
regenerating or rewriting it. It builds each mapped artifact with workspace
mode disabled and module files read-only, then uses the same fresh fixtures,
ordered processes, limits, normalization, and exact comparisons as `build` and
`update`. The sidecar remains authoritative for artifact and symbol mappings,
coverage policy, and legacy Go-test or trace bindings. Its older command form
is accepted only for scenario IDs without an executable accepted contract; a
duplicate sidecar command oracle is an error.

The deterministic `gridspec.qualification/v1` report binds source identity,
output digest, artifact mappings, Go-test observations, normalized scenario
observations, and expected-file hashes. It is stored as the verification bytes
already linked by the derived-artifact v1 CAS message. The ownership manifest
v3 records the corresponding evidence digest and scenario IDs; v1 and v2
manifests remain readable.

Existing-code conformance stores the same normalized scenario observations in
its `gridspec.conformance/v1` report, then combines their covered IDs with
static, Go-test, trace, exclusion, floor, and baseline evidence. Ordinary
conformance is read-only unless the operator explicitly records the report as
an unsigned candidate.

When no executable scenario is accepted, compilation does not degrade to a
warning or unsafe mode. `gridspec scenarios` asks an LLM for bounded strict JSON,
constructs a preserving transaction itself, stores the exact exchange in CAS,
and publishes an unsigned proposal. `build` and `update` can initiate that same
proposal path, but emit no code until a separate signed acceptance. A proposal
preview never creates a proposal on top of itself.

`gridspec diff --proposal CID` recursively compares the two materialized states
and emits sorted JSON-pointer changes. It is deterministic review machinery;
it does not ask an LLM to decide whether the change is acceptable.

Compilation and disassembly are partial and potentially lossy. A specification
can intentionally leave implementation latitude; Go can contain optimizations,
incidental structure, or behavior not recoverable as design intent. Round trips
need not reproduce identical text:

```text
disassemble(compile(spec)) ~= spec
compile(disassemble(go))   ~= go
```

Here `~=` means agreement on explicitly selected observations and invariants,
not byte identity or unrestricted semantic equivalence.

## Signatures and Git carriers

GridSpec stores content-addressed Grid blocks as Git blobs under
`blocks/<CID-prefix>/<CID-rest>`. A carrier tree has a `ROOT` file containing
the root CID. Git commit IDs provide transport, reachability, atomic ref updates,
and compatibility with fetch/push; they are not GridSpec semantic identities.
Every block is rehashed against its CID when read.

CAS therefore serves the same two roles during local compilation and transport:
it is the verified local compiler-input store, and its Git carriers make those
inputs reachable elsewhere. Generation never bypasses the local CAS merely
because all transaction text happens to be available in the working tree.

Accepted carriers use:

```text
refs/gridspec/projects/<encoded-project>/<agent-CID>
```

Proposals use `refs/gridspec/proposals/...`; fetched refs are isolated below
`refs/gridspec/remotes/...`. Publication uses compare-and-swap ref updates so
concurrent acceptance cannot silently overwrite another head.
The signing key is generated with mode `0600` below the repository's Git
directory, configured by path, and excluded from carrier closures. LLM requests
receive specification or source bytes, never the key.

The Grid Commit v3 signature covers the protocol message, predecessor list,
complete timeline selection, metadata root, agent identity, algorithm, and
public key. Receiver-local policy binds that public key to an agent identity;
cryptographic validity alone does not establish trust.

## What is and is not provable

Nothing here violates the halting problem, Rice's theorem, or undecidability of
general program equivalence. GridSpec does not promise a total, semantics-
preserving compiler/disassembler pair for arbitrary Go programs. It makes
smaller claims with explicit boundaries:

- the transaction language has no loops, recursion, I/O, clocks, randomness,
  or process execution, so replay terminates in steps bounded by program size;
- Grid-CBOR encoding, CIDs, reference sets, signatures, and replay are exact;
- finite-state checks are conclusive only within their declared bounds and
  report `inconclusive` when limits prevent a proof;
- conformance combines static bindings, accepted command contracts, Go tests,
  semantic traces, coverage floors, exclusions, and reviewed baselines;
- generated Go is accepted only after deterministic validation and tests, which
  demonstrate selected behavior rather than proving all behavior;
- disassembly produces a hypothesis/proposal whose omissions and uncertainty
  must remain reviewable.

The impossible claim would be “for every Go program, automatically recover a
complete specification and prove the regenerated program equivalent.” GridSpec
does not make it.

## LLM boundary

LLMs are active transformation engines for code-to-spec, spec/IR-to-code,
repair suggestions, explanatory views, and other semantic translations. Calling
them “advisory” understates their role. The correct boundary is capability and
authority:

- they may create complete candidate transactions and complete candidate code;
- they may propose bounded graph, node, and edge descriptions, while the tool
  constructs field-preserving transactions and stores request/response audit
  data in CAS;
- their raw output is untrusted input to deterministic parsers and checkers;
- provider/model/response identity is recorded as provenance where useful;
- a model failure cannot mutate accepted state;
- a model never receives signing material or calls the acceptance operation;
- acceptance is a separate signed transaction-selection act.

Human-readable titles and summaries are part of accepted state. DOT renderers
never ask a model to invent labels: they use IDs by default and may format any
stored entity field explicitly. This keeps identical state CIDs reproducible
across machines and makes descriptive changes reviewable like code changes.

See the frozen wire descriptions in [`protocols/`](../protocols/) and the
transaction grammar in
[`transaction-language-v1.md`](../protocols/transaction-language-v1.md).
