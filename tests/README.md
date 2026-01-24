# Kitwork Engine Tests

This directory contains the test suite for the Kitwork Engine.

## Structure

- **integration_test.go**: Main integration tests covering core features, workflows, and database interactions.
- **opcode_test.go**: Low-level tests for specific VM opcodes (Loops, Logic, Concurrency).
- **benchmark_test.go**: Performance benchmarks.

## Running Tests

Run all tests:
```bash
go test -v ./tests/...
```

Run benchmarks:
```bash
go test -bench=. ./tests/...
```
