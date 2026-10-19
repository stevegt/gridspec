# GridSpec Conformance Evidence Version 1

Status: frozen protocol specification.

The protocol CID is CIDv1 raw/SHA-256 over this file's exact bytes.

Conformance evidence is an unsigned deterministic Grid-CBOR message binding an
accepted Grid Commit v3 CID, an implementation tree digest, the checker
configuration digest, evidence-class results, exclusions, diagnostics, and the
complete report content CID. It reports bounded evidence and never claims
general semantic equivalence between arbitrary programs.

The wire value is deterministic Grid-CBOR:

```text
grid([pCID, accepted_commit, implementation_digest, checker_configuration,
      evidence_results, exclusions, diagnostics, report])
```

`accepted_commit` is the selected Grid Commit v3 CID.
`implementation_digest` is a nonempty UTF-8 digest string. Every other field is
a canonical tag-42 CIDv1 link to exact bytes. The evidence message and all
linked content form its required closure. The evidence CID uses the Grid-CBOR
codec with SHA-256.
