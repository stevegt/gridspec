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
./specdot vet
./specdot vet specs/other.dot --profile build --emit-ir specdot.ir.json
./specdot vet --format sarif > specdot.sarif
```

With no paths, `vet` discovers `specdot.yaml` upward and compiles every configured
root. Passing a path narrows the requested entry point but does not narrow the
symbol table: cross-file validation always sees the complete module.

Exit status is `0` for a successful vet, `1` for validation errors or required
inconclusive proofs, and `2` for usage, configuration, or internal failures.

The repository's own manifest vets a small module containing data, Promise
Theory, algorithm, state-machine, scenario, and Markdown sources:

```sh
go run ./cmd/specdot vet --profile build
```

The regression fixture derived from the CAR incident is deliberately invalid:

```sh
go run ./cmd/specdot vet --config testdata/car-misfire/specdot.yaml
```

It demonstrates three general failures: conflating logical and physical
identity, giving two agents unselected publication authority over one exclusive
resource, and deleting an artifact on outcomes where its contract requires
retention.

See [the language reference](docs/language.md), [rule reference](docs/rules.md),
and the [healthy example](examples/healthy/worker.dot).

## Scope

Version 1 implements `specdot vet` and emits `specdot.ir/v1`. A future
`specdot build` can consume that sealed IR to generate code, tests, and
documentation. The build-readiness profile exists now so that generation does
not have to guess about interfaces, state, data, faults, persistence, trust, or
destructive behavior.

The implementation is Go throughout. Graphviz is not a runtime dependency and
there is no Java-based verifier.
