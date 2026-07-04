package hydrate

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed runtime.js
var runtimeJS string

// Runtime returns the client runtime source (tiny parser + IR walker, no eval). It is the same for
// every page and every tenant, so a host serves it once at a stable, cacheable URL (RuntimePath).
// Because the engine serves BOTH this runtime and the Go compiler from one codebase, the two ends
// version together — grammar sync is by construction, not by discipline.
func Runtime() string { return runtimeJS }

// RuntimePath is the built-in, always-on route the runtime is served at, and what Render points
// pages at. Distinct from the verb runtime's /jitjs (jit/js) — this is the expression engine.
const RuntimePath = "/jithydrate"

// The root marker is how a page opts into hydrate. Only when it is present does Render touch the
// page — so static pages (and demos that show data-* as example text) are never affected.
// Dual-alias like every authored attribute: canonical + short.
const (
	rootMarker      = "data-kitwork-hydrate"
	rootMarkerShort = "data-kit-hydrate"
	appMarker       = "data-kitwork-app"
	appMarkerShort  = "data-kit-app"
)

// directiveRe matches an authored EXPRESSION directive in either alias:
// data-kitwork-<name>="<expr>" (canonical) or data-kit-<name>="<expr>" (short).
// Expressions use single-quoted string literals, so the value never contains a double quote.
var directiveRe = regexp.MustCompile(`data-(?:kitwork|kit)-(text|show|click|validate)="([^"]*)"`)

// presenceRe decides runtime INJECTION: it also covers the non-expression attributes — model is a
// plain scope key, live an SSE URL, scope/component a boundary — which need the runtime but must
// never be compile-verified.
var presenceRe = regexp.MustCompile(`data-(?:kitwork|kit)-(?:text|show|click|validate|model|live|scope|component|remember|api)="`)

const injectTag = `<script data-kitwork-jit="hydrate" src="` + RuntimePath + `" defer></script>`

// Render is the server pass for hydrate pages. THE WIRE SHIPS THE SOURCE: authored
// data-kit(work)-* attributes ride to the client unchanged (readable DOM, smaller wire) and the
// client runtime parses them there — no eval, same grammar. What the server does here:
//
//  1. VERIFY — every expression is compiled with the Go compiler at render time, so a typo is
//     caught and logged on the server instead of failing silently in the browser.
//  2. DELIVER — inject the <script src="/jithydrate"> reference once, only when the page actually
//     uses a directive.
//
// IR remains the engine's INTERNAL form (ctx.validate, go tests, analysis) and an optional wire
// mode: the client runtime also reads data-kitwork-*-ir when present — Render just no longer
// emits it. A page WITHOUT the marker (or with no directive) is returned byte-for-byte unchanged.
func Render(html string) string {
	if !strings.Contains(html, rootMarker) && !strings.Contains(html, rootMarkerShort) &&
		!strings.Contains(html, appMarker) && !strings.Contains(html, appMarkerShort) {
		return html
	}
	for _, m := range directiveRe.FindAllStringSubmatch(html, -1) {
		if _, err := Compile(m[2]); err != nil {
			fmt.Printf("[hydrate] %v — in %s\n", err, m[0])
		}
	}
	if !presenceRe.MatchString(html) {
		return html
	}
	// The jit/js pass (which runs earlier in the pipeline) inlines the SAME kernel as the core of
	// its verb bundle — when that block is already on the page, a second reference would be pure
	// duplication (the kernel is boot-guarded anyway, this just saves the bytes). Match the full
	// open tag: the kernel SOURCE mentions the bare marker (mergeHead), so a substring would
	// false-positive on any page that inlines the kernel for other reasons.
	if strings.Contains(html, `<script data-kitwork-jit="js">`) {
		return html
	}
	if i := strings.LastIndex(html, "</head>"); i >= 0 {
		return html[:i] + injectTag + html[i:]
	}
	if i := strings.LastIndex(html, "</body>"); i >= 0 {
		return html[:i] + injectTag + html[i:]
	}
	return html + injectTag
}
