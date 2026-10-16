# VM Compatibility Archive

This directory preserves executable evidence for the frozen Kitwork VM
contract. Normal tests load the committed `.kwpb` Program binaries directly;
they do not compile the adjacent sources.

`testdata/v4` is the archive of bytecode 4 (15/10/2026: STEP, which makes a
number of what `++` and `--` step, as JavaScript's ToNumber does), made by
compiler schema 11 from the cases `testdata/v3` holds, byte for byte, and one
more, `step`, that runs STEP and a division by zero. Every case of v3 spends the
energy it spent in v3; `loops` runs four instructions fewer.

`testdata/v3` is the archive of bytecode 3 (14/10/2026: seven wide forms and a
2 MiB program limit), made by compiler schema 8 from the cases `testdata/v2`
holds. `testdata/v2` and `testdata/v3` are bytecode 2's and bytecode 3's
evidence, kept unchanged; the current runtime refuses their Programs by their
version, and a test proves it for each.

The `.kwpb` files are internal compatibility fixtures, not a public storage or
distribution format. Compiler `.kwbc` artifacts are intentionally excluded:
they are short-lived cache entries tied to the current compiler fingerprint.

## Verify

```text
go test ./compatibility -run TestVMCompatibilityArchive -count=1
```

Each case proves source provenance, binary SHA-256, Program checksum, verifier
profile, deterministic re-encoding, execution result or diagnostic, VM reuse
hygiene, and pool hygiene.

## Update

Expectations in `testdata/v4/manifest.json` are authored by hand. Generate a new
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
