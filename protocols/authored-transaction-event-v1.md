# GridSpec Authored Transaction Event Version 1

Status: frozen protocol specification.

The protocol CID is CIDv1 raw/SHA-256 over this file's exact bytes.

An authored event has the first seven positions and replay rules of GridSpec
Unsigned Transaction Event Version 1, followed by this proof map:

```text
{
  "agent_id": 42(AgentID),
  "algorithm": "Ed25519",
  "public_key": bstr32,
  "signature": bstr64
}
```

The Ed25519 signature covers the same Grid-CBOR event with the proof replaced
by the closed map containing `agent_id`, `algorithm`, and `public_key` but no
`signature`. Signature validity, receiver-local identity binding, trust,
selection by a signed Grid commit, and authorization of an external effect are
separate outcomes.

