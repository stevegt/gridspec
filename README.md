# GridSpec

GridSpec treats specifications as source code and Go as compiler output. Its
source of truth is a finite transactional language over immutable Grid-CBOR
state. An LLM can compile specs into Go or disassemble Go into a proposed spec
transaction, but deterministic replay, semantic checks, tests, and a separate
signed acceptance decide what becomes authoritative.

DOT and text are generated views. Legacy Specdot DOT/Markdown modules can be
imported, but they are no longer the primary editing format.

Transaction text is a change recipe, not a complete compiler input. GridSpec
replays it against a base CID and materializes the result in its Git-backed CAS;
views, checks, and generators then reload that exact state CID. This is similar
to building a Dockerfile into a digest-addressed local image before another
stage consumes it.

## Build and initialize

```sh
go build -o gridspec ./cmd/gridspec
gridspec init --project example.com/my/system
```

`init` creates an Ed25519 key with mode `0600` under `.git/gridspec/keys`, an
initial transaction event, and a signed Grid Commit v3 selection. The key is
never placed in the content-addressed closure or sent to an LLM.

Write or generate a transaction, publish it as a proposal, and explicitly
accept it:

```sh
gridspec propose change.gridspec
gridspec accept <proposal-CID>
gridspec vet --profile build
```

`propose` deterministically replays and vets the candidate state before it
publishes a proposal ref. `accept` reloads and vets that immutable proposal
again before signing the new complete timeline selection. The final `vet` is a
convenient explicit report of the now-accepted state.

Tool and LLM proposals are unsigned by default. `propose --authored` adds an
authorship signature when a person intentionally claims the transaction. In
both cases acceptance is a different signature over a complete timeline-head
map.

## Views

```sh
gridspec view --format text
gridspec view --format dot
gridspec view --format dot --graph graph.example.detail
gridspec view --format dot --graph graph.example.detail --label-format '{title}'
gridspec view --format dot --label-format '{title} [{id}]'
gridspec view --proposal <proposal-CID> --format text
```

The default DOT view is deliberately compact: one node per semantic graph.
Selecting a graph emits only that graph's nodes and edges. Views can be deleted
and regenerated without changing specification identity. IDs remain the
default labels. `--label-format` selects any canonical entity field or nested
map field, such as `{kind}` or `{attrs.specdot_branch}`; a missing value falls
back to the semantic ID. Double braces emit literal braces.

Active build-ready graphs, nodes, and edges carry authoritative human-readable
titles of at most 40 characters. Optional summaries are at most 140 characters.
Both are signed-state fields, not text inferred while rendering; DOT output
uses summaries as tooltips. An LLM can fill missing descriptions through an
unsigned proposal:

```sh
gridspec describe
gridspec describe --graph graph.example.detail
```

The command accepts only a complete, strict JSON description set, constructs
the replacement transaction itself without changing unrelated fields, and
links the exact request and response from the proposal event through CAS.
Review and accept the proposal separately.

Executable command scenarios use the same authority path:

```sh
gridspec scenarios                 # propose the missing command oracle
gridspec scenarios --graph ID      # fill one reviewed scenario graph
gridspec diff --proposal <CID>     # deterministic semantic review
gridspec accept <CID>              # separate signed decision
```

`diff` emits sorted recursive JSON-pointer changes in text or JSON. Its exit
status is 0 for equal states, 1 for differences, and 2 for usage or read errors.

If `build` or `update` finds no accepted executable scenario, it performs the
first step automatically, publishes only an audited unsigned spec proposal,
and stops without emitting code. A proposal preview with the same omission
fails instead of recursively proposing another proposal.

## Legacy migration

```sh
gridspec init --project example.com/legacy/system
gridspec import legacy --config specdot.yaml --profile build
gridspec accept <proposal-CID>
```

Migration first runs the existing completeness, uniqueness, URL, promise,
data-identity, effect-lifecycle, finite-state, and car-misfire checks. A broken
legacy module is not silently converted. The imported transaction is a proposal
until separately accepted.

## Go compiler, disassembler, and conformance

Set `OPENAI_API_KEY`; optionally set `GRIDSPEC_MODEL` (default `gpt-5.4`) or
`GRIDSPEC_OPENAI_BASE_URL`. GridSpec uses the OpenAI Responses API through a
provider interface that can be replaced in tests.

```sh
# Existing Go -> unsigned spec proposal
gridspec disassemble --source .

# Accepted specs -> a new, independently tested Go tree
gridspec build --output ../generated-system

# Current vetted proposal -> independently tested preview tree
gridspec build --proposal <proposal-CID> --output ../preview-system

# Existing Go vs accepted specs, without regeneration
gridspec conform --implementation gridspec.impl.yaml
gridspec conform --implementation gridspec.impl.yaml --format json --record

# Read-only comparison against a current proposal
gridspec conform --proposal <proposal-CID> --implementation gridspec.impl.yaml

# Specs -> changes only to hash-tracked generated files
gridspec update --source .          # dry run
gridspec update --source . --write  # tests and rolls back on failure
gridspec update --proposal <proposal-CID> --source . # preview only
```

Generated trees contain a `gridspec.generated/v3` manifest in
`.gridspec-generated.json`, recording whether the input was accepted or
proposed, its accepted-base commit, event and state CIDs, owned paths, content
hashes, contributing spec IDs, artifact-to-package mappings, and qualification
evidence metadata. Version 1 and 2 manifests remain readable and are upgraded
by the next successful update. `update` refuses to overwrite an unowned file or
a generated file changed by hand. New
update files must be under `gridspec_gen/`; proposal updates are dry-run only.
Dry-run updates reconstruct and qualify a temporary module without changing the
source tree. Write mode applies only those already-qualified changes after a
second ownership-hash check. Successful builds and update runs also publish an
unsigned derived-artifact message under
`refs/gridspec/candidates/...`, binding the accepted commit, normalized output
digest, provider provenance, exact CAS input CIDs, and verification evidence.
Proposal builds use preview targets and name both the proposal event and result
state. A candidate never changes accepted specification state.

Generated-code qualification is mandatory and independent of model-authored
tests. GridSpec first runs `go test ./...` as supplemental evidence, then builds
the declared artifacts and executes every accepted `contract.scenario` in a
fresh fixture workspace. Ordered steps are separate processes in the same
workspace, so persistence and restart behavior are observable. Exit status,
stdout, stderr, declared files, and undeclared file changes are checked exactly;
timeouts, repeats, fixture size, and output size are bounded. Exact normalized
observations and file hashes are stored as `gridspec.qualification/v1` evidence
through the existing artifact CAS envelope. Generated code executes in the
trusted local environment; this is resource bounding, not an OS sandbox.
Artifact mappings are restricted to `.` or `./`-relative packages inside the
staged generated module.

Conformance of hand-written code is evidence, not regeneration. It builds the
sidecar-mapped package and executes accepted `contract.scenario` commands
through the same runner used to qualify generated code. The sidecar maps
artifact names and Go symbols; it also retains supplemental Go-test and trace
mappings while those contracts migrate into accepted state. Its older
`command` form is deprecated and cannot override an accepted scenario with the
same ID. Conformance does not write the source module, and its JSON report
includes normalized accepted-scenario observations alongside static, command,
Go-test, trace, coverage, exclusion, and baseline evidence. These are bounded
observations, not a proof of arbitrary program equivalence.
`--record` explicitly publishes the exact report, checker configuration,
implementation digest, evidence classes, exclusions, and diagnostics as an
unsigned content-addressed candidate; ordinary conformance remains read-only.

## Git transport

GridSpec blocks are Git blobs in carrier commits. `ROOT` names the semantic CID;
Git commit IDs only provide transport and atomic refs.

```sh
gridspec fetch origin
gridspec push origin
```

These commands transfer only GridSpec project, proposal, and candidate refs,
not source branches. Fetch writes remote-tracking refs below
`refs/gridspec/remotes/` and never overwrites a local accepted ref. Every read
rehashes every block against its CID. Local ref advancement uses compare-and-swap.

## Guarantees and limits

The transaction language always terminates: it has no loops, recursion, I/O,
clocks, randomness, or process execution. General Go equivalence remains
undecidable. Compile/disassemble round trips are therefore partial and may be
lossy; the supported claim is agreement on declared contracts and selected
evidence.

Read [the architecture and theory boundary](docs/architecture.md), the
[transaction grammar](protocols/transaction-language-v1.md), the
[self-specification guide](specs/README.md), and the frozen protocol documents
under [`protocols/`](protocols/).
