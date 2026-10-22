// Package loopback opens the SSRF guard of kitwork/http (utilities/http) to loopback addresses: the
// engine's tests and its synthetic canary serve their upstream on 127.0.0.1 with httptest. Since
// 22/10/2026 ALLOW_LOCAL does not turn the guard off (KITWORK_HTTP.md mục 9), and the package is
// internal so that only the engine's own packages reach this switch: no configuration, no Kitwork
// program, and no module that imports the engine (the host's included).
package loopback

import "sync/atomic"

var opened atomic.Bool

// Open lets requests reach 127.0.0.1 and ::1 through the SSRF guard until restore is called. Private,
// link-local and metadata addresses stay blocked.
func Open() (restore func()) {
	previous := opened.Swap(true)
	return func() { opened.Store(previous) }
}

// Opened reports whether loopback is open.
func Opened() bool { return opened.Load() }
