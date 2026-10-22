# Specifications

The accepted GridSpec state under `refs/gridspec/projects/...` is authoritative.
[`authority.gridspec`](authority.gridspec) is the reviewed incremental transaction for the
new authority model. [`cas-compilation.gridspec`](cas-compilation.gridspec) makes
CAS materialization mandatory for all downstream consumers and records the
accepted/proposal preview distinction. [`descriptions.gridspec`](descriptions.gridspec)
specifies strict model output, CAS audit evidence, and field-preserving
description proposals. [`gridspec.txn`](gridspec.txn) is a generated text
snapshot used for inspection and bootstrap recovery; regenerate it with:

```sh
gridspec view --format text --output specs/gridspec.txn
```

[`qualification.gridspec`](qualification.gridspec) adds the mandatory generated-
code qualification workflow and the first accepted compiler-owned command
oracle; [`qualification-failures.gridspec`](qualification-failures.gridspec)
adds the explicit Go-test and command-oracle rejection paths.
[`gridspec-qualification.dot`](../views/gridspec-qualification.dot) is
its generated detailed view. Richer persistence, failure, upper-bound, dry-run,
and update behavior is exercised by the persistent-counter closed-loop
integration fixture.

The `.dot` and `.md` files in this directory are retained as legacy migration
inputs and historical provenance. They are not edited as the source of truth.
Generate compact diagrams with `gridspec view --format dot`; use `--graph ID`
for one detailed graph and `--label-format '{title}'` or another field template
when human-readable labels are useful. IDs remain the default. Missing titles
and summaries can be proposed with `gridspec describe`; the command never
accepts its own output. The checked-in
[`gridspec-authority.dot`](../views/gridspec-authority.dot) is the generated
view of the transactional authority and compilation boundary;
[`gridspec-describe.dot`](../views/gridspec-describe.dot) shows the description
proposal workflow; `gridspec scenarios` uses the same audited-proposal and
separate-acceptance boundary for missing command oracles.
