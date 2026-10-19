# GridSpec State Version 1

Status: frozen protocol specification.

The protocol CID is CIDv1 raw/SHA-256 over this file's exact bytes.

A state is deterministic Grid-CBOR with four array positions:

```text
grid([42(this pCID), module, profile, 42(entity reference-set root CID)])
```

`module` and `profile` are nonempty UTF-8 text. The reference set maps the
UTF-8 bytes `kind`, one zero byte, then `id` to deterministic CBOR containing
one tag-42 Grid-CBOR entity CID. Every entity key must agree with the entity it
names. A state is valid only when the complete reference-set and entity closure
is present, canonical, and contains no duplicate semantic identity.

