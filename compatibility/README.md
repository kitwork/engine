# VM Compatibility Archive

This directory preserves executable evidence for the frozen Kitwork VM v2
contract. Normal tests load the committed `.kwpb` Program binaries directly;
they do not compile the adjacent sources.

The `.kwpb` files are internal compatibility fixtures, not a public storage or
distribution format. Compiler `.kwbc` artifacts are intentionally excluded:
they are short-lived cache entries tied to the current compiler fingerprint.

## Verify

```text
go test ./compatibility -run TestVMV2CompatibilityArchive -count=1
```

Each case proves source provenance, binary SHA-256, Program checksum, verifier
profile, deterministic re-encoding, execution result or diagnostic, VM reuse
hygiene, and pool hygiene.

## Update

Expectations in `testdata/v2/manifest.json` are authored by hand. Generate a new
archive only after reviewing those expectations:

```text
go run ./cmd/vmcompat --update
```

Once evidence exists, changing it requires a second explicit decision:

```text
go run ./cmd/vmcompat --update --replace
```

Do not replace v2 evidence merely because a newer compiler emits different
bytecode. If VM compatibility intentionally changes, make the version decision
first and normally create a new versioned archive rather than rewriting v2.
