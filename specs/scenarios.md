# Validation scenarios

## Clean validation

[DOT source](scenario-clean.dot)

A complete local module under a finite-required profile produces a sealed
digest, an empty diagnostic report, and process status 0.

```specdot
schema: specdot.contract/v1
for: scenario.specdot.clean
kind: scenario
summary: A complete module validates without findings.
```

## Semantic findings

[DOT source](scenario-findings.dot)

Syntax, linking, semantic, or violated-property errors remain diagnostics. The
report is emitted deterministically and the process exits with status 1.

```specdot
schema: specdot.contract/v1
for: scenario.specdot.findings
kind: scenario
summary: Validation findings are reported with process status 1.
```

## Required inconclusive check

[DOT source](scenario-required-inconclusive.dot)

Unsupported history semantics or exhausted bounds yield an inconclusive check.
When finite checking is required, that conclusion is an error and the process
exits with status 1 after reporting it.

```specdot
schema: specdot.contract/v1
for: scenario.specdot.required_inconclusive
kind: scenario
summary: Required finite work cannot silently pass when proof is inconclusive.
```

## Configuration failure

[DOT source](scenario-config-failure.dot)

A missing manifest, malformed strict YAML, unknown profile, or discovery error
is written to stderr and terminates with status 2 before a diagnostic report is
constructed.

```specdot
schema: specdot.contract/v1
for: scenario.specdot.config_failure
kind: scenario
summary: Usage and setup failures terminate before semantic validation.
```

## IR emission failure

[DOT source](scenario-emit-failure.dot)

If a requested temporary write, sync, close, or rename fails, the prior target
is retained, the error is written to stderr, and the process exits with status
2 without claiming successful publication.

```specdot
schema: specdot.contract/v1
for: scenario.specdot.emit_failure
kind: scenario
summary: Failed IR publication preserves the previous artifact and fails closed.
```

## Deterministic repeat

[DOT source](scenario-repeat.dot)

Two invocations over identical source bytes, configuration, profile, and
supported environment assumptions produce byte-identical normalized IR and the
same SHA-256 digest.

```specdot
schema: specdot.contract/v1
for: scenario.specdot.repeat
kind: scenario
summary: Identical validation inputs produce identical canonical IR.
```
