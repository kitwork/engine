# Kitwork Language Conformance Corpus

This corpus freezes observable language behavior independently from parser,
compiler, and runtime unit tests. Sources live in `testdata/`; `corpus.json`
declares whether each source must execute, return a structured diagnostic, or
be rejected during compilation.

Every accepted source crosses this boundary:

```text
source file
  -> native compiler
  -> bytecode artifact encode/decode/re-encode
  -> verifier-owned immutable Program
  -> fresh VM
  -> previously-used VM
  -> released and reacquired app VM pool lease
```

The three executions must publish the same variables, result or diagnostic,
instruction count, energy, and final stack/frame state. Add a fixture whenever
language semantics change. Keep host capabilities out of this corpus; their
contracts belong to end-to-end route tests.

Run it directly with:

```text
go test ./conformance -run TestLanguageConformanceCorpus -count=1
```
