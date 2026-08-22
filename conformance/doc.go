// Package conformance owns Kitwork's executable language corpus.
//
// The corpus is intentionally outside compiler and runtime unit tests. Every
// accepted fixture crosses the complete source, artifact, verifier, fresh VM,
// reused VM, and pooled VM boundary.
package conformance
