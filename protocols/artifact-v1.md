# GridSpec Derived Artifact Version 1

Status: frozen protocol specification.

The protocol CID is CIDv1 raw/SHA-256 over this file's exact bytes.

A derived artifact is an unsigned deterministic Grid-CBOR message binding an
accepted Grid Commit v3 CID, target name, generator identity, generator input
evidence CIDs, output tree digest, and verification evidence CID. It records
exact compiler output but is never specification authority. A changed selected
specification head makes an artifact stale until a successor artifact names the
new accepted commit.

The wire value is deterministic Grid-CBOR:

```text
grid([pCID, accepted_commit, target, generator, inputs, output_digest,
      verification_evidence])
```

CID fields use canonical tag 42. `target`, `generator`, and `output_digest` are
nonempty UTF-8 strings. `inputs` is a duplicate-free array of CIDv1 values
sorted by complete CID bytes. `verification_evidence` is a CIDv1 link to exact
evidence bytes. The artifact CID uses the Grid-CBOR codec with SHA-256.
