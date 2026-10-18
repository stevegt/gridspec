# Data model

[DOT source](data.dot)

The public request and result are nominal values. Parsed syntax, canonical IR,
diagnostics, and finite-check results remain distinct even when their Go
representations share primitive encodings.

```specdot
schema: specdot.contract/v1
for: graph.specdot.data
kind: data
summary: Nominal inputs, intermediate representations, diagnostics, and output resources.
```

## Parsed and canonical values

```specdot
schema: specdot.contract/v1
for: type.source
kind: type
data:
  type: record
  fields:
    - {name: path, type: type.filesystem.path}
    - {name: kind, type: string}
    - {name: sha256, type: string}
  encoding: process-memory
```

```specdot
schema: specdot.contract/v1
for: type.parsed.module
kind: type
data:
  type: record
  fields:
    - {name: source_count, type: int}
    - {name: graph_count, type: int}
    - {name: contract_count, type: int}
    - {name: diagnostic_count, type: int}
  encoding: process-memory
```

```specdot
schema: specdot.contract/v1
for: type.ir.graph
kind: type
data:
  type: record
  fields:
    - {name: id, type: string}
    - {name: kind, type: string}
    - {name: status, type: string}
  encoding: json
```

```specdot
schema: specdot.contract/v1
for: type.ir.contract
kind: type
data:
  type: record
  fields:
    - {name: for_id, type: string}
    - {name: kind, type: string}
    - {name: status, type: string}
  encoding: json
```

```specdot
schema: specdot.contract/v1
for: type.ir.binding
kind: type
data:
  type: record
  fields:
    - {name: offer, type: string}
    - {name: acceptance, type: string}
    - {name: body_type, type: string}
  encoding: json
```

```specdot
schema: specdot.contract/v1
for: type.diagnostic
kind: type
data:
  type: record
  fields:
    - {name: rule, type: string}
    - {name: severity, type: string}
    - {name: message, type: string}
    - {name: file, type: type.filesystem.path, optional: true}
  encoding: process-memory
```

```specdot
schema: specdot.contract/v1
for: type.check.run
kind: type
data:
  type: record
  fields:
    - {name: machine, type: string}
    - {name: status, type: string}
    - {name: states, type: int}
    - {name: steps, type: int}
  encoding: process-memory
```

```specdot
schema: specdot.contract/v1
for: type.canonical.ir
kind: type
data:
  type: record
  fields:
    - {name: schema, type: string}
    - {name: module, type: string}
    - {name: profile, type: string}
    - {name: sources, type: type.source.list}
    - {name: graphs, type: type.ir.graph.list}
    - {name: contracts, type: type.ir.contract.list}
    - {name: bindings, type: type.ir.binding.list}
    - {name: checks, type: type.check.run.list}
    - {name: digest, type: string}
  encoding: json
  version: specdot.ir/v1
  migration: explicit-version-upgrade
```

## Invocation boundary

```specdot
schema: specdot.contract/v1
for: type.vet.request
kind: type
data:
  type: record
  fields:
    - {name: config_path, type: type.filesystem.path, optional: true}
    - {name: profile, type: string}
    - {name: format, type: type.report.format}
    - {name: online, type: bool}
    - {name: emit_ir, type: type.filesystem.path, optional: true}
    - {name: paths, type: type.filesystem.path.list}
  encoding: process-memory
```

```specdot
schema: specdot.contract/v1
for: type.vet.result
kind: type
data:
  type: record
  fields:
    - {name: exit_code, type: type.exit.code}
    - {name: format, type: type.report.format}
    - {name: digest, type: string, optional: true}
    - {name: diagnostics, type: type.diagnostic.list}
    - {name: ir_written, type: bool}
  encoding: process-memory
```

## Conformance values

```specdot
schema: specdot.contract/v1
for: type.implementation.sidecar
kind: type
data:
  type: record
  fields:
    - {name: schema, type: string}
    - {name: name, type: string}
    - {name: bindings, type: string}
    - {name: scenarios, type: string}
    - {name: coverage, type: string}
  encoding: yaml
  version: specdot.implementation/v1
  migration: explicit-version-upgrade
```

```specdot
schema: specdot.contract/v1
for: type.conformance.trace.event
kind: type
data:
  type: record
  fields:
    - {name: scenario_id, type: string}
    - {name: spec_id, type: string}
    - {name: phase, type: string, optional: true}
    - {name: outcome, type: string, optional: true}
    - {name: sequence, type: int}
  encoding: ndjson
  version: specdot.trace/v1
  migration: explicit-version-upgrade
```

```specdot
schema: specdot.contract/v1
for: type.conformance.report
kind: type
data:
  type: record
  fields:
    - {name: implementation, type: string}
    - {name: spec_digest, type: string}
    - {name: evidence, type: string}
    - {name: coverage, type: string}
    - {name: diagnostics, type: type.diagnostic.list}
  encoding: json
  version: specdot.conformance/v1
  migration: explicit-version-upgrade
```

```specdot
schema: specdot.contract/v1
for: type.conformance.baseline
kind: type
data:
  type: record
  fields:
    - {name: implementation, type: string}
    - {name: spec_digest, type: string}
    - {name: evidence, type: string}
    - {name: exclusions, type: string}
  encoding: json
  version: specdot.conformance-baseline/v1
  migration: explicit-version-upgrade
```

```specdot
schema: specdot.contract/v1
for: type.conform.request
kind: type
data:
  type: record
  fields:
    - {name: config_path, type: type.filesystem.path, optional: true}
    - {name: implementation_path, type: type.filesystem.path, optional: true}
    - {name: profile, type: string}
    - {name: format, type: type.report.format}
    - {name: emit_baseline, type: type.filesystem.path, optional: true}
  encoding: process-memory
```

```specdot
schema: specdot.contract/v1
for: type.conform.result
kind: type
data:
  type: record
  fields:
    - {name: exit_code, type: type.exit.code}
    - {name: report, type: type.conformance.report}
    - {name: baseline_written, type: bool}
  encoding: process-memory
```

## Resources and identity

```specdot
schema: specdot.contract/v1
for: resource.spec.module
kind: resource
data:
  type: resource
  fields:
    - {name: manifest, type: type.filesystem.path}
    - {name: sources, type: type.source.list}
  mutable: false
  lifecycle: [discovered, loaded, parsed]
  encoding: dot-and-markdown
```

```specdot
schema: specdot.contract/v1
for: resource.remote.markdown
kind: resource
data:
  type: resource
  fields:
    - {name: url, type: string}
  mutable: false
  lifecycle: [declared, probed, accepted, rejected]
  encoding: markdown
```

```specdot
schema: specdot.contract/v1
for: resource.ir.output
kind: resource
data:
  type: resource
  fields:
    - {name: artifact_id, type: type.artifact.id}
    - {name: path, type: type.filesystem.path}
    - {name: digest, type: string}
  identity: [artifact_id]
  authority: agent.specdot
  mutable: true
  exclusive: true
  lifecycle: [absent, temporary, sealed, published]
  retention: [failure, unknown]
  physical_id_type: type.filesystem.path
  encoding: json
  version: specdot.ir/v1
  migration: explicit-version-upgrade
```

```specdot
schema: specdot.contract/v1
for: resource.report.stream
kind: resource
data:
  type: resource
  fields:
    - {name: invocation_id, type: type.invocation.id}
    - {name: format, type: type.report.format}
  identity: [invocation_id]
  authority: agent.specdot
  mutable: true
  exclusive: true
  lifecycle: [open, written, closed]
  encoding: text-json-or-sarif
```

```specdot
schema: specdot.contract/v1
for: resource.implementation.sidecar
kind: resource
data:
  type: resource
  fields:
    - {name: path, type: type.filesystem.path}
    - {name: implementation, type: type.implementation.sidecar}
  mutable: false
  lifecycle: [declared, loaded, validated]
  encoding: yaml
```

```specdot
schema: specdot.contract/v1
for: resource.go.module
kind: resource
data:
  type: resource
  fields:
    - {name: path, type: type.filesystem.path}
  mutable: false
  lifecycle: [declared, loaded, type_checked, executed]
  encoding: go-source
```

```specdot
schema: specdot.contract/v1
for: resource.conformance.baseline
kind: resource
data:
  type: resource
  fields:
    - {name: artifact_id, type: type.artifact.id}
    - {name: path, type: type.filesystem.path}
    - {name: baseline, type: type.conformance.baseline}
  identity: [artifact_id]
  authority: agent.specdot
  mutable: true
  exclusive: true
  lifecycle: [absent, candidate, reviewed, replaced]
  retention: [failure, unknown]
  physical_id_type: type.filesystem.path
  encoding: json
  version: specdot.conformance-baseline/v1
  migration: explicit-version-upgrade
```
