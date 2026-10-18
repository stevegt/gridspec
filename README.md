# specdot

`specdot vet` treats system specifications like source code. It compiles DOT
graphs and their linked Markdown contracts into deterministic IR, links symbols
across the complete module, runs semantic lint passes, and explores finite
state-machine profiles with a native Go checker.

The important distinction is that a diagram which renders is not necessarily a
specification which links. `specdot` rejects missing boundary contracts,
duplicate semantic IDs, dead flow paths, broken Markdown URLs, unmatched
promises, ambiguous state transitions, undefined data identity, and incomplete
or contradictory side-effect lifecycles.

## Build and run

```sh
go build -o specdot ./cmd/specdot
./specdot vet --profile build
./specdot vet --profile build --emit-ir specdot.ir.json
./specdot vet --format sarif > specdot.sarif
./specdot conform
```

With no paths, `vet` discovers `specdot.yaml` upward and compiles every configured
root. Passing a path narrows the requested entry point but does not narrow the
symbol table: cross-file validation always sees the complete module.

Exit status is `0` for a successful vet, `1` for validation errors or required
inconclusive proofs, and `2` for usage, configuration, or internal failures.

`specdot conform` validates the same canonical IR and then compares it with an
existing Go implementation using a strict `specdot.impl.yaml` sidecar. It
resolves Go symbols, runs isolated command fixtures and exact Go tests, checks
optional semantic traces, and enforces coverage floors plus a reviewed
baseline. Conformance executes repository code and must only be run on trusted
sidecars and source trees.

The repository's root manifest vets Specdot's own interfaces, data model,
pipeline, finite checker, lifecycle, effects, scenarios, and bootstrap boundary:

```sh
go run ./cmd/specdot vet --profile build
go run ./cmd/specdot conform
```

The smaller healthy example remains available as an isolated fixture:

```sh
go run ./cmd/specdot vet --config examples/healthy/specdot.yaml --profile build
```

The regression fixture derived from the CAR incident is deliberately invalid:

```sh
go run ./cmd/specdot vet --config testdata/car-misfire/specdot.yaml
```

It demonstrates three general failures: conflating logical and physical
identity, giving two agents unselected publication authority over one exclusive
resource, and deleting an artifact on outcomes where its contract requires
retention.

See [the self-specification](specs/vet.dot), [bootstrap decision](specs/architecture.md),
[language reference](docs/language.md), [rule reference](docs/rules.md), and
[healthy example](examples/healthy/worker.dot).

## Scope

Version 1 implements `specdot vet`, emits `specdot.ir/v1`, and compares that IR
with existing Go code through `specdot conform`. A future `specdot build` can
consume the sealed IR to generate code, tests, and documentation. The
build-readiness profile exists now so that generation does not have to guess
about interfaces, state, data, faults, persistence, trust, or destructive
behavior.

The self-specification and deterministic conformance report are dogfooding and
consistency gates. They do not claim that validation by the current Go
implementation proves that implementation correct; independent tests remain
part of the bootstrap authority until a future self-hosting design is reviewed.

The implementation is Go throughout. Graphviz is not a runtime dependency and
there is no Java-based verifier.
