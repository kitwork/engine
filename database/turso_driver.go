//go:build turso

// Turso backend activation point (build tag: `turso`).
//
// The default Kitwork build is a single pure-Go static binary: SQLite via modernc.org/sqlite,
// Postgres via lib/pq, NO cgo. The Turso Database backend (the Rust rewrite of SQLite, reached from
// Go through turso.tech/database/tursogo over purego — no cgo, but a native blob embedded and
// dlopen'd at runtime, ~15 MB per target) is a DELIBERATELY separate, opt-in build so default builds
// never link the dependency and never change their distribution shape.
//
// This file is compiled ONLY with `-tags turso`; the blank import below registers the "turso"
// database/sql driver. With it, database.Connect stops returning its "not compiled in" error,
// BuildDSN's tag-free `turso` case supplies the DSN, and the gated differential probe
// (turso_diff_test.go) stops skipping and measures the real compat gap. Build/test it with:
//
//	go build -tags turso ./...
//	go test  -tags turso ./database/...
//
// CAVEAT — `go mod tidy` prunes tagged deps: tidy does not enable custom build tags, so it sees
// nothing importing tursogo and DROPS the two turso requirements from go.mod. If that happens,
// restore them with `go get turso.tech/database/tursogo`. Cross-compiling WITH the tag works
// (turso-go-platform-libs ships per-target blobs — verified linux/amd64 from windows).
package database

import _ "turso.tech/database/tursogo" // registers the "turso" database/sql driver (no cgo; purego + native blob)

// TursoBuildTag is a compile-time breadcrumb present ONLY in `-tags turso` builds, so tooling and
// humans can confirm the tag took effect.
const TursoBuildTag = true
