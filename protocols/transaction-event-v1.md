# GridSpec Unsigned Transaction Event Version 1

Status: frozen protocol specification.

The protocol CID is CIDv1 raw/SHA-256 over this file's exact bytes.

An unsigned proposal event is deterministic Grid-CBOR:

```text
grid([
  42(this pCID),
  null | 42(previous transaction-event CID),
  42(base state CID),
  42(GridSpec transaction-language pCID),
  42(raw program CID),
  42(result state CID),
  [42(required content CID), ...]
])
```

Required CIDs are unique and sorted by complete binary CID bytes. They include
the program and result state and every directly required content root. The
event is valid only when replaying the exact program against the exact base
state produces the named result state. The event makes no authorship claim.

