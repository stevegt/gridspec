# GridSpec Entity Version 1

Status: frozen protocol specification.

The protocol CID is CIDv1 raw/SHA-256 over this file's exact bytes.

An entity is deterministic Grid-CBOR with four array positions:

```text
grid([42(this pCID), kind, id, fields])
```

`kind` and `id` are nonempty UTF-8 text. `fields` is a deterministic CBOR map
whose keys are nonempty UTF-8 text and whose values are null, booleans, signed
64-bit integers, UTF-8 text, byte strings, arrays, or recursively constrained
text-keyed maps. Floats, tags, indefinite-length values, and duplicate keys are
invalid. Application rules define recognized kinds and fields.

