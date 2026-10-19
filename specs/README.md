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
proposal workflow.
